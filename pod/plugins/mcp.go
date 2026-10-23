package plugins

import (
	"context"
	"encoding/json"
	"net/url"

	"github.com/DrSmithFr/web-ide/pod/projects"
)

// MCPTool is a tool a plugin offers to the MCP clients only (Claude Code, the ACP agents),
// not to the assistant: the kanban_* tools. Every call names the folder of the client (cwd),
// which selects the project; the call is refused where the plugin is disabled.
type MCPTool struct {
	Name        string
	Description string
	// Props are the JSON schemas of the parameters besides cwd; Required their required names.
	Props    map[string]any
	Required []string
	// Needs is a capability the project must also have ("" for none): the conversation tools
	// of the AI read the tickets.
	Needs string
	// Run answers the text read by the model; an error goes back to it as a failed call.
	Run func(ctx context.Context, sc MCPScope, args json.RawMessage) (string, error)
}

// MCPScope is what a call of the MCP works on, found from the folder of the client.
type MCPScope struct {
	// Here is the project of the folder (a worktree has its own), Root its main project.
	Here, Root *projects.Project
	// Ticket is the ticket of the worktree the client works in, 0 elsewhere.
	Ticket int64
	// URL is the address the user opens the IDE at (its relay page, else the local one).
	URL string
	// Lang is the language of the interface (the errors shown in a conversation).
	Lang string
}

// ProjectURL is the page of a project in the IDE, at the address the user opens it.
func (sc MCPScope) ProjectURL(id string) string {
	return sc.URL + "/project/" + url.PathEscape(id)
}

// MCPSchema is the input schema of a tool of the MCP: cwd, then its parameters.
func MCPSchema(props map[string]any, required []string) map[string]any {
	all := map[string]any{"cwd": Str("Your working directory (absolute): it selects the project")}
	for k, v := range props {
		all[k] = v
	}
	return map[string]any{"type": "object", "properties": all, "required": append([]string{"cwd"}, required...)}
}

// Str, StrList and Enum are the JSON schemas of a parameter.
func Str(description string) map[string]any {
	return map[string]any{"type": "string", "description": description}
}

func StrList(description string) map[string]any {
	return map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": description}
}

func Enum(description string, values ...string) map[string]any {
	return map[string]any{"type": "string", "enum": values, "description": description}
}
