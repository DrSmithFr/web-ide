package keeper

import (
	"bufio"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/creack/pty"
	"golang.org/x/crypto/ssh"
)

// GCAfter: an ended process nobody attached to is dropped after this long.
const GCAfter = time.Hour

// proc is a process of the keeper, known by its pid and its file descriptors only (no
// exec.Cmd): a keeper that re-executes itself rebuilds it (upgrade.go).
type proc struct {
	Proc
	pty   *os.File // master of a PTY
	out   *os.File // read end of the output of a piped process (stdout and stderr)
	stdin *os.File // write end of the input of a piped process
	// An SSH process: its session, input and output.
	sess  *ssh.Session
	sin   io.WriteCloser
	sout  io.Reader
	buf   []byte // output kept: buf[0] is at offset Base
	ended time.Time
	// attached counts the attachments running; cond wakes them on output or exit.
	attached int
	cond     *sync.Cond
	// pumped is closed when the pump stops reading for an update (the output stays in the
	// kernel for the next keeper).
	pumped chan struct{}
}

func (p *proc) output() *os.File {
	if p.pty != nil {
		return p.pty
	}
	return p.out
}

// Server is the keeper: it owns the processes and serves the pod on a Unix socket.
type Server struct {
	Version string

	mu     sync.Mutex
	procs  map[string]*proc
	order  []string
	relays map[string]*relay
	// sshConns: the SSH connections opened for the pod, by key.
	sshConns map[string]*ssh.Client
	started  time.Time
	ln       net.Listener
	conns    map[*serverConn]bool
	closed   bool
	// exe is the binary to re-execute for an update; upgrading: an update is running.
	exe       string
	upgrading bool
}

func NewServer(version string) *Server {
	exe, _ := os.Executable()
	return &Server{exe: exe, Version: version, procs: map[string]*proc{}, relays: map[string]*relay{}, sshConns: map[string]*ssh.Client{}, started: time.Now(), conns: map[*serverConn]bool{}}
}

// Listen opens the socket at path (0600). A socket file left by a keeper that stopped is
// replaced; one that answers, or that another user owns, is not.
func Listen(path string) (net.Listener, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	if st, err := os.Lstat(path); err == nil {
		if sys, ok := st.Sys().(*syscall.Stat_t); !ok || int(sys.Uid) != os.Getuid() {
			return nil, fmt.Errorf("keeper: %s belongs to another user", path)
		}
		if c, err := net.DialTimeout("unix", path, time.Second); err == nil {
			c.Close()
			return nil, fmt.Errorf("keeper: a keeper already answers on %s", path)
		}
		if err := os.Remove(path); err != nil {
			return nil, err
		}
	}
	old := syscall.Umask(0o177)
	ln, err := net.Listen("unix", path)
	syscall.Umask(old)
	if err != nil {
		return nil, err
	}
	return ln, os.Chmod(path, 0o600)
}

// Serve accepts the pods until Close.
func (s *Server) Serve(ln net.Listener) error {
	s.mu.Lock()
	s.ln = ln
	s.mu.Unlock()
	go s.gcLoop()
	for {
		c, err := ln.Accept()
		if err != nil {
			s.mu.Lock()
			closed := s.closed
			s.mu.Unlock()
			if closed {
				return nil
			}
			return err
		}
		sc := &serverConn{s: s, c: c}
		s.mu.Lock()
		s.conns[sc] = true
		s.mu.Unlock()
		go sc.serve()
	}
}

// Close stops serving and ends every process: SIGHUP to the terminals, SIGKILL to the groups
// of the piped ones.
func (s *Server) Close() {
	s.mu.Lock()
	s.closed = true
	if s.ln != nil {
		s.ln.Close()
	}
	procs := make([]*proc, 0, len(s.procs))
	for _, p := range s.procs {
		procs = append(procs, p)
	}
	conns := make([]net.Conn, 0, len(s.conns))
	for c := range s.conns {
		conns = append(conns, c.c)
	}
	s.mu.Unlock()
	for _, c := range conns {
		c.Close()
	}
	for _, p := range procs {
		p.kill()
	}
	s.mu.Lock()
	for k, c := range s.sshConns {
		c.Close()
		delete(s.sshConns, k)
	}
	s.mu.Unlock()
}

func newID() string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func (s *Server) spawn(a Spawn) (Spawned, error) {
	if len(a.Argv) == 0 {
		return Spawned{}, errors.New("argv is empty")
	}
	if err := s.accepting(); err != nil {
		return Spawned{}, err
	}
	cmd := exec.Command(a.Argv[0], a.Argv[1:]...)
	cmd.Dir = a.Dir
	cmd.Env = append(os.Environ(), a.Env...)
	p := &proc{Proc: Proc{ID: newID(), Owner: a.Owner, Target: a.Target, PTY: a.PTY, Meta: a.Meta}}
	p.cond = sync.NewCond(&s.mu)
	if strings.HasPrefix(a.Target, "ssh:") {
		if err := s.spawnSSH(p, a); err != nil {
			return Spawned{}, err
		}
	} else if a.PTY {
		cols, rows := a.Cols, a.Rows
		if cols <= 0 || rows <= 0 {
			cols, rows = 120, 30
		}
		f, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: uint16(cols), Rows: uint16(rows)})
		if err != nil {
			return Spawned{}, err
		}
		p.pty = f
	} else {
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		inR, inW, err := os.Pipe()
		if err != nil {
			return Spawned{}, err
		}
		outR, outW, err := os.Pipe()
		if err != nil {
			inR.Close()
			inW.Close()
			return Spawned{}, err
		}
		cmd.Stdin, cmd.Stdout, cmd.Stderr = inR, outW, outW
		err = cmd.Start()
		inR.Close()
		outW.Close()
		if err != nil {
			inW.Close()
			outR.Close()
			return Spawned{}, err
		}
		p.stdin, p.out = inW, outR
	}
	if p.sess == nil {
		p.Pid = cmd.Process.Pid
	}
	s.mu.Lock()
	s.procs[p.ID] = p
	s.order = append(s.order, p.ID)
	s.mu.Unlock()
	go s.pump(p)
	log.Printf("keeper: %s started (pid %d, %s): %v", p.ID, p.Pid, map[bool]string{true: "pty", false: "pipe"}[p.PTY], a.Argv)
	return Spawned{ID: p.ID, Pid: p.Pid}, nil
}

// pump keeps the output of p in its ring and wakes its attachments; then its exit code. An
// update stops it with a read deadline: it returns, leaving the rest to the next keeper.
func (s *Server) pump(p *proc) {
	var out io.Reader = p.output()
	if p.sess != nil {
		out = p.sout
	}
	b := make([]byte, 32*1024)
	for {
		n, err := out.Read(b)
		if n > 0 {
			s.mu.Lock()
			p.buf = append(p.buf, b[:n]...)
			if over := len(p.buf) - ScrollbackMax; over > 0 {
				p.buf = append([]byte(nil), p.buf[over:]...)
				p.Base += int64(over)
			}
			p.End = p.Base + int64(len(p.buf))
			p.cond.Broadcast()
			s.mu.Unlock()
		}
		if errors.Is(err, os.ErrDeadlineExceeded) && s.pausing() {
			close(p.pumped)
			return
		}
		if err != nil {
			break
		}
	}
	var code int
	switch {
	case p.sess != nil:
		code = sshWait(p.sess)
	default:
		if p.pty == nil {
			p.out.Close()
		}
		code = waitPid(p.Pid)
	}
	s.mu.Lock()
	p.Exited, p.Code, p.ended = true, code, time.Now()
	if p.pty != nil {
		p.pty.Close()
	}
	p.cond.Broadcast()
	s.mu.Unlock()
	log.Printf("keeper: %s ended (code %d)", p.ID, code)
}

// waitPid waits for the end of a child: its exit code, -1 when a signal ended it (or when it
// was reaped already).
func waitPid(pid int) int {
	var ws syscall.WaitStatus
	for {
		_, err := syscall.Wait4(pid, &ws, 0, nil)
		if err == syscall.EINTR {
			continue
		}
		if err != nil || !ws.Exited() {
			return -1
		}
		return ws.ExitStatus()
	}
}

func (p *proc) kill() {
	if p.sess != nil {
		_ = sshSignal(p.sess, "KILL")
		return
	}
	if p.Pid == 0 {
		return
	}
	if p.pty != nil {
		_ = syscall.Kill(p.Pid, syscall.SIGHUP)
		_ = p.pty.Close()
		return
	}
	_ = syscall.Kill(-p.Pid, syscall.SIGKILL)
}

func (s *Server) get(id string) (*proc, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.procs[id]
	if !ok {
		return nil, fmt.Errorf("no process %s", id)
	}
	return p, nil
}

func (s *Server) list(owner string) []Proc {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []Proc{}
	for _, id := range s.order {
		if p := s.procs[id]; owner == "" || p.Owner == owner {
			out = append(out, p.Proc)
		}
	}
	return out
}

func (s *Server) forget(id string) {
	s.mu.Lock()
	p, ok := s.procs[id]
	if ok {
		delete(s.procs, id)
		for i, o := range s.order {
			if o == id {
				s.order = append(s.order[:i], s.order[i+1:]...)
				break
			}
		}
		p.cond.Broadcast()
	}
	s.mu.Unlock()
	if ok && !p.Exited {
		p.kill()
	}
}

func (s *Server) gcLoop() {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for range t.C {
		s.gc(time.Now())
		s.httpGC(time.Now())
	}
}

// gc drops the processes ended for GCAfter that nobody follows.
func (s *Server) gc(now time.Time) {
	s.mu.Lock()
	var old []string
	for id, p := range s.procs {
		if p.Exited && p.attached == 0 && now.Sub(p.ended) > GCAfter {
			old = append(old, id)
		}
	}
	s.mu.Unlock()
	for _, id := range old {
		s.forget(id)
	}
}

// ---------- a connection of the pod ----------

type serverConn struct {
	s      *Server
	c      net.Conn
	wmu    sync.Mutex
	closed bool // under s.mu
}

func (sc *serverConn) send(h header, payload []byte) error {
	sc.wmu.Lock()
	defer sc.wmu.Unlock()
	return writeFrame(sc.c, h, payload)
}

func (sc *serverConn) reply(id int64, v any, err error) {
	if err != nil {
		_ = sc.send(header{ID: id, Kind: KindError, Error: err.Error()}, nil)
		return
	}
	data, _ := json.Marshal(v)
	_ = sc.send(header{ID: id, Kind: KindResult, Result: data}, nil)
}

func (sc *serverConn) serve() {
	s := sc.s
	defer func() {
		sc.c.Close()
		s.mu.Lock()
		sc.closed = true
		delete(s.conns, sc)
		for _, p := range s.procs {
			p.cond.Broadcast() // its attachments see the connection closed
		}
		s.mu.Unlock()
	}()
	r := bufio.NewReader(sc.c)
	for {
		h, payload, err := readFrame(r)
		if err != nil {
			return
		}
		sc.handle(h, payload)
	}
}

func (sc *serverConn) handle(h header, payload []byte) {
	s := sc.s
	var a procArgs
	if h.Op != OpSpawn && h.Op != OpHTTPStart && h.Op != OpUpgrade && h.Op != OpSSHDial && len(h.Args) > 0 {
		if err := json.Unmarshal(h.Args, &a); err != nil {
			sc.reply(h.ID, nil, err)
			return
		}
	}
	switch h.Op {
	case OpHello:
		sc.reply(h.ID, Hello{Protocol: Protocol, Pid: os.Getpid(), Version: s.Version, Started: s.started.UnixMilli()}, nil)
	case OpSpawn:
		var sp Spawn
		if err := json.Unmarshal(h.Args, &sp); err != nil {
			sc.reply(h.ID, nil, err)
			return
		}
		res, err := s.spawn(sp)
		sc.reply(h.ID, res, err)
	case OpList:
		sc.reply(h.ID, s.list(a.Owner), nil)
	case OpAttach:
		p, err := s.get(a.ID)
		if err != nil {
			sc.reply(h.ID, nil, err)
			return
		}
		sc.reply(h.ID, struct{}{}, nil)
		go sc.attach(h.ID, p, a.From)
	case OpInput, OpCloseStdin, OpResize, OpSignal, OpMeta:
		p, err := s.get(a.ID)
		if err == nil {
			err = sc.act(h.Op, p, a, payload)
		}
		sc.reply(h.ID, struct{}{}, err)
	case OpForget:
		s.forget(a.ID)
		sc.reply(h.ID, struct{}{}, nil)
	case OpSSHDial:
		var d SSHDial
		if err := json.Unmarshal(h.Args, &d); err != nil {
			sc.reply(h.ID, nil, err)
			return
		}
		sc.reply(h.ID, struct{}{}, s.sshDial(d))
	case OpUpgrade:
		var u Upgrade
		_ = json.Unmarshal(h.Args, &u)
		err := s.upgrade(u, func(line string) { _ = sc.send(header{ID: h.ID, Kind: KindLog, Error: line}, nil) })
		sc.reply(h.ID, struct{}{}, err)
	case OpHTTPStart:
		var hs HTTPStart
		if err := json.Unmarshal(h.Args, &hs); err != nil {
			sc.reply(h.ID, nil, err)
			return
		}
		sc.reply(h.ID, struct{}{}, s.httpStart(hs, payload))
	case OpHTTPAttach:
		r, err := s.httpGet(a.ID)
		if err != nil {
			sc.reply(h.ID, nil, err)
			return
		}
		sc.reply(h.ID, struct{}{}, nil)
		go sc.httpAttach(h.ID, r, a.From)
	case OpHTTPList:
		sc.reply(h.ID, s.httpList(a.Owner), nil)
	case OpHTTPCancel:
		r, err := s.httpGet(a.ID)
		if err == nil {
			r.cancel()
		}
		sc.reply(h.ID, struct{}{}, err)
	case OpHTTPForget:
		s.httpForget(a.ID)
		sc.reply(h.ID, struct{}{}, nil)
	default:
		sc.reply(h.ID, nil, fmt.Errorf("unknown op %q", h.Op))
	}
}

func (sc *serverConn) act(op string, p *proc, a procArgs, payload []byte) error {
	switch op {
	case OpInput:
		var w io.Writer = p.stdin
		switch {
		case p.sess != nil:
			w = p.sin
		case p.pty != nil:
			w = p.pty
		case p.stdin == nil:
			w = nil
		}
		if w == nil || p.Exited {
			return errors.New("the process has ended")
		}
		_, err := w.Write(payload)
		return err
	case OpCloseStdin:
		if p.sess != nil {
			return p.sin.Close()
		}
		if p.stdin == nil {
			return errors.New("no stdin to close")
		}
		return p.stdin.Close()
	case OpResize:
		if p.sess != nil && p.PTY {
			return p.sess.WindowChange(a.Rows, a.Cols)
		}
		if p.pty == nil {
			return errors.New("not a terminal")
		}
		return pty.Setsize(p.pty, &pty.Winsize{Cols: uint16(a.Cols), Rows: uint16(a.Rows)})
	case OpSignal:
		if p.sess != nil {
			return sshSignal(p.sess, a.Signal)
		}
		switch a.Signal {
		case "HUP":
			if p.pty != nil {
				p.kill()
				return nil
			}
			return syscall.Kill(-p.Pid, syscall.SIGHUP)
		case "INT":
			return syscall.Kill(-p.Pid, syscall.SIGINT)
		case "TERM":
			return syscall.Kill(-p.Pid, syscall.SIGTERM)
		case "KILL":
			return syscall.Kill(-p.Pid, syscall.SIGKILL)
		}
		return fmt.Errorf("unknown signal %q", a.Signal)
	case OpMeta:
		sc.s.mu.Lock()
		p.Meta = a.Meta
		sc.s.mu.Unlock()
	}
	return nil
}

// attach sends the output of p from the offset from (from its oldest byte kept, said
// truncated, when from is older), then what comes, then its exit.
func (sc *serverConn) attach(id int64, p *proc, from int64) {
	s := sc.s
	s.mu.Lock()
	p.attached++
	defer func() {
		p.attached--
		s.mu.Unlock()
	}()
	truncated := from < p.Base
	if truncated {
		from = p.Base
	}
	for {
		for from >= p.End && !p.Exited && !sc.closed && s.procs[p.ID] == p {
			p.cond.Wait()
		}
		if sc.closed || s.procs[p.ID] != p {
			return
		}
		if from < p.Base { // the ring turned while this attachment was writing
			from, truncated = p.Base, true
		}
		if from < p.End {
			chunk := append([]byte(nil), p.buf[from-p.Base:]...)
			if len(chunk) > 64*1024 {
				chunk = chunk[:64*1024]
			}
			h := header{ID: id, Kind: KindOutput, Offset: from, Truncated: truncated}
			from += int64(len(chunk))
			truncated = false
			s.mu.Unlock()
			err := sc.send(h, chunk)
			s.mu.Lock()
			if err != nil {
				return
			}
			continue
		}
		code := p.Code
		s.mu.Unlock()
		_ = sc.send(header{ID: id, Kind: KindExit, Code: code}, nil)
		s.mu.Lock()
		return
	}
}
