package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/DrSmithFr/web-ide/pod/commands"

	"github.com/DrSmithFr/web-ide/lib/i18n"
	"github.com/DrSmithFr/web-ide/pod/assistant"
	"github.com/DrSmithFr/web-ide/pod/plugins"
	"github.com/DrSmithFr/web-ide/pod/plugins/builtin"
	"github.com/DrSmithFr/web-ide/pod/plugins/install"
	"github.com/DrSmithFr/web-ide/pod/runtime"
	"github.com/DrSmithFr/web-ide/pod/settingsync"
	"github.com/DrSmithFr/web-ide/pod/sshx"
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
	// mcp are the tools of the plugins offered to the MCP only (Host.MCPTool).
	mcp []pluginMCPTool
	// The lifecycle hooks of the plugins: a project opened (by plugin), the windows counted.
	onOpen    []pluginHook
	onWindows []func(n int)
	// The pod ready, the pod stopping.
	onStart, onStop []func()
	mu              sync.Mutex
	// choices caches .ide/plugins.json per main project; written by plugins.set.
	choices map[string]plugins.Choices
	// provided are the capabilities the plugins gave (Host.Provide), by name; set at registration.
	provided map[string]any
	// preview serves a preview asked through the tunnel (Host.HandlePreview: the Previews plugin).
	preview func(id string) (http.Handler, bool)
	// questions are the sources of the Questions tool (Host.Questions), vault the maps of the
	// synced vault (Host.Vault).
	questions []pluginQuestions
	vault     []settingsync.VaultMap
	// installer installs the external plugins (handlers_plugins_install.go), one change of the
	// lock at a time.
	installer *install.Installer
	installMu sync.Mutex
}

// pluginQuestions is a source of the Questions tool given by a plugin.
type pluginQuestions struct {
	plugin string
	q      plugins.Questions
}

// pluginHost is what a plugin registers through.
type pluginHost struct {
	s  *Server
	id string
	m  plugins.Manifest
}

func (h pluginHost) Handle(method string, f plugins.Handler) {
	id := h.id
	name := id + "." + method
	if strings.Contains(method, ".") {
		if !h.m.Owns(method) {
			panic(fmt.Sprintf("plugin %s: method %s is out of its namespaces", id, method))
		}
		name = method
	}
	if _, taken := h.s.handlers[name]; taken {
		panic(fmt.Sprintf("plugin %s: method %s is taken", id, name))
	}
	h.s.handle(name, withRuntime(func(ctx context.Context, c *Client, rt *runtime.Runtime, p json.RawMessage) (any, error) {
		if !h.s.pluginOn(rt, id) {
			return nil, i18n.Errorf("the %s plugin is disabled on this project", h.m.Name)
		}
		emit := h.s.emitter(c.project)
		return f(ctx, plugins.Call{Runtime: rt, Params: p, Lang: c.language(), Window: clientWindow{c},
			Emit:       func(event string, data any) { emit(event, data, "") },
			EmitOthers: func(event string, data any) { emit(event, data, c.id) },
			EmitFamily: func(event string, data any) { h.s.emitFamily(mainID(rt), event, data) }})
	}))
}

func (h pluginHost) AssistantTool(t plugins.AssistantTool) {
	if !slices.Contains(h.m.Contributes.AssistantTools, t.Name) {
		panic(fmt.Sprintf("plugin %s: assistant tool %s not in its manifest", h.id, t.Name))
	}
	h.s.plugins.tools = append(h.s.plugins.tools, pluginTool{plugin: h.id, tool: t, def: assistant.Func(t.Name, t.Description, t.Props, t.Required...)})
}

// pluginMCPTool is a tool of a plugin offered to the MCP only.
type pluginMCPTool struct {
	plugin string
	tool   plugins.MCPTool
}

func (h pluginHost) MCPTool(t plugins.MCPTool) {
	if !slices.Contains(h.m.Contributes.MCPTools, t.Name) {
		panic(fmt.Sprintf("plugin %s: MCP tool %s not in its manifest", h.id, t.Name))
	}
	h.s.plugins.mcp = append(h.s.plugins.mcp, pluginMCPTool{plugin: h.id, tool: t})
}

func (s *Server) findPluginMCPTool(name string) *pluginMCPTool {
	for i := range s.plugins.mcp {
		if s.plugins.mcp[i].tool.Name == name {
			return &s.plugins.mcp[i]
		}
	}
	return nil
}

// runPluginMCPTool runs a tool of a plugin offered to the MCP in the project of the folder of
// the client: refused where the plugin is disabled, or the capability it needs is off.
func (s *Server) runPluginMCPTool(ctx context.Context, t *pluginMCPTool, args json.RawMessage) (string, error) {
	a, err := bind[struct {
		Cwd string `json:"cwd"`
	}](args)
	if err != nil {
		return "", err
	}
	sc, err := s.mcpScope(a.Cwd)
	if err != nil {
		return "", err
	}
	rt, err := s.openRuntime(sc.here.ID, sshx.Creds{})
	if err != nil {
		return "", err
	}
	if !s.pluginOn(rt, t.plugin) {
		m, _ := s.Plugins.Get(t.plugin)
		return "", i18n.Errorf("the %s plugin is disabled on this project", m.Manifest().Name)
	}
	if c := t.tool.Needs; c != "" && !s.capabilityOn(rt, c) {
		return "", i18n.Errorf("%s needs the %s capability, off on this project (Settings > Plugins)", t.tool.Name, c)
	}
	return t.tool.Run(ctx, plugins.MCPScope{Here: sc.here, Root: sc.root, Ticket: sc.ticket, URL: s.publicURL(), Lang: s.uiLanguage()}, args)
}

func (s *Server) registerPlugins() {
	if s.Plugins == nil {
		s.Plugins = builtin.Registry(false)
	}
	s.plugins.choices = map[string]plugins.Choices{}
	for _, p := range s.Plugins.All() {
		m := p.Manifest()
		p.Register(pluginHost{s: s, id: m.ID, m: m})
	}
	s.registerPluginInstall()
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
		var ch plugins.Choices
		url := s.externalURL(rt, a.ID)
		s.plugins.mu.Lock()
		if pl, ok := s.Plugins.Get(a.ID); ok {
			if pl.Manifest().Required && !a.Enabled {
				s.plugins.mu.Unlock()
				return nil, i18n.Errorf("the %s plugin is always on: the IDE needs it", pl.Manifest().Name)
			}
			ch, err = plugins.SetEnabled(rt.FS, rt.IdeDir, a.ID, a.Enabled)
		} else if url != "" {
			// An external plugin: the project uses it or not (it runs from #129 on).
			if !a.Enabled {
				url = ""
			}
			ch, err = plugins.SetExternal(rt.FS, rt.IdeDir, a.ID, url)
		} else {
			s.plugins.mu.Unlock()
			return nil, i18n.Errorf("unknown plugin: %s", a.ID)
		}
		if err == nil {
			s.plugins.choices[mainID(rt)] = ch
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
	return s.Plugins.ResolveChoices(s.pluginChoices(rt))
}

// pluginChoices are the choices of .ide/plugins.json of the main project of rt, cached.
func (s *Server) pluginChoices(rt *runtime.Runtime) plugins.Choices {
	key := mainID(rt)
	s.plugins.mu.Lock()
	ch, ok := s.plugins.choices[key]
	s.plugins.mu.Unlock()
	if !ok {
		var err error
		if ch, err = plugins.LoadChoices(rt.FS, rt.IdeDir); err != nil {
			ch = plugins.Choices{} // an invalid file enables every plugin; it is left as it is
		}
		s.plugins.mu.Lock()
		s.plugins.choices[key] = ch
		s.plugins.mu.Unlock()
	}
	return ch
}

// externalURL is the repository of an external plugin: the pod's installed one, else the one
// the project names ("" when neither knows it).
func (s *Server) externalURL(rt *runtime.Runtime, id string) string {
	if e, ok := s.Plugins.External(id); ok {
		return e.URL
	}
	return s.pluginChoices(rt).External[id]
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
func (s *Server) runPluginTool(ctx context.Context, rt *runtime.Runtime, t *pluginTool, args json.RawMessage, lang string, options json.RawMessage) (assistant.ToolResult, error) {
	if !s.pluginOn(rt, t.plugin) {
		m, _ := s.Plugins.Get(t.plugin)
		return assistant.ToolResult{}, i18n.Errorf("the %s plugin is disabled on this project", m.Manifest().Name)
	}
	res, err := t.tool.Run(ctx, plugins.Call{Runtime: rt, Params: args, Lang: lang, Options: options})
	if err != nil {
		return assistant.ToolResult{}, err
	}
	if res.Error {
		return assistant.ToolResult{Content: "Error: " + res.Content, Summary: res.Summary, Status: "error"}, nil
	}
	return assistant.ToolResult{Content: res.Content, Summary: res.Summary, Status: "ok"}, nil
}

// clientWindow is the window of an RPC call of a plugin.
type clientWindow struct{ c *Client }

func (w clientWindow) Push(event string, data any) { w.c.push(event, data) }
func (w clientWindow) Done() <-chan struct{}       { return w.c.ctx.Done() }
func (w clientWindow) Hold(id string, stop func()) { w.c.streams.Store(id, stop) }
func (w clientWindow) Project() string             { return w.c.project }
func (w clientWindow) ID() string                  { return w.c.id }
func (w clientWindow) Watch(chat string)           { w.c.watch(chat) }
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

// capabilityOn tells whether the project of rt has a capability (terminal: its consoles).
func (s *Server) capabilityOn(rt *runtime.Runtime, capability string) bool {
	return plugins.Provided(s.pluginStates(rt))[capability]
}

// toolNeeds are the tools of the MCP endpoint that need a capability: the consoles and saved
// commands need terminal (the Exec plugin), the previews previews, a doodle the AI. The AI plugin checks its own
// tools the same way (plugins/ai/needs.go).
var toolNeeds = map[string]string{
	"list_consoles": "terminal", "read_console": "terminal", "console_input": "terminal",
	"list_commands": "terminal", "run_saved_command": "terminal",
	"share_preview": "previews", "list_previews": "previews",
	// The doodle the user draws is the AI's (its modal).
	"request_doodle": "assistant",
}

// needsOf is the capability a core tool of the MCP needs.
func needsOf(tool string) string { return toolNeeds[tool] }

// missingFor is the error of a core tool whose capability the project of rt lacks (nil: it runs).
func (s *Server) missingFor(rt *runtime.Runtime, tool string) error {
	if c := needsOf(tool); c != "" && !s.capabilityOn(rt, c) {
		return i18n.Errorf("%s needs the %s capability, off on this project (Settings > Plugins)", tool, c)
	}
	return nil
}

// offered drops the definitions of the core tools whose capability the project of rt lacks.
func (s *Server) offered(rt *runtime.Runtime, defs []json.RawMessage) []json.RawMessage {
	out := defs[:0:0]
	for _, d := range defs {
		var f struct {
			Function struct{ Name string } `json:"function"`
		}
		_ = json.Unmarshal(d, &f)
		if s.missingFor(rt, f.Function.Name) == nil {
			out = append(out, d)
		}
	}
	return out
}

func (h pluginHost) HandleGlobal(method string, f plugins.Handler) {
	name := method
	if !h.m.Owns(method) {
		panic(fmt.Sprintf("plugin %s: method %s is out of its namespaces", h.id, method))
	}
	if _, taken := h.s.handlers[name]; taken {
		panic(fmt.Sprintf("plugin %s: method %s is taken", h.id, name))
	}
	h.s.handle(name, func(ctx context.Context, c *Client, p json.RawMessage) (any, error) {
		return f(ctx, plugins.Call{Params: p, Lang: c.language(), Window: clientWindow{c},
			Emit:       func(event string, data any) { h.s.broadcast(event, data, nil) },
			EmitOthers: func(event string, data any) { h.s.broadcast(event, data, nil) },
			EmitFamily: func(event string, data any) { h.s.broadcast(event, data, nil) }})
	})
}

func (h pluginHost) Pod() plugins.Pod { return podLend{h.s} }

func (h pluginHost) Questions(q plugins.Questions) {
	h.s.plugins.questions = append(h.s.plugins.questions, pluginQuestions{plugin: h.id, q: q})
}

func (h pluginHost) Vault(m settingsync.VaultMap) { h.s.plugins.vault = append(h.s.plugins.vault, m) }

// VaultMaps are the maps the plugins keep in the synced vault (main.go makes its source).
func (s *Server) VaultMaps() []settingsync.VaultMap { return s.plugins.vault }

// podLend is the pod lent to the built-in plugins.
type podLend struct{ s *Server }

func (p podLend) Runtime(project string) (*runtime.Runtime, error) {
	return p.s.openRuntime(project, sshx.Creds{})
}

func (p podLend) OpenRuntime(project string) *runtime.Runtime {
	p.s.mu.Lock()
	defer p.s.mu.Unlock()
	return p.s.runtimes[project]
}

func (p podLend) Project(id string) (name, host string, ok bool) {
	pr, ok := p.s.Projects.Get(id)
	if !ok {
		return "", "", false
	}
	if pr.SSH != nil {
		host = pr.SSH.Host
	}
	return pr.Name(), host, true
}

func (p podLend) Broadcast(event string, data any)     { p.s.broadcast(event, data, nil) }
func (p podLend) Emit(project, event string, data any) { p.s.emitter(project)(event, data, "") }
func (p podLend) Authorized(r *http.Request) bool      { return p.s.authorized(r) }

type pluginHook struct {
	plugin string
	f      func(rt *runtime.Runtime)
}

func (h pluginHost) OnProjectOpen(f func(rt *runtime.Runtime)) {
	h.s.plugins.onOpen = append(h.s.plugins.onOpen, pluginHook{h.id, f})
}

func (h pluginHost) OnWindows(f func(n int)) {
	h.s.plugins.onWindows = append(h.s.plugins.onWindows, f)
}

func (h pluginHost) HandlePreview(f func(id string) (http.Handler, bool)) { h.s.plugins.preview = f }

func (h pluginHost) Provide(c string, impl any) {
	if !slices.Contains(h.m.Provides, c) {
		panic(fmt.Sprintf("plugin %s: capability %s not in the provides of its manifest", h.id, c))
	}
	if h.s.plugins.provided == nil {
		h.s.plugins.provided = map[string]any{}
	}
	h.s.plugins.provided[c] = impl
}

func (h pluginHost) OnStart(f func()) { h.s.plugins.onStart = append(h.s.plugins.onStart, f) }
func (h pluginHost) OnStop(f func())  { h.s.plugins.onStop = append(h.s.plugins.onStop, f) }

// Started tells the plugins the pod is ready (the AI takes back the conversations that ran
// when the pod stopped). Called once by main, before the pod listens.
func (s *Server) Started() {
	for _, f := range s.plugins.onStart {
		f()
	}
}

// projectOpened calls the hooks of the plugins enabled on the project of rt.
func (s *Server) projectOpened(rt *runtime.Runtime) {
	for _, hk := range s.plugins.onOpen {
		if s.pluginOn(rt, hk.plugin) {
			hk.f(rt)
		}
	}
}

// windowsChanged tells the plugins how many windows the pod has. Called with s.mu held, so
// that the counts arrive in order.
func (s *Server) windowsChanged() {
	for _, f := range s.plugins.onWindows {
		f(len(s.clients))
	}
}

func (p podLend) EmitFamily(root, event string, data any, except string) {
	p.s.mu.Lock()
	var targets []*Client
	for c := range p.s.clients {
		if c.project == "" || c.id == except {
			continue
		}
		if c.project == root {
			targets = append(targets, c)
		} else if pr, ok := p.s.Projects.Get(c.project); ok && pr.Parent == root {
			targets = append(targets, c)
		}
	}
	p.s.mu.Unlock()
	for _, c := range targets {
		c.push(event, data)
	}
}

func (p podLend) CloseRuntime(project string) { p.s.closeRuntime(project) }

func (p podLend) CapabilityOn(rt *runtime.Runtime, c string) bool { return p.s.capabilityOn(rt, c) }

func (p podLend) Capability(rt *runtime.Runtime, c string) any {
	if !p.s.capabilityOn(rt, c) {
		return nil
	}
	return p.s.plugins.provided[c]
}

func (p podLend) Stopping() bool { return p.s.stopped.Load() }

func (p podLend) Plugin(id string) (plugins.Plugin, bool) { return p.s.Plugins.Get(id) }

func (p podLend) UICall(ctx context.Context, project, chat, tool string, args any, wait bool, timeout time.Duration) (plugins.UIResult, bool, bool) {
	return p.s.uiCallIn(ctx, p.s.uiClientFor(project, chat), project, chat, tool, args, wait, timeout)
}

func (p podLend) AssistantTools(rt *runtime.Runtime) []json.RawMessage { return p.s.pluginDefs(rt) }

func (p podLend) RunAssistantTool(ctx context.Context, rt *runtime.Runtime, name string, args json.RawMessage, lang string, opts assistant.Options) (assistant.ToolResult, bool, error) {
	t := p.s.findPluginTool(name)
	if t == nil {
		return assistant.ToolResult{}, false, nil
	}
	res, err := p.s.runPluginTool(ctx, rt, t, args, lang, pluginOptions(opts, t.plugin))
	return res, true, err
}

func (p podLend) Notify(n plugins.Note)                  { go p.s.notify(n) }
func (p podLend) QuestionsChanged(project, asked string) { p.s.questionsChanged(project, asked) }
func (p podLend) CommandsText(rt *runtime.Runtime) (string, int, error) {
	return p.s.commandsText(rt)
}
func (p podLend) StartSaved(rt *runtime.Runtime, id string, restart bool) (commands.Command, string, string, error) {
	return p.s.startSaved(rt, id, restart)
}

func (p podLend) Services() *plugins.Services {
	s := p.s
	return &plugins.Services{Store: s.Store, Projects: s.Projects, Cfg: s.Cfg, Keeper: s.Keeper, Desks: s.Desks,
		Token: s.Token, MCPBase: s.MCPBase, Version: s.Version, Commands: s.projectCommands, Models: s.Models}
}
