package plugins

import (
	"context"
	"encoding/json"

	"github.com/DrSmithFr/web-ide/pod/internal/assistant"
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

// Result of an assistant tool: the text read by the model, the summary shown to the user.
// An error status tells the model the call failed.
type Result struct {
	Content string
	Summary assistant.Text
	Error   bool
}
