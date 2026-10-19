// Package git is the Git plugin: the Git tool (changes, commits, history, branches) and the git
// content of the diff view; it provides the worktrees capability. The git commands are run by
// pod/internal/git through the runtime of the project; the kanban runs its own.
package git

import (
	"context"
	_ "embed"
	"encoding/json"

	"github.com/DrSmithFr/web-ide/pod/internal/plugins"
)

//go:embed plugin.json
var manifest []byte

type Plugin struct{ m plugins.Manifest }

func New() *Plugin {
	m, err := plugins.Parse(manifest)
	if err != nil {
		panic(err)
	}
	return &Plugin{m: m}
}

func (p *Plugin) Manifest() plugins.Manifest { return p.m }

func bind[T any](raw json.RawMessage) (T, error) {
	var v T
	if len(raw) == 0 || string(raw) == "null" {
		return v, nil
	}
	err := json.Unmarshal(raw, &v)
	return v, err
}

func (p *Plugin) Register(h plugins.Host) {
	type pathsArg struct {
		Paths []string `json:"paths"`
	}
	// Every change of the repository is announced to the windows of the project.
	changed := func(c plugins.Call) { c.Emit("git.changed", nil) }

	h.Handle("status", func(ctx context.Context, c plugins.Call) (any, error) {
		return c.Runtime.Git.Status(ctx)
	})
	h.Handle("ignored", func(ctx context.Context, c plugins.Call) (any, error) {
		rt, p := c.Runtime, c.Params
		a, err := bind[pathsArg](p)
		if err != nil {
			return nil, err
		}
		return rt.Git.Ignored(ctx, a.Paths)
	})
	h.Handle("show", func(ctx context.Context, c plugins.Call) (any, error) {
		rt, p := c.Runtime, c.Params
		a, err := bind[struct {
			Path string `json:"path"`
			Rev  string `json:"rev"`
		}](p)
		if err != nil {
			return nil, err
		}
		content, ok, err := rt.Git.Show(ctx, a.Path, a.Rev)
		return map[string]any{"content": content, "exists": ok}, err
	})
	h.Handle("stage", func(ctx context.Context, c plugins.Call) (any, error) {
		rt, p := c.Runtime, c.Params
		a, err := bind[pathsArg](p)
		if err != nil {
			return nil, err
		}
		err = rt.Git.Stage(ctx, a.Paths)
		changed(c)
		return nil, err
	})
	h.Handle("unstage", func(ctx context.Context, c plugins.Call) (any, error) {
		rt, p := c.Runtime, c.Params
		a, err := bind[pathsArg](p)
		if err != nil {
			return nil, err
		}
		err = rt.Git.Unstage(ctx, a.Paths)
		changed(c)
		return nil, err
	})
	h.Handle("discard", func(ctx context.Context, c plugins.Call) (any, error) {
		rt, p := c.Runtime, c.Params
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
	})
	h.Handle("commit", func(ctx context.Context, c plugins.Call) (any, error) {
		rt, p := c.Runtime, c.Params
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
	})
	h.Handle("log", func(ctx context.Context, c plugins.Call) (any, error) {
		rt, p := c.Runtime, c.Params
		a, _ := bind[struct {
			Skip  int    `json:"skip"`
			N     int    `json:"n"`
			Query string `json:"query"`
		}](p)
		return rt.Git.Log(ctx, a.Skip, a.N, a.Query)
	})
	type revArg struct {
		Rev  string `json:"rev"`
		Mode string `json:"mode"`
	}
	h.Handle("commitInfo", func(ctx context.Context, c plugins.Call) (any, error) {
		rt, p := c.Runtime, c.Params
		a, err := bind[revArg](p)
		if err != nil {
			return nil, err
		}
		return rt.Git.CommitInfo(ctx, a.Rev)
	})
	h.Handle("revert", func(ctx context.Context, c plugins.Call) (any, error) {
		rt, p := c.Runtime, c.Params
		a, err := bind[revArg](p)
		if err != nil {
			return nil, err
		}
		out, err := rt.Git.Revert(ctx, a.Rev)
		changed(c)
		return out, err
	})
	h.Handle("reset", func(ctx context.Context, c plugins.Call) (any, error) {
		rt, p := c.Runtime, c.Params
		a, err := bind[revArg](p)
		if err != nil {
			return nil, err
		}
		err = rt.Git.Reset(ctx, a.Rev, a.Mode)
		changed(c)
		return nil, err
	})
	h.Handle("branches", func(ctx context.Context, c plugins.Call) (any, error) {
		rt := c.Runtime
		return rt.Git.Branches(ctx)
	})
	h.Handle("switch", func(ctx context.Context, c plugins.Call) (any, error) {
		rt, p := c.Runtime, c.Params
		a, err := bind[struct {
			Name   string `json:"name"`
			Create bool   `json:"create"`
			From   string `json:"from"`
		}](p)
		if err != nil {
			return nil, err
		}
		err = rt.Git.Switch(ctx, a.Name, a.Create, a.From)
		changed(c)
		return nil, err
	})
	h.Handle("init", func(ctx context.Context, c plugins.Call) (any, error) {
		rt := c.Runtime
		err := rt.Git.Init(ctx)
		changed(c)
		return nil, err
	})
}
