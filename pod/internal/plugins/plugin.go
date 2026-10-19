package plugins

import (
	"context"
	"encoding/json"

	"github.com/DrSmithFr/web-ide/pod/internal/runtime"
)

// Plugin is a built-in plugin: its manifest, and what it registers in the pod.
type Plugin interface {
	Manifest() Manifest
	Register(h Host)
}

// Host is what the pod gives a plugin at registration.
type Host interface {
	// Handle adds the RPC method <plugin id>.<method>.
	Handle(method string, h Handler)
	// AssistantTool adds a tool offered to the assistant and the MCP; its name must be listed
	// in the manifest.
	AssistantTool(t AssistantTool)
}

// Call is one call of a plugin: the project it runs in (local or SSH, through its runtime),
// its parameters and the language of the user.
type Call struct {
	Runtime *runtime.Runtime
	Params  json.RawMessage
	Lang    string
	// Window is the window of an RPC call (nil for an assistant tool).
	Window Window
	// Emit sends an event to every window of the project (nil for an assistant tool).
	Emit func(event string, data any)
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
