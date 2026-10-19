package plugins

import (
	"context"
	"encoding/json"
	"time"

	"github.com/DrSmithFr/web-ide/pod/internal/assistant"
	"github.com/DrSmithFr/web-ide/pod/internal/hfcache"

	"github.com/DrSmithFr/web-ide/pod/internal/acp"
	"github.com/DrSmithFr/web-ide/pod/internal/commands"
	"github.com/DrSmithFr/web-ide/pod/internal/config"
	"github.com/DrSmithFr/web-ide/pod/internal/desks"
	"github.com/DrSmithFr/web-ide/pod/internal/forwards"
	"github.com/DrSmithFr/web-ide/pod/internal/kanban"
	"github.com/DrSmithFr/web-ide/pod/internal/keeper"
	"github.com/DrSmithFr/web-ide/pod/internal/llm"
	"github.com/DrSmithFr/web-ide/pod/internal/preview"
	"github.com/DrSmithFr/web-ide/pod/internal/projects"
	"github.com/DrSmithFr/web-ide/pod/internal/runtime"
	"github.com/DrSmithFr/web-ide/pod/internal/store"
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
	// when it stops (they are saved for the next pod).
	OnStart(f func())
	OnStop(f func())
}

// Pod is the pod as a built-in plugin sees it: the projects, the events to every window, and
// its pod-wide services (the port forwards and the previews, which the HTTP server serves).
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
	Forwards() *forwards.Manager
	Previews() *preview.Manager
	// EmitFamily sends an event to the windows of a main project and of its worktrees, but the
	// window except ("" for none).
	EmitFamily(root, event string, data any, except string)
	// CloseRuntime closes the runtime of a project (a worktree removed).
	CloseRuntime(project string)
	// Services are the stores and managers of the pod (the kanban, the conversations…).
	Services() *Services
	// CapabilityOn tells whether a project has a capability (worktrees, terminal…).
	CapabilityOn(rt *runtime.Runtime, capability string) bool
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
	// Notify sends the push notification of a conversation that ended or asks (asks), and
	// NotifyFeedback the one of a test feedback added to a ticket.
	Notify(asks bool, root, chatID, title string)
	NotifyFeedback(loc kanban.Location, id int64, text string)
	// NoteAsks records the questions a conversation asks from message from (the Questions tool).
	NoteAsks(root string, c *assistant.Chat, from int)
	// The saved commands of a project, as the tools read them, and one started (a console).
	CommandsText(rt *runtime.Runtime) (text string, n int, err error)
	StartSaved(rt *runtime.Runtime, id string, restart bool) (cmd commands.Command, console, running string, err error)
	// SharePreview shows the card of an app preview in the windows of project (share_preview).
	SharePreview(project string, rt *runtime.Runtime, a assistant.Args) (assistant.ToolResult, error)
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
	LLM      *llm.Manager
	Desks    *desks.Store
	Kanban   *kanban.Manager
	// Token of the pod and MCPBase, the address of its MCP endpoint for the agents.
	Token   string
	MCPBase string
	// Commands are the saved commands of the project of rt (the kanban settings migrated).
	Commands func(rt *runtime.Runtime) ([]commands.Command, error)
	// ChatLoc is where the conversations of a project are kept.
	ChatLoc func(project string) (llm.ChatLocation, string, error)
	// AgentsMCP is the MCP server of the IDE as the sessions of the agents reach it.
	AgentsMCP func() []acp.MCPServer
	// Models caches the speech recognition models downloaded for the page.
	Models *hfcache.Cache
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
