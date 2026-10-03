package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"time"

	"github.com/DrSmithFr/web-ide/pod/internal/execx"
	"github.com/DrSmithFr/web-ide/pod/internal/runtime"
)

// Output kept of a command run for the assistant: its start and its end.
const (
	execHead = 16 * 1024
	execTail = 48 * 1024
)

// capped keeps the first execHead bytes and the last execTail bytes written to it.
type capped struct {
	head, tail []byte
	total      int
}

func (c *capped) Write(p []byte) (int, error) {
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
	if c.total <= execHead+execTail {
		return string(c.head) + string(c.tail), false
	}
	var b bytes.Buffer
	b.Write(c.head)
	b.WriteString("\n… (sortie coupée) …\n")
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
			return nil, errors.New("commande vide")
		}
		dir := rt.Root
		if a.Cwd != "" {
			if dir, err = rt.Abs(a.Cwd); err != nil {
				return nil, err
			}
		}
		limit := time.Duration(min(max(a.Timeout, 1), 1800)) * time.Second
		if a.Timeout <= 0 {
			limit = 120 * time.Second
		}
		start := time.Now()
		proc, err := rt.Runner.Start([]string{"sh", "-c", "{\n" + a.Command + "\n} 2>&1"}, dir)
		if err != nil {
			return nil, err
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
		timedOut, canceled := false, false
		// After a kill, a process left in the background may still hold the output open.
		drain := func() {
			select {
			case <-copied:
			case <-time.After(2 * time.Second):
			}
		}
		select {
		case <-copied:
		case <-timer.C:
			timedOut = true
			_ = proc.Kill()
			drain()
		case <-ctx.Done():
			canceled = true
			_ = proc.Kill()
			drain()
		}
		code := execx.ExitCode(proc.Wait())
		// Read the buffer only once the copy is over (or abandoned).
		text, truncated := out.String()
		return map[string]any{
			"output": text, "code": code, "timedOut": timedOut, "canceled": canceled,
			"truncated": truncated, "durationMs": time.Since(start).Milliseconds(), "cwd": dir,
		}, nil
	}))
}
