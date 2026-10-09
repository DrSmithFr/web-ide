// Package sshtest provides an in-memory SSH server for the tests.
package sshtest

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"os"
	"os/exec"
	"strconv"
	"testing"

	"github.com/creack/pty"
	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

// Start runs a minimal SSH server for the tests: password "pw", sftp subsystem, exec (in a PTY
// when one is asked), shell in a PTY and port forwarding (direct-tcpip, used by the database tunnels). It returns the port.
func Start(t testing.TB) int {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	go Serve(l)
	return l.Addr().(*net.TCPAddr).Port
}

// Serve accepts SSH connections on l until it is closed (also run by the browser tests: sshtestd).
func Serve(l net.Listener) {
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	signer, _ := ssh.NewSignerFromKey(priv)
	cfg := &ssh.ServerConfig{PasswordCallback: func(c ssh.ConnMetadata, pw []byte) (*ssh.Permissions, error) {
		if string(pw) == "pw" {
			return nil, nil
		}
		return nil, errors.New("bad password")
	}}
	cfg.AddHostKey(signer)
	for {
		conn, err := l.Accept()
		if err != nil {
			return
		}
		go func() {
			_, chans, reqs, err := ssh.NewServerConn(conn, cfg)
			if err != nil {
				return
			}
			go ssh.DiscardRequests(reqs)
			for nc := range chans {
				if nc.ChannelType() == "direct-tcpip" {
					go forward(nc)
					continue
				}
				if nc.ChannelType() != "session" {
					nc.Reject(ssh.UnknownChannelType, "")
					continue
				}
				ch, chReqs, _ := nc.Accept()
				go serveSession(ch, chReqs)
			}
		}()
	}
}

func serveSession(ch ssh.Channel, reqs <-chan *ssh.Request) {
	var size *pty.Winsize // asked by pty-req: the shell or the command runs in a PTY
	var tty *os.File
	for req := range reqs {
		switch req.Type {
		case "subsystem":
			req.Reply(true, nil)
			srv, _ := sftp.NewServer(ch)
			srv.Serve()
			ch.Close()
			return
		case "pty-req":
			var r struct {
				Term                      string
				Cols, Rows, Width, Height uint32
				Modes                     string
			}
			_ = ssh.Unmarshal(req.Payload, &r)
			size = &pty.Winsize{Cols: uint16(r.Cols), Rows: uint16(r.Rows)}
			req.Reply(true, nil)
		case "window-change":
			var r struct{ Cols, Rows, Width, Height uint32 }
			_ = ssh.Unmarshal(req.Payload, &r)
			if tty != nil {
				_ = pty.Setsize(tty, &pty.Winsize{Cols: uint16(r.Cols), Rows: uint16(r.Rows)})
			}
		case "signal":
			// Not forwarded: the client closes the channel after it.
			if req.WantReply {
				req.Reply(true, nil)
			}
		case "shell", "exec":
			args := []string{"sh"}
			if req.Type == "exec" {
				n := binary.BigEndian.Uint32(req.Payload[:4])
				args = []string{"sh", "-c", string(req.Payload[4 : 4+n])}
			}
			cmd := exec.Command(args[0], args[1:]...)
			cmd.Env = append(os.Environ(), "SHELL=/bin/sh")
			if size != nil {
				f, err := pty.StartWithSize(cmd, size)
				if err != nil {
					req.Reply(false, nil)
					continue
				}
				tty = f
				req.Reply(true, nil)
				go func() { _, _ = io.Copy(ch, f) }()
				go func() { _, _ = io.Copy(f, ch) }()
				go func() { exitStatus(ch, cmd.Wait()) }()
				continue
			}
			if req.Type == "shell" {
				req.Reply(false, nil)
				continue
			}
			cmd.Stdin, cmd.Stdout, cmd.Stderr = ch, ch, ch.Stderr()
			req.Reply(true, nil)
			exitStatus(ch, cmd.Run())
			return
		default:
			if req.WantReply {
				req.Reply(false, nil)
			}
		}
	}
}

// exitStatus sends the code of a command that ended with err, and closes the channel.
func exitStatus(ch ssh.Channel, err error) {
	code := 0
	if err != nil {
		code = 1
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			code = ee.ExitCode()
		}
	}
	status := make([]byte, 4)
	binary.BigEndian.PutUint32(status, uint32(code))
	ch.SendRequest("exit-status", false, status)
	ch.Close()
}

// forward connects a direct-tcpip channel to its target (RFC 4254 7.2).
func forward(nc ssh.NewChannel) {
	var p struct {
		Host     string
		Port     uint32
		OrigHost string
		OrigPort uint32
	}
	if err := ssh.Unmarshal(nc.ExtraData(), &p); err != nil {
		nc.Reject(ssh.ConnectionFailed, err.Error())
		return
	}
	conn, err := net.Dial("tcp", net.JoinHostPort(p.Host, strconv.Itoa(int(p.Port))))
	if err != nil {
		nc.Reject(ssh.ConnectionFailed, err.Error())
		return
	}
	ch, reqs, err := nc.Accept()
	if err != nil {
		conn.Close()
		return
	}
	go ssh.DiscardRequests(reqs)
	go func() {
		io.Copy(ch, conn)
		ch.CloseWrite()
	}()
	io.Copy(conn, ch)
	conn.Close()
}
