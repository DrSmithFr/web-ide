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
	"sync"
	"syscall"
	"time"

	"github.com/creack/pty"
)

// GCAfter: an ended process nobody attached to is dropped after this long.
const GCAfter = time.Hour

type proc struct {
	Proc
	cmd   *exec.Cmd
	pty   *os.File       // master of a PTY
	stdin io.WriteCloser // of a piped process
	buf   []byte         // output kept: buf[0] is at offset Base
	ended time.Time
	// attached counts the attachments running; cond wakes them on output or exit.
	attached int
	cond     *sync.Cond
}

// Server is the keeper: it owns the processes and serves the pod on a Unix socket.
type Server struct {
	Version string

	mu      sync.Mutex
	procs   map[string]*proc
	order   []string
	relays  map[string]*relay
	started time.Time
	ln      net.Listener
	conns   map[*serverConn]bool
	closed  bool
}

func NewServer(version string) *Server {
	return &Server{Version: version, procs: map[string]*proc{}, relays: map[string]*relay{}, started: time.Now(), conns: map[*serverConn]bool{}}
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
	cmd := exec.Command(a.Argv[0], a.Argv[1:]...)
	cmd.Dir = a.Dir
	cmd.Env = append(os.Environ(), a.Env...)
	p := &proc{Proc: Proc{ID: newID(), Owner: a.Owner, PTY: a.PTY, Meta: a.Meta}, cmd: cmd}
	p.cond = sync.NewCond(&s.mu)
	var out io.Reader
	if a.PTY {
		cols, rows := a.Cols, a.Rows
		if cols <= 0 || rows <= 0 {
			cols, rows = 120, 30
		}
		f, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: uint16(cols), Rows: uint16(rows)})
		if err != nil {
			return Spawned{}, err
		}
		p.pty, out = f, f
	} else {
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		stdin, err := cmd.StdinPipe()
		if err != nil {
			return Spawned{}, err
		}
		r, w, err := os.Pipe()
		if err != nil {
			return Spawned{}, err
		}
		cmd.Stdout, cmd.Stderr = w, w
		if err := cmd.Start(); err != nil {
			r.Close()
			w.Close()
			return Spawned{}, err
		}
		w.Close()
		p.stdin, out = stdin, r
	}
	p.Pid = cmd.Process.Pid
	s.mu.Lock()
	s.procs[p.ID] = p
	s.order = append(s.order, p.ID)
	s.mu.Unlock()
	go s.pump(p, out)
	log.Printf("keeper: %s started (pid %d, %s): %v", p.ID, p.Pid, map[bool]string{true: "pty", false: "pipe"}[p.PTY], a.Argv)
	return Spawned{ID: p.ID, Pid: p.Pid}, nil
}

// pump keeps the output of p in its ring and wakes its attachments; then its exit code.
func (s *Server) pump(p *proc, out io.Reader) {
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
		if err != nil {
			break
		}
	}
	if c, ok := out.(io.Closer); ok && p.pty == nil {
		c.Close()
	}
	err := p.cmd.Wait()
	code := 0
	var ee *exec.ExitError
	switch {
	case errors.As(err, &ee):
		code = ee.ExitCode()
	case err != nil:
		code = -1
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

func (p *proc) kill() {
	if p.cmd.Process == nil {
		return
	}
	if p.pty != nil {
		_ = p.cmd.Process.Signal(syscall.SIGHUP)
		_ = p.pty.Close()
		return
	}
	_ = syscall.Kill(-p.cmd.Process.Pid, syscall.SIGKILL)
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
	if h.Op != OpSpawn && h.Op != OpHTTPStart && len(h.Args) > 0 {
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
		w := io.Writer(p.stdin)
		if p.pty != nil {
			w = p.pty
		}
		if w == nil || p.Exited {
			return errors.New("the process has ended")
		}
		_, err := w.Write(payload)
		return err
	case OpCloseStdin:
		if p.stdin == nil {
			return errors.New("no stdin to close")
		}
		return p.stdin.Close()
	case OpResize:
		if p.pty == nil {
			return errors.New("not a terminal")
		}
		return pty.Setsize(p.pty, &pty.Winsize{Cols: uint16(a.Cols), Rows: uint16(a.Rows)})
	case OpSignal:
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
