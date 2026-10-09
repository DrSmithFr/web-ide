package keeper

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// The keeper updates itself by re-executing its (new) binary in its own process: same PID,
// so its processes stay its children; their PTY masters and pipes, and the listening socket,
// cross the exec as inherited file descriptors; the rest (output rings, metadata, ended HTTP
// requests) goes through a state file. What cannot cross (HTTP requests in flight) makes the
// update wait for it, or is cancelled with force.

// StateVersion is the format of the state file; a binary reads only its own.
const StateVersion = 1

// StateEnv names the state file to the re-executed keeper.
const StateEnv = "WEBIDE_KEEPER_STATE"

const OpUpgrade = "upgrade"

// KindLog: a line of progress of an upgrade.
const KindLog = "log"

// Upgrade asks for a re-exec of the keeper with the binary at Path ("": its own path).
type Upgrade struct {
	Path  string        `json:"path,omitempty"`
	Force bool          `json:"force,omitempty"`
	Wait  time.Duration `json:"wait,omitempty"`
}

type procState struct {
	Proc
	Fd    int    `json:"fd"`    // PTY master, or read end of the output (-1 once ended)
	Stdin int    `json:"stdin"` // write end of the input of a piped process, else -1
	Buf   []byte `json:"buf"`
	Ended int64  `json:"ended,omitempty"`
}

type relayState struct {
	HTTPInfo
	Head  *httpHead `json:"head,omitempty"`
	Buf   []byte    `json:"buf"`
	Ended int64     `json:"ended,omitempty"`
}

type keeperState struct {
	Version  int          `json:"version"`
	Started  int64        `json:"started"`
	Listener int          `json:"listener"`
	Procs    []procState  `json:"procs"`
	Relays   []relayState `json:"relays"`
}

func (s *Server) accepting() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.upgrading {
		return errors.New("the keeper is updating, try again in a moment")
	}
	return nil
}

func (s *Server) pausing() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.upgrading
}

// fdOf is the descriptor of f without putting it in blocking mode (File.Fd does).
func fdOf(f interface {
	SyscallConn() (syscall.RawConn, error)
}) int {
	rc, err := f.SyscallConn()
	if err != nil {
		return -1
	}
	fd := -1
	_ = rc.Control(func(u uintptr) { fd = int(u) })
	return fd
}

func inherit(fd int, on bool) {
	if fd < 0 {
		return
	}
	flag := 0
	if !on {
		flag = syscall.FD_CLOEXEC
	}
	_, _, _ = syscall.Syscall(syscall.SYS_FCNTL, uintptr(fd), syscall.F_SETFD, uintptr(flag))
}

// blockers are what an exec cannot take: the HTTP requests in flight.
func (s *Server) blockers() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []string
	for _, r := range s.relays {
		if !r.Done {
			out = append(out, "HTTP request "+r.ID)
		}
	}
	return out
}

// UpgradeSelf re-executes the keeper with its own (new) binary (SIGHUP).
func (s *Server) UpgradeSelf() error {
	return s.upgrade(Upgrade{}, func(line string) { log.Printf("keeper: %s", line) })
}

// upgrade re-executes the keeper; it returns only when the exec did not happen (an error).
func (s *Server) upgrade(u Upgrade, report func(string)) error {
	path := u.Path
	if path == "" {
		path = s.exe
	}
	out, err := exec.Command(path, "keeper", "-state-version").Output()
	if err != nil {
		return fmt.Errorf("%s does not answer: %v", path, err)
	}
	if v, _ := strconv.Atoi(strings.TrimSpace(string(out))); v != StateVersion {
		return fmt.Errorf("%s reads the state version %s, this keeper writes %d: restart it instead", path, strings.TrimSpace(string(out)), StateVersion)
	}
	s.mu.Lock()
	if s.upgrading {
		s.mu.Unlock()
		return errors.New("an update is already running")
	}
	s.upgrading = true
	s.mu.Unlock()
	resume := func() {
		s.mu.Lock()
		s.upgrading = false
		s.mu.Unlock()
	}

	// What cannot cross the exec: wait for it, or cancel it.
	wait := u.Wait
	if wait <= 0 {
		wait = 10 * time.Minute
	}
	deadline := time.Now().Add(wait)
	last := ""
	for {
		b := s.blockers()
		if len(b) == 0 {
			break
		}
		if u.Force {
			report("cancelled: " + strings.Join(b, ", "))
			s.mu.Lock()
			for _, r := range s.relays {
				if !r.Done {
					r.cancel()
				}
			}
			s.mu.Unlock()
		} else if time.Now().After(deadline) {
			resume()
			return fmt.Errorf("still running after %s: %s (force cancels them)", wait, strings.Join(b, ", "))
		} else if now := strings.Join(b, ", "); now != last {
			report(fmt.Sprintf("waiting for %s (until %s)", now, deadline.Format("15:04:05")))
			last = now
		}
		time.Sleep(200 * time.Millisecond)
	}

	// The pumps stop reading: what they did not read stays in the kernel for the next keeper.
	s.mu.Lock()
	var running []*proc
	for _, p := range s.procs {
		if !p.Exited {
			p.pumped = make(chan struct{})
			running = append(running, p)
		}
	}
	s.mu.Unlock()
	for _, p := range running {
		_ = p.output().SetReadDeadline(time.Now())
	}
	for _, p := range running {
		select {
		case <-p.pumped:
		case <-time.After(3 * time.Second): // it ended meanwhile (its pump reaps it)
		}
	}
	restart := func() {
		for _, p := range running {
			select {
			case <-p.pumped:
				_ = p.output().SetReadDeadline(time.Time{})
				go s.pump(p)
			default:
			}
		}
	}

	s.mu.Lock()
	st := keeperState{Version: StateVersion, Started: s.started.UnixMilli(), Listener: fdOf(s.ln.(*net.UnixListener))}
	var fds []int
	for _, id := range s.order {
		p := s.procs[id]
		ps := procState{Proc: p.Proc, Fd: -1, Stdin: -1, Buf: p.buf}
		if !p.ended.IsZero() {
			ps.Ended = p.ended.UnixMilli()
		}
		if !p.Exited {
			select {
			case <-p.pumped:
				ps.Fd = fdOf(p.output())
				if p.stdin != nil {
					ps.Stdin = fdOf(p.stdin)
				}
			default:
				ps.Exited, ps.Code = true, -1 // ending: the next keeper cannot wait for it
			}
		}
		fds = append(fds, ps.Fd, ps.Stdin)
		st.Procs = append(st.Procs, ps)
	}
	for _, r := range s.relays {
		rs := relayState{HTTPInfo: r.HTTPInfo, Head: r.head, Buf: r.buf}
		if !r.ended.IsZero() {
			rs.Ended = r.ended.UnixMilli()
		}
		st.Relays = append(st.Relays, rs)
	}
	s.mu.Unlock()
	fds = append(fds, st.Listener)

	statePath := filepath.Join(filepath.Dir(s.ln.Addr().String()), "keeper-state.json")
	data, _ := json.Marshal(st)
	if err := os.WriteFile(statePath, data, 0o600); err != nil {
		restart()
		resume()
		return err
	}
	for _, fd := range fds {
		inherit(fd, true)
	}
	report(fmt.Sprintf("re-executing %s, keeping %d running process(es)", path, len(running)))
	log.Printf("keeper: re-executing %s", path)
	env := append(os.Environ(), StateEnv+"="+statePath)
	err = syscall.Exec(path, os.Args, env)
	// Still here: the exec failed, the keeper goes on as it was.
	for _, fd := range fds {
		inherit(fd, false)
	}
	_ = os.Remove(statePath)
	restart()
	resume()
	return fmt.Errorf("exec %s: %v", path, err)
}

// Restore rebuilds the keeper from the state file left by the one it replaced (same PID):
// its processes, their output, the ended HTTP requests, and the socket it listens on.
func Restore(statePath, version string) (*Server, net.Listener, error) {
	defer os.Unsetenv(StateEnv)
	data, err := os.ReadFile(statePath)
	if err != nil {
		return nil, nil, err
	}
	_ = os.Remove(statePath)
	var st keeperState
	if err := json.Unmarshal(data, &st); err != nil {
		return nil, nil, err
	}
	if st.Version != StateVersion {
		return nil, nil, fmt.Errorf("state version %d, this keeper reads %d", st.Version, StateVersion)
	}
	lf := os.NewFile(uintptr(st.Listener), "keeper.sock")
	ln, err := net.FileListener(lf)
	lf.Close()
	if err != nil {
		return nil, nil, err
	}
	s := NewServer(version)
	s.started = time.UnixMilli(st.Started)
	file := func(fd int, name string) *os.File {
		if fd < 0 {
			return nil
		}
		inherit(fd, false)
		_ = syscall.SetNonblock(fd, true) // pollable: read deadlines work for the next update
		return os.NewFile(uintptr(fd), name)
	}
	for _, ps := range st.Procs {
		p := &proc{Proc: ps.Proc, buf: ps.Buf}
		p.cond = sync.NewCond(&s.mu)
		if ps.Ended > 0 {
			p.ended = time.UnixMilli(ps.Ended)
		}
		if !p.Exited {
			f := file(ps.Fd, p.ID)
			if p.PTY {
				p.pty = f
			} else {
				p.out, p.stdin = f, file(ps.Stdin, p.ID+"-in")
			}
		}
		s.procs[p.ID] = p
		s.order = append(s.order, p.ID)
		if !p.Exited {
			go s.pump(p)
		}
	}
	for _, rs := range st.Relays {
		r := &relay{HTTPInfo: rs.HTTPInfo, head: rs.Head, buf: rs.Buf, cancel: func() {}}
		r.cond = sync.NewCond(&s.mu)
		if rs.Ended > 0 {
			r.ended = time.UnixMilli(rs.Ended)
		}
		s.relays[r.ID] = r
	}
	log.Printf("keeper: restored %d processes and %d requests", len(st.Procs), len(st.Relays))
	return s, ln, nil
}
