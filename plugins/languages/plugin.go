// Package languages is the plugin of the language servers (gopls, typescript-language-server,
// pyright…): code navigation, completion, diagnostics, the Problems and Structure tools. The
// servers of a project (internal/lsp) are a part of its runtime, started on demand, stopped when
// the project is idle; the lsp_* tools of the assistant reach them through the language
// capability (language.go).
package languages

import (
	"context"
	_ "embed"
	"encoding/json"

	"antennae.space/ide/plugins/languages/internal/lsp"
	"antennae.space/ide/pod/plugins"
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

type call struct {
	Lang, Method string
	Params       json.RawMessage
}

func (p *Plugin) Register(h plugins.Host) {
	h.Provide("language", language{})
	h.Handle("lsp.status", func(ctx context.Context, c plugins.Call) (any, error) {
		return serversOf(c.Runtime).Status(), nil
	})
	// The language of a path: no project needed.
	h.HandleGlobal("lsp.langOf", func(ctx context.Context, c plugins.Call) (any, error) {
		a, err := bind[struct{ Path string }](c.Params)
		return lsp.LangOf(a.Path), err
	})
	h.Handle("lsp.capabilities", func(ctx context.Context, c plugins.Call) (any, error) {
		a, err := bind[struct{ Lang string }](c.Params)
		if err != nil {
			return nil, err
		}
		return serversOf(c.Runtime).Capabilities(a.Lang)
	})
	h.Handle("lsp.request", func(ctx context.Context, c plugins.Call) (any, error) {
		a, err := bind[call](c.Params)
		if err != nil {
			return nil, err
		}
		return serversOf(c.Runtime).Request(ctx, a.Lang, a.Method, a.Params)
	})
	h.Handle("lsp.notify", func(ctx context.Context, c plugins.Call) (any, error) {
		a, err := bind[call](c.Params)
		if err != nil {
			return nil, err
		}
		return nil, serversOf(c.Runtime).Notify(a.Lang, a.Method, a.Params)
	})
}
