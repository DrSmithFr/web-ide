package server

import (
	"context"
	"encoding/json"

	"github.com/DrSmithFr/web-ide/pod/internal/runtime"
)

func (s *Server) registerGit() {
	h := func(f func(ctx context.Context, c *Client, rt *runtime.Runtime, p json.RawMessage) (any, error)) handler {
		return func(ctx context.Context, c *Client, p json.RawMessage) (any, error) {
			rt, err := c.runtime()
			if err != nil {
				return nil, err
			}
			return f(ctx, c, rt, p)
		}
	}
	type pathsArg struct {
		Paths []string `json:"paths"`
	}
	// Every change of the repository is announced to the windows of the project.
	changed := func(c *Client) { s.emitter(c.project)("git.changed", nil, "") }

	s.handle("git.status", h(func(ctx context.Context, c *Client, rt *runtime.Runtime, p json.RawMessage) (any, error) {
		return rt.Git.Status(ctx)
	}))
	s.handle("git.ignored", h(func(ctx context.Context, c *Client, rt *runtime.Runtime, p json.RawMessage) (any, error) {
		a, err := bind[pathsArg](p)
		if err != nil {
			return nil, err
		}
		return rt.Git.Ignored(ctx, a.Paths)
	}))
	s.handle("git.show", h(func(ctx context.Context, c *Client, rt *runtime.Runtime, p json.RawMessage) (any, error) {
		a, err := bind[struct {
			Path string `json:"path"`
			Rev  string `json:"rev"`
		}](p)
		if err != nil {
			return nil, err
		}
		content, ok, err := rt.Git.Show(ctx, a.Path, a.Rev)
		return map[string]any{"content": content, "exists": ok}, err
	}))
	s.handle("git.stage", h(func(ctx context.Context, c *Client, rt *runtime.Runtime, p json.RawMessage) (any, error) {
		a, err := bind[pathsArg](p)
		if err != nil {
			return nil, err
		}
		err = rt.Git.Stage(ctx, a.Paths)
		changed(c)
		return nil, err
	}))
	s.handle("git.unstage", h(func(ctx context.Context, c *Client, rt *runtime.Runtime, p json.RawMessage) (any, error) {
		a, err := bind[pathsArg](p)
		if err != nil {
			return nil, err
		}
		err = rt.Git.Unstage(ctx, a.Paths)
		changed(c)
		return nil, err
	}))
	s.handle("git.discard", h(func(ctx context.Context, c *Client, rt *runtime.Runtime, p json.RawMessage) (any, error) {
		a, err := bind[struct {
			Paths     []string `json:"paths"`
			Untracked []string `json:"untracked"`
		}](p)
		if err != nil {
			return nil, err
		}
		if len(a.Paths) > 0 {
			if err := rt.Git.Discard(ctx, a.Paths); err != nil {
				return nil, err
			}
		}
		for _, u := range a.Untracked {
			if err := rt.Delete(u); err != nil {
				return nil, err
			}
		}
		changed(c)
		return nil, nil
	}))
	s.handle("git.commit", h(func(ctx context.Context, c *Client, rt *runtime.Runtime, p json.RawMessage) (any, error) {
		a, err := bind[struct {
			Message string `json:"message"`
			Amend   bool   `json:"amend"`
		}](p)
		if err != nil {
			return nil, err
		}
		out, err := rt.Git.Commit(ctx, a.Message, a.Amend)
		changed(c)
		return out, err
	}))
	s.handle("git.log", h(func(ctx context.Context, c *Client, rt *runtime.Runtime, p json.RawMessage) (any, error) {
		a, _ := bind[struct {
			N int `json:"n"`
		}](p)
		return rt.Git.Log(ctx, a.N)
	}))
	s.handle("git.branches", h(func(ctx context.Context, c *Client, rt *runtime.Runtime, p json.RawMessage) (any, error) {
		return rt.Git.Branches(ctx)
	}))
	s.handle("git.switch", h(func(ctx context.Context, c *Client, rt *runtime.Runtime, p json.RawMessage) (any, error) {
		a, err := bind[struct {
			Name   string `json:"name"`
			Create bool   `json:"create"`
		}](p)
		if err != nil {
			return nil, err
		}
		err = rt.Git.Switch(ctx, a.Name, a.Create)
		changed(c)
		return nil, err
	}))
	s.handle("git.init", h(func(ctx context.Context, c *Client, rt *runtime.Runtime, p json.RawMessage) (any, error) {
		err := rt.Git.Init(ctx)
		changed(c)
		return nil, err
	}))
}
