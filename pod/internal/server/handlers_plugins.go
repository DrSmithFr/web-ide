package server

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"sync"

	"github.com/DrSmithFr/web-ide/pod/internal/assistant"
	"github.com/DrSmithFr/web-ide/pod/internal/i18n"
	"github.com/DrSmithFr/web-ide/pod/internal/plugins"
	"github.com/DrSmithFr/web-ide/pod/internal/runtime"
)

// The plugins of the pod (docs/design/plugins.md): their RPC methods and assistant tools,
// registered once, run only on the projects where the plugin is enabled.

// pluginTool is an assistant tool of a plugin, with its definition for the model.
type pluginTool struct {
	plugin string
	tool   plugins.AssistantTool
	def    assistant.Def
}

// pluginSet is the state the server keeps of the plugins.
type pluginSet struct {
	tools []pluginTool // in the order of the registry
	mu    sync.Mutex
	// disabled caches .ide/plugins.json per main project; written by plugins.set.
	disabled map[string][]string
}

// pluginHost is what a plugin registers through.
type pluginHost struct {
	s  *Server
	id string
	m  plugins.Manifest
}

func (h pluginHost) Handle(method string, f plugins.Handler) {
	id := h.id
	if _, taken := h.s.handlers[id+"."+method]; taken {
		panic(fmt.Sprintf("plugin %s: method %s.%s is taken", id, id, method))
	}
	h.s.handle(id+"."+method, withRuntime(func(ctx context.Context, c *Client, rt *runtime.Runtime, p json.RawMessage) (any, error) {
		if !h.s.pluginOn(rt, id) {
			return nil, i18n.Errorf("the %s plugin is disabled on this project", h.m.Name)
		}
		return f(ctx, plugins.Call{Runtime: rt, Params: p, Lang: c.language(), Window: clientWindow{c}})
	}))
}

func (h pluginHost) AssistantTool(t plugins.AssistantTool) {
	if !slices.Contains(h.m.Contributes.AssistantTools, t.Name) {
		panic(fmt.Sprintf("plugin %s: assistant tool %s not in its manifest", h.id, t.Name))
	}
	h.s.plugins.tools = append(h.s.plugins.tools, pluginTool{plugin: h.id, tool: t, def: assistant.Func(t.Name, t.Description, t.Props, t.Required...)})
}

func (s *Server) registerPlugins() {
	if s.Plugins == nil {
		s.Plugins = plugins.NewRegistry()
	}
	s.plugins.disabled = map[string][]string{}
	for _, p := range s.Plugins.All() {
		m := p.Manifest()
		p.Register(pluginHost{s: s, id: m.ID, m: m})
	}
	s.handle("plugins.list", withRuntime(func(ctx context.Context, c *Client, rt *runtime.Runtime, p json.RawMessage) (any, error) {
		return map[string]any{"plugins": s.pluginStates(rt)}, nil
	}))
	s.handle("plugins.set", withRuntime(func(ctx context.Context, c *Client, rt *runtime.Runtime, p json.RawMessage) (any, error) {
		a, err := bind[struct {
			ID      string
			Enabled bool
		}](p)
		if err != nil {
			return nil, err
		}
		if _, ok := s.Plugins.Get(a.ID); !ok {
			return nil, i18n.Errorf("unknown plugin: %s", a.ID)
		}
		s.plugins.mu.Lock()
		list, err := plugins.SetEnabled(rt.FS, rt.IdeDir, a.ID, a.Enabled)
		if err == nil {
			s.plugins.disabled[mainID(rt)] = list
		}
		s.plugins.mu.Unlock()
		if err != nil {
			return nil, err
		}
		states := s.pluginStates(rt)
		s.emitFamily(mainID(rt), "plugins.changed", map[string]any{"plugins": states})
		return map[string]any{"plugins": states}, nil
	}))
}

// pluginStates are the plugins as the project of rt sees them (its main project's settings).
func (s *Server) pluginStates(rt *runtime.Runtime) []plugins.State {
	key := mainID(rt)
	s.plugins.mu.Lock()
	list, ok := s.plugins.disabled[key]
	s.plugins.mu.Unlock()
	if !ok {
		var err error
		if list, err = plugins.LoadDisabled(rt.FS, rt.IdeDir); err != nil {
			list = nil // an invalid file enables every plugin; it is left as it is
		}
		s.plugins.mu.Lock()
		s.plugins.disabled[key] = list
		s.plugins.mu.Unlock()
	}
	return s.Plugins.Resolve(list)
}

func (s *Server) pluginOn(rt *runtime.Runtime, id string) bool {
	return plugins.Enabled(s.pluginStates(rt))[id]
}

// pluginDefs are the definitions of the assistant tools of the plugins enabled on rt's project.
func (s *Server) pluginDefs(rt *runtime.Runtime) []json.RawMessage {
	if len(s.plugins.tools) == 0 {
		return nil
	}
	on := plugins.Enabled(s.pluginStates(rt))
	var out []json.RawMessage
	for _, t := range s.plugins.tools {
		if on[t.plugin] {
			out = append(out, t.def.JSON)
		}
	}
	return out
}

func (s *Server) findPluginTool(name string) *pluginTool {
	for i := range s.plugins.tools {
		if s.plugins.tools[i].tool.Name == name {
			return &s.plugins.tools[i]
		}
	}
	return nil
}

// runPluginTool runs an assistant tool of a plugin on rt's project (the assistant and the MCP).
func (s *Server) runPluginTool(ctx context.Context, rt *runtime.Runtime, t *pluginTool, args json.RawMessage, lang string, options json.RawMessage) (toolResult, error) {
	if !s.pluginOn(rt, t.plugin) {
		m, _ := s.Plugins.Get(t.plugin)
		return toolResult{}, i18n.Errorf("the %s plugin is disabled on this project", m.Manifest().Name)
	}
	res, err := t.tool.Run(ctx, plugins.Call{Runtime: rt, Params: args, Lang: lang, Options: options})
	if err != nil {
		return toolResult{}, err
	}
	if res.Error {
		return toolResult{Content: "Error: " + res.Content, Summary: res.Summary, Status: "error"}, nil
	}
	return toolResult{Content: res.Content, Summary: res.Summary, Status: "ok"}, nil
}

// clientWindow is the window of an RPC call of a plugin.
type clientWindow struct{ c *Client }

func (w clientWindow) Push(event string, data any) { w.c.push(event, data) }
func (w clientWindow) Done() <-chan struct{}       { return w.c.ctx.Done() }
func (w clientWindow) Hold(id string, stop func()) { w.c.streams.Store(id, stop) }
func (w clientWindow) Release(id string) {
	if stop, ok := w.c.streams.LoadAndDelete(id); ok {
		stop.(func())()
	}
}

// pluginOptions are the options of a plugin in a conversation of the assistant.
func pluginOptions(o assistant.Options, plugin string) json.RawMessage {
	if raw, ok := o.Plugins[plugin]; ok {
		return raw
	}
	if plugin == "docker" && o.DockerProfiles != nil { // a page from before the Docker plugin
		data, _ := json.Marshal(map[string]any{"profiles": o.DockerProfiles})
		return data
	}
	return nil
}
