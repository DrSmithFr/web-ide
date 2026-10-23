package plugins

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/DrSmithFr/web-ide/pod/assistant"
	"github.com/DrSmithFr/web-ide/pod/hfcache"

	"github.com/DrSmithFr/web-ide/pod/commands"
	"github.com/DrSmithFr/web-ide/pod/config"
	"github.com/DrSmithFr/web-ide/pod/desks"
	"github.com/DrSmithFr/web-ide/pod/keeper"
	"github.com/DrSmithFr/web-ide/pod/projects"
	"github.com/DrSmithFr/web-ide/pod/runtime"
	"github.com/DrSmithFr/web-ide/pod/settingsync"
	"github.com/DrSmithFr/web-ide/pod/store"
)

// Plugin is a built-in plugin: its manifest, and what it registers in the pod.
type Plugin interface {
	Manifest() Manifest
	Register(h Host)
}

// Host is what the pod gives a plugin at registration.
type Host interface {
	// Handle adds the RPC method <plugin id>.<method>, or the method as named when it has a
	// prefix of the plugin's namespaces (console.create).
	Handle(method string, h Handler)
	// AssistantTool adds a tool offered to the assistant and the MCP; its name must be listed
	// in the manifest.
	AssistantTool(t AssistantTool)
	// MCPTool adds a tool offered to the MCP only; its name must be listed in the manifest.
	MCPTool(t MCPTool)
	// HandleGlobal adds a method without a project (Call.Runtime nil: the home page), always
	// answered: the switch of a project hides the plugin there, not the state of the pod.
	HandleGlobal(method string, h Handler)
	// Pod is what the pod lends its built-in plugins beyond one project.
	Pod() Pod
	// OnProjectOpen is called when a window opens a project where the plugin is enabled.
	OnProjectOpen(f func(rt *runtime.Runtime))
	// OnWindows is called with the number of windows of the pod each time it changes, in order;
	// the pod is locked meanwhile: f must not call it back (it arms a timer, say).
	OnWindows(f func(n int))
	// OnStart is called once the pod is ready (the conversations that ran are resumed), OnStop
	// when it stops (they are saved for the next pod), the last registered first: a plugin
	// stops before the ones it uses (AI before Kanban, whose bases it writes).
	OnStart(f func())
	OnStop(f func())
	// Provide gives a capability of the manifest's provides to the others and the core: an
	// interface of pod/caps.
	Provide(capability string, impl any)
	// HandlePreview serves the previews the page asks through the tunnel of the relay (the
	// Previews plugin): f is the handler of a preview id, false when there is none.
	HandlePreview(f func(id string) (http.Handler, bool))
	// Questions adds a source of the Questions tool (the sessions of the agents, the
	// conversations of the assistant); Pod.QuestionsChanged tells the windows to ask again.
	Questions(q Questions)
	// Vault adds a map of the synced vault (the API keys of the model servers): its entries go
	// to the other pods of the account when the vault is on (docs/design/relay.md).
	Vault(m settingsync.VaultMap)
}

// Pod is the pod as a built-in plugin sees it: the projects, the events to every window, and
// its pod-wide services.
type Pod interface {
	// Runtime opens the runtime of a project (a preview of another project of the pod).
	Runtime(project string) (*runtime.Runtime, error)
	// OpenRuntime is the runtime of a project if a window has it open.
	OpenRuntime(project string) *runtime.Runtime
	// Project is the name of a project and its SSH host.
	Project(id string) (name, host string, ok bool)
	// Broadcast sends an event to every window of the pod; Emit to the windows of a project.
	Broadcast(event string, data any)
	Emit(project, event string, data any)
	// EmitFamily sends an event to the windows of a main project and of its worktrees, but the
	// window except ("" for none).
	EmitFamily(root, event string, data any, except string)
	// CloseRuntime closes the runtime of a project (a worktree removed).
	CloseRuntime(project string)
	// Services are the stores and managers of the pod (the conversations, the desks…).
	Services() *Services
	// CapabilityOn tells whether a project has a capability (worktrees, terminal…).
	CapabilityOn(rt *runtime.Runtime, capability string) bool
	// Capability is what provides a capability on the project of rt (an interface of
	// pod/caps), nil when no enabled plugin provides it.
	Capability(rt *runtime.Runtime, capability string) any
	// Stopping tells that the pod is stopping (the work running is left to the next pod).
	Stopping() bool
	// Plugin is a plugin of the pod by id, to use its exports (the AI uses the Kanban's).
	Plugin(id string) (Plugin, bool)
	// UICall asks a window of project to run a tool of the interface (open_file, focus…): the
	// window showing the conversation chat first. With wait, it waits for its answer: window
	// tells whether a window was there, answered whether it answered in time.
	UICall(ctx context.Context, project, chat, tool string, args any, wait bool, timeout time.Duration) (res UIResult, window, answered bool)
	// AssistantTools are the definitions of the assistant tools of the plugins enabled on the
	// project of rt; RunAssistantTool runs one (found: a plugin has it).
	AssistantTools(rt *runtime.Runtime) []json.RawMessage
	RunAssistantTool(ctx context.Context, rt *runtime.Runtime, name string, args json.RawMessage, lang string, opts assistant.Options) (res assistant.ToolResult, found bool, err error)
	// Notify sends a push notification to the devices that want its kind and show no window
	// (a conversation that ended, a test feedback added to a ticket).
	Notify(n Note)
	// QuestionsChanged tells the windows of the root of project that its questions changed;
	// asked names who just asked one (a push notification), "" for none.
	QuestionsChanged(project, asked string)
	// The saved commands of a project, as the tools read them, and one started (a console).
	CommandsText(rt *runtime.Runtime) (text string, n int, err error)
	StartSaved(rt *runtime.Runtime, id string, restart bool) (cmd commands.Command, console, running string, err error)
	// Authorized tells whether an HTTP request carries the token of the pod (a private preview).
	Authorized(r *http.Request) bool
}

// UIResult is the answer of a window to a tool of the interface.
type UIResult struct {
	Content string          `json:"content"`
	Summary json.RawMessage `json:"summary"`
	Status  string          `json:"status"`
	// A page drawn by board_draw.
	Page *assistant.Page `json:"page,omitempty"`
	// Image drawn by the user for request_doodle of the MCP (a data URL of a PNG).
	Image string `json:"image,omitempty"`
}

// Services are the stores and managers of the pod lent to the built-in plugins, with the
// address the agents reach the pod at.
type Services struct {
	Store    *store.Store
	Projects *projects.Registry
	Cfg      *config.Config
	Keeper   *keeper.Client
	Desks    *desks.Store
	// Token of the pod and MCPBase, the address of its MCP endpoint for the agents (else the
	// listen address of Cfg); Version of the pod, told to the agents.
	Token   string
	MCPBase string
	Version string
	// Commands are the saved commands of the project of rt.
	Commands func(rt *runtime.Runtime) ([]commands.Command, error)
	// Models caches the speech recognition models downloaded for the page.
	Models *hfcache.Cache
}

// Note is a push notification (docs/design/relay.md): its kind (a switch of the devices,
// pod/push), the path of the app it opens, its tag (a newer one with the same tag replaces
// it), its urgency (normal, high) and its texts in a language.
type Note struct {
	Kind, Path, Tag, Urgency string
	Text                     func(lang string) (title, body string)
}

// Call is one call of a plugin: the project it runs in (local or SSH, through its runtime),
// its parameters and the language of the user.
type Call struct {
	Runtime *runtime.Runtime
	Params  json.RawMessage
	Lang    string
	// Window is the window of an RPC call (nil for an assistant tool).
	Window Window
	// Emit sends an event to every window of the project (nil for an assistant tool);
	// EmitOthers to every one but the window that called; EmitFamily to the windows of the
	// main project and of its worktrees.
	Emit       func(event string, data any)
	EmitOthers func(event string, data any)
	EmitFamily func(event string, data any)
	// Options are the plugin's options of the assistant's conversation (nil from the MCP): what
	// the page module gave for it (assistantOptions), e.g. the Compose profiles active.
	Options json.RawMessage
}

// Window is the window that called a method: the events pushed to it, and the streams it
// holds (a log followed) until it stops them or leaves.
type Window interface {
	Push(event string, data any)
	// Done is closed when the window leaves.
	Done() <-chan struct{}
	// Hold keeps a stream of the window under an id; Release stops and forgets it.
	Hold(id string, stop func())
	Release(id string)
	// Project is the project the window shows ("" on the home page); ID tells windows apart.
	Project() string
	ID() string
	// Watch records the conversation of the assistant the window shows (the tools of the
	// interface run in that window first).
	Watch(chat string)
}

// Handler answers an RPC method of a plugin.
type Handler func(ctx context.Context, c Call) (any, error)

// AssistantTool is a tool a plugin offers to the assistant, Claude Code (/mcp) and the ACP
// agents, from one declaration.
type AssistantTool struct {
	Name        string
	Description string
	// Props are the JSON schemas of the parameters; Required their required names.
	Props    map[string]any
	Required []string
	Run      func(ctx context.Context, c Call) (Result, error)
}

// UsageError tells that the call itself is wrong (a parameter missing or invalid), as the
// statistics of the assistant count it.
type UsageError struct{ Msg string }

func (e UsageError) Error() string { return e.Msg }

// CodedError is an error the page tells apart by its code (db_password: ask the password and
// try again), with data for it; its message is Err, translated.
type CodedError struct {
	Code string
	Data map[string]string
	Err  error
}

func (e *CodedError) Error() string { return e.Err.Error() }
func (e *CodedError) Unwrap() error { return e.Err }

// Result of an assistant tool: the text read by the model, the summary shown to the user.
// An error status tells the model the call failed.
type Result struct {
	Content string
	// Summary is assistant.T(…).Raw() to translate, or assistant.Plain(…).
	Summary json.RawMessage
	Error   bool
}
