// Package hello is the plugin of the browser tests (WEBIDE_TEST_PLUGINS): a tool, an RPC
// method and an assistant tool, the smallest use of every contribution point of the core.
package hello

import (
	"context"
	_ "embed"
	"encoding/json"

	"github.com/DrSmithFr/web-ide/pod/assistant"
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

func (p *Plugin) Register(h plugins.Host) {
	h.Handle("greet", func(ctx context.Context, c plugins.Call) (any, error) {
		return map[string]string{"text": greet(c)}, nil
	})
	h.AssistantTool(plugins.AssistantTool{
		Name:        "hello_greet",
		Description: "Greets someone from the project (a test tool).",
		Props:       map[string]any{"name": map[string]any{"type": "string", "description": "Who to greet"}},
		Run: func(ctx context.Context, c plugins.Call) (plugins.Result, error) {
			text := greet(c)
			return plugins.Result{Content: text, Summary: assistant.T("greeted", nil).Raw()}, nil
		},
	})
}

func greet(c plugins.Call) string {
	var a struct{ Name string }
	_ = json.Unmarshal(c.Params, &a)
	if a.Name == "" {
		a.Name = "world"
	}
	return "Hello " + a.Name + " from " + c.Runtime.P.Name()
}
