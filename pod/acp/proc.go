package acp

import (
	"bufio"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"sync"
	"syscall"

	"github.com/DrSmithFr/web-ide/pod/keeper"
)

// proc is the process of an agent: in the keeper (it survives the pod), or a child of the pod.
type proc interface {
	Write(b []byte) error
	// Kill ends the process; its exit is not reported.
	Kill()
	// Detach stops following the process and leaves it running (the pod stops).
	Detach()
}

// events is what a process sends: lines of its standard output (with the offset after the
// line), then its exit. Run by the process queue of its session, never by the reader.
type events struct {
	line func(line []byte, next int64)
	exit func(code int)
}

// lines cuts output chunks into lines; offset is the output offset after the last full line.
type lines struct {
	partial []byte
	offset  int64
}

func (l *lines) feed(offset int64, data []byte, truncated bool, emit func([]byte, int64)) {
	if truncated || offset != l.offset+int64(len(l.partial)) {
		// The keeper lost a part: the line cut there is dropped.
		l.partial = nil
		l.offset = offset
	}
	for len(data) > 0 {
		i := indexByte(data, '\n')
		if i < 0 {
			l.partial = append(l.partial, data...)
			return
		}
		line := append(l.partial, data[:i]...)
		l.partial = nil
		l.offset += int64(len(line)) + 1
		data = data[i+1:]
		emit(line, l.offset)
	}
}

func indexByte(b []byte, c byte) int {
	for i, x := range b {
		if x == c {
			return i
		}
	}
	return -1
}

// ---------- keeper ----------

type keeperProc struct {
	c   *keeper.Client
	id  string
	att *keeper.Attachment
}

// procMeta marks a process of the keeper as the agent of a session.
type procMeta struct {
	Agent string `json:"agent"`
}

func spawnKeeper(c *keeper.Client, owner, session string, argv []string, dir string, env []string) (string, error) {
	meta, _ := json.Marshal(procMeta{Agent: session})
	sp, err := c.Spawn(keeper.Spawn{Owner: owner, Argv: argv, Dir: dir, Env: env, Meta: meta})
	if err != nil {
		return "", err
	}
	return sp.ID, nil
}

// followKeeper follows the process id of the keeper from the offset from.
func followKeeper(c *keeper.Client, id string, from int64, q *queue, ev events) (*keeperProc, error) {
	l := &lines{offset: from}
	att, err := c.Attach(id, from,
		func(offset int64, data []byte, truncated bool) {
			l.feed(offset, data, truncated, func(line []byte, next int64) { q.push(func() { ev.line(line, next) }) })
		},
		func(code int) { q.push(func() { ev.exit(code) }) })
	if err != nil {
		return nil, err
	}
	return &keeperProc{c: c, id: id, att: att}, nil
}

func (p *keeperProc) Write(b []byte) error { return p.c.Input(p.id, b) }

func (p *keeperProc) Kill() {
	p.att.Stop()
	_ = p.c.Forget(p.id)
}

func (p *keeperProc) Detach() { p.att.Stop() }

// ---------- local ----------

type localProc struct {
	cmd   *exec.Cmd
	stdin io.WriteCloser
	mu    sync.Mutex
	done  bool
}

func spawnLocal(argv []string, dir string, env []string, q *queue, ev events) (*localProc, error) {
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	p := &localProc{cmd: cmd, stdin: stdin}
	go func() {
		r := bufio.NewReaderSize(stdout, 64*1024)
		var offset int64
		for {
			line, err := r.ReadBytes('\n')
			if len(line) > 0 && line[len(line)-1] == '\n' {
				offset += int64(len(line))
				l, next := line[:len(line)-1], offset
				q.push(func() { ev.line(l, next) })
			}
			if err != nil {
				break
			}
		}
		code := -1
		if err := cmd.Wait(); err == nil {
			code = 0
		} else if ee, ok := err.(*exec.ExitError); ok {
			code = ee.ExitCode()
		}
		p.mu.Lock()
		done := p.done
		p.mu.Unlock()
		if !done {
			q.push(func() { ev.exit(code) })
		}
	}()
	return p, nil
}

func (p *localProc) Write(b []byte) error {
	_, err := p.stdin.Write(b)
	return err
}

func (p *localProc) Kill() {
	p.mu.Lock()
	p.done = true
	p.mu.Unlock()
	if p.cmd.Process != nil {
		_ = syscall.Kill(-p.cmd.Process.Pid, syscall.SIGKILL)
	}
}

// Detach: a child of the pod ends with it.
func (p *localProc) Detach() { p.Kill() }

// ---------- queue ----------

// queue runs the events of a session in order on its own goroutine: the reader of the keeper
// must never wait for the session (which writes to the keeper).
type queue struct {
	mu     sync.Mutex
	cond   *sync.Cond
	items  []func()
	closed bool
}

func newQueue() *queue {
	q := &queue{}
	q.cond = sync.NewCond(&q.mu)
	go q.run()
	return q
}

func (q *queue) push(f func()) {
	q.mu.Lock()
	if !q.closed {
		q.items = append(q.items, f)
		q.cond.Signal()
	}
	q.mu.Unlock()
}

func (q *queue) close() {
	q.mu.Lock()
	q.closed = true
	q.cond.Signal()
	q.mu.Unlock()
}

func (q *queue) run() {
	for {
		q.mu.Lock()
		for len(q.items) == 0 && !q.closed {
			q.cond.Wait()
		}
		if q.closed {
			q.mu.Unlock()
			return
		}
		f := q.items[0]
		q.items = q.items[1:]
		q.mu.Unlock()
		f()
	}
}
