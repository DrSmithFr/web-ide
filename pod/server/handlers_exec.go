package server

import (
	"context"
	"encoding/json"

	"antennae.space/ide/lib/i18n"
	"antennae.space/ide/pod/runtime"
)

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
		r, err := rt.RunShell(ctx, a.Command, a.Cwd, a.Timeout, nil)
		if err != nil {
			return nil, err
		}
		return map[string]any{
			"output": r.Output, "code": r.Code, "timedOut": r.TimedOut, "canceled": r.Canceled,
			"truncated": r.Truncated, "durationMs": r.DurationMs, "cwd": r.Cwd,
		}, nil
	}))
}
