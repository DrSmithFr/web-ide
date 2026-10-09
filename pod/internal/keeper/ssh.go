package keeper

import (
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/DrSmithFr/web-ide/pod/internal/sshx"
)

// The keeper opens its own SSH connections, so that the terminals and commands of an SSH
// project survive the pod. The pod dials first (its file system, its language servers need
// it) and checks the host key, with its questions; it then gives the keeper that key, pinned,
// and the secrets of the connection (kept in memory, never logged nor listed). A process on
// such a connection has the target "ssh:<key>"; its command is built by the pod.

const OpSSHDial = "ssh.dial"

// SSHDial opens (or keeps) the connection Key to Target.
type SSHDial struct {
	Key     string      `json:"key"`
	Target  sshx.Target `json:"target"`
	Creds   sshx.Creds  `json:"creds,omitempty"`
	HostKey string      `json:"hostKey"` // authorized_keys format
}

func (s *Server) sshDial(a SSHDial) error {
	key, _, _, _, err := ssh.ParseAuthorizedKey([]byte(a.HostKey))
	if err != nil {
		return fmt.Errorf("host key: %v", err)
	}
	s.mu.Lock()
	c := s.sshConns[a.Key]
	s.mu.Unlock()
	if c != nil {
		if _, _, err := c.SendRequest("keepalive@openssh.com", true, nil); err == nil {
			return nil
		}
		c.Close()
	}
	c, err = sshx.DialPinned(a.Target, a.Creds, key)
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.sshConns[a.Key] = c
	s.mu.Unlock()
	go s.keepalive(a.Key, c)
	log.Printf("keeper: SSH connection %s", a.Key)
	return nil
}

// keepalive checks a connection every 30 s; once lost, its processes end (their sessions
// fail) and it is forgotten.
func (s *Server) keepalive(key string, c *ssh.Client) {
	t := time.NewTicker(30 * time.Second)
	defer t.Stop()
	for range t.C {
		if _, _, err := c.SendRequest("keepalive@openssh.com", true, nil); err != nil {
			break
		}
	}
	c.Close()
	s.mu.Lock()
	if s.sshConns[key] == c {
		delete(s.sshConns, key)
	}
	s.mu.Unlock()
	log.Printf("keeper: SSH connection %s lost", key)
}

// spawnSSH starts a process on an SSH connection: Argv[0] is the remote command ("" for the
// login shell of a terminal).
func (s *Server) spawnSSH(p *proc, a Spawn) error {
	key := strings.TrimPrefix(a.Target, "ssh:")
	s.mu.Lock()
	c := s.sshConns[key]
	s.mu.Unlock()
	if c == nil {
		return fmt.Errorf("no SSH connection %s in the keeper", key)
	}
	sess, err := c.NewSession()
	if err != nil {
		return err
	}
	if a.PTY {
		cols, rows := a.Cols, a.Rows
		if cols <= 0 || rows <= 0 {
			cols, rows = 120, 30
		}
		modes := ssh.TerminalModes{ssh.ECHO: 1, ssh.TTY_OP_ISPEED: 115200, ssh.TTY_OP_OSPEED: 115200}
		if err := sess.RequestPty("xterm-256color", rows, cols, modes); err != nil {
			sess.Close()
			return err
		}
	}
	in, err := sess.StdinPipe()
	if err != nil {
		sess.Close()
		return err
	}
	out, err := sess.StdoutPipe()
	if err != nil {
		sess.Close()
		return err
	}
	if !a.PTY {
		sess.Stderr = nil // the command of the pod sends it to stdout (2>&1)
	}
	cmd := ""
	if len(a.Argv) > 0 {
		cmd = a.Argv[0]
	}
	if cmd == "" {
		err = sess.Shell()
	} else {
		err = sess.Start(cmd)
	}
	if err != nil {
		sess.Close()
		return err
	}
	p.sess, p.sin, p.sout = sess, in, out
	return nil
}

// sshWait is the code of an SSH process once ended (-1 when the connection was lost).
func sshWait(sess *ssh.Session) int {
	err := sess.Wait()
	var ee *ssh.ExitError
	switch {
	case err == nil:
		return 0
	case errors.As(err, &ee):
		return ee.ExitStatus()
	}
	return -1
}

func sshSignal(sess *ssh.Session, sig string) error {
	name := map[string]ssh.Signal{"HUP": ssh.SIGHUP, "INT": ssh.SIGINT, "TERM": ssh.SIGTERM, "KILL": ssh.SIGKILL}[sig]
	if name == "" {
		return fmt.Errorf("unknown signal %q", sig)
	}
	_ = sess.Signal(name)
	if sig == "HUP" || sig == "KILL" {
		return sess.Close() // servers that ignore the signals end with the channel
	}
	return nil
}
