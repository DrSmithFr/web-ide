package execx

import (
	"fmt"
	"io"
	"sync"

	"github.com/DrSmithFr/web-ide/pod/internal/keeper"
)

// KeeperProcess is a piped process run by the keeper: it survives the pod, and a pod that
// restarts follows it again (FollowKeeper) from the start of its output.
type KeeperProcess struct {
	ID   string
	c    *keeper.Client
	att  *keeper.Attachment
	mu   sync.Mutex
	cond *sync.Cond
	out  []byte
	end  bool
	code int
}

// StartKeeper runs argv in dir in the keeper for owner, its stdout and stderr together, on
// target ("" local, "ssh:<key>" a connection of the keeper).
func StartKeeper(c *keeper.Client, owner, target string, argv []string, dir string) (*KeeperProcess, error) {
	if target != "" {
		argv, dir = []string{RemoteCmd(argv, dir)}, ""
	}
	p, err := c.Spawn(keeper.Spawn{Owner: owner, Target: target, Argv: argv, Dir: dir})
	if err != nil {
		return nil, err
	}
	return FollowKeeper(c, p.ID)
}

// FollowKeeper follows a process of the keeper from the start of its output.
func FollowKeeper(c *keeper.Client, id string) (*KeeperProcess, error) {
	p := &KeeperProcess{ID: id, c: c}
	p.cond = sync.NewCond(&p.mu)
	att, err := c.Attach(id, 0, func(_ int64, data []byte, _ bool) {
		p.mu.Lock()
		p.out = append(p.out, data...)
		p.cond.Broadcast()
		p.mu.Unlock()
	}, func(code int) {
		p.mu.Lock()
		p.end, p.code = true, code
		p.cond.Broadcast()
		p.mu.Unlock()
	})
	if err != nil {
		return nil, err
	}
	p.att = att
	return p, nil
}

func (p *KeeperProcess) Stdin() io.WriteCloser { return keeperStdin{p} }
func (p *KeeperProcess) Stdout() io.Reader     { return keeperStdout{p} }

// Wait returns once the process ended (the caller forgets it once its result is kept).
func (p *KeeperProcess) Wait() error {
	p.mu.Lock()
	for !p.end {
		p.cond.Wait()
	}
	code := p.code
	p.mu.Unlock()
	if code != 0 {
		return exitError(code)
	}
	return nil
}

func (p *KeeperProcess) Kill() error { return p.c.Signal(p.ID, "KILL") }

type keeperStdin struct{ p *KeeperProcess }

func (s keeperStdin) Write(b []byte) (int, error) {
	if err := s.p.c.Input(s.p.ID, b); err != nil {
		return 0, err
	}
	return len(b), nil
}
func (s keeperStdin) Close() error { return s.p.c.CloseStdin(s.p.ID) }

type keeperStdout struct{ p *KeeperProcess }

func (s keeperStdout) Read(b []byte) (int, error) {
	p := s.p
	p.mu.Lock()
	defer p.mu.Unlock()
	for len(p.out) == 0 && !p.end {
		p.cond.Wait()
	}
	if len(p.out) == 0 {
		return 0, io.EOF
	}
	n := copy(b, p.out)
	p.out = p.out[n:]
	return n, nil
}

// exitError is the end of a process with a code (ExitCode reads it).
type exitError int

func (e exitError) Error() string { return fmt.Sprintf("exit status %d", int(e)) }
func (e exitError) ExitCode() int { return int(e) }

var _ Process = (*KeeperProcess)(nil)
