package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"sync"
	"time"

	"github.com/DrSmithFr/web-ide/pod/internal/execx"
	"github.com/DrSmithFr/web-ide/pod/internal/i18n"
	"github.com/DrSmithFr/web-ide/pod/internal/runtime"
)

// Output kept of a command run for the assistant: its start and its end.
const (
	execHead = 16 * 1024
	execTail = 48 * 1024
)

// capped keeps the first execHead bytes and the last execTail bytes written to it.
type capped struct {
	mu         sync.Mutex
	head, tail []byte
	total      int
}

func (c *capped) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	n0 := len(p)
	c.total += n0
	if room := execHead - len(c.head); room > 0 {
		n := min(room, len(p))
		c.head = append(c.head, p[:n]...)
		p = p[n:]
	}
	c.tail = append(c.tail, p...)
	if over := len(c.tail) - execTail; over > 0 {
		c.tail = append(c.tail[:0], c.tail[over:]...)
	}
	return n0, nil
}

func (c *capped) String() (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.total <= execHead+execTail {
		return string(c.head) + string(c.tail), false
	}
	var b bytes.Buffer
	b.Write(c.head)
	b.WriteString("\n… (output cut) …\n")
	b.Write(c.tail)
	return b.String(), true
}

// withRuntime gives a handler the runtime of the project of the connection.
func withRuntime(f func(ctx context.Context, c *Client, rt *runtime.Runtime, p json.RawMessage) (any, error)) handler {
	return func(ctx context.Context, c *Client, p json.RawMessage) (any, error) {
		rt, err := c.runtime()
		if err != nil {
			return nil, err
		}
		return f(ctx, c, rt, p)
	}
}

func (s *Server) registerExec() {
	// exec.run runs a shell command for the assistant without a terminal: the output
	// (stdout and stderr together) and the exit code are returned once it ends, or when
	// the time limit or a cancellation stops it.
	s.handle("exec.run", withRuntime(func(ctx context.Context, c *Client, rt *runtime.Runtime, p json.RawMessage) (any, error) {
		a, err := bind[struct {
			Command string `json:"command"`
			Cwd     string `json:"cwd"`
			Timeout int    `json:"timeout"`
		}](p)
		if err != nil {
			return nil, err
		}
		if a.Command == "" {
			return nil, i18n.New("empty command")
		}
		r, err := runShell(ctx, rt, a.Command, a.Cwd, a.Timeout, nil)
		if err != nil {
			return nil, err
		}
		return map[string]any{
			"output": r.Output, "code": r.Code, "timedOut": r.TimedOut, "canceled": r.Canceled,
			"truncated": r.Truncated, "durationMs": r.DurationMs, "cwd": r.Cwd,
		}, nil
	}))
}

type shellResult struct {
	Output     string
	Code       int
	TimedOut   bool
	Canceled   bool
	Truncated  bool
	DurationMs int64
	Cwd        string
}

// runShell runs a shell command without a terminal (no input): the output (stdout and stderr
// together) and the exit code once it ends, or when the time limit or ctx stops it.
// runShell runs a command; progress (if any) gets its output so far while it runs.
func runShell(ctx context.Context, rt *runtime.Runtime, command, cwd string, timeout int, progress func(string)) (shellResult, error) {
	var err error
	dir := rt.Root
	if cwd != "" {
		if dir, err = rt.Abs(cwd); err != nil {
			return shellResult{}, err
		}
	}
	limit := time.Duration(min(max(timeout, 1), 1800)) * time.Second
	if timeout <= 0 {
		limit = 120 * time.Second
	}
	start := time.Now()
	proc, err := rt.Runner.Start([]string{"sh", "-c", "{\n" + command + "\n} 2>&1"}, dir)
	if err != nil {
		return shellResult{}, err
	}
	proc.Stdin().Close() // no input: a command waiting for one gets end of file
	out := &capped{}
	copied := make(chan struct{})
	go func() {
		_, _ = io.Copy(out, proc.Stdout())
		close(copied)
	}()
	timer := time.NewTimer(limit)
	defer timer.Stop()
	var tick <-chan time.Time
	if progress != nil {
		ticker := time.NewTicker(500 * time.Millisecond)
		defer ticker.Stop()
		tick = ticker.C
	}
	timedOut, canceled := false, false
	// After a kill, a process left in the background may still hold the output open.
	drain := func() {
		select {
		case <-copied:
		case <-time.After(2 * time.Second):
		}
	}
	sent := 0
wait:
	for {
		select {
		case <-copied:
			break wait
		case <-tick:
			out.mu.Lock()
			total := out.total
			out.mu.Unlock()
			if total != sent {
				sent = total
				text, _ := out.String()
				progress(text)
			}
		case <-timer.C:
			timedOut = true
			_ = proc.Kill()
			drain()
			break wait
		case <-ctx.Done():
			canceled = true
			_ = proc.Kill()
			drain()
			break wait
		}
	}
	code := execx.ExitCode(proc.Wait())
	// Read the buffer only once the copy is over (or abandoned).
	text, truncated := out.String()
	return shellResult{Output: text, Code: code, TimedOut: timedOut, Canceled: canceled, Truncated: truncated, DurationMs: time.Since(start).Milliseconds(), Cwd: dir}, nil
}
