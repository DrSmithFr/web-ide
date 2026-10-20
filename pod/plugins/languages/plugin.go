// Package languages is the plugin of the language servers (gopls, typescript-language-server,
// pyright…): code navigation, completion, diagnostics, the Problems and Structure tools. The
// servers are started on demand by the runtime of the project (pod/lsp), which stops
// them when the project is idle.
package languages

import (
	"context"
	_ "embed"
	"encoding/json"

	"github.com/DrSmithFr/web-ide/pod/lsp"
	"github.com/DrSmithFr/web-ide/pod/plugins"
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
	h.Handle("lsp.status", func(ctx context.Context, c plugins.Call) (any, error) {
		return c.Runtime.LSP.Status(), nil
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
		return c.Runtime.LSP.Capabilities(a.Lang)
	})
	h.Handle("lsp.request", func(ctx context.Context, c plugins.Call) (any, error) {
		a, err := bind[call](c.Params)
		if err != nil {
			return nil, err
		}
		return c.Runtime.LSP.Request(ctx, a.Lang, a.Method, a.Params)
	})
	h.Handle("lsp.notify", func(ctx context.Context, c plugins.Call) (any, error) {
		a, err := bind[call](c.Params)
		if err != nil {
			return nil, err
		}
		return nil, c.Runtime.LSP.Notify(a.Lang, a.Method, a.Params)
	})
}
