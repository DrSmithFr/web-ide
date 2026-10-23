package server

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"

	"github.com/DrSmithFr/web-ide/pod/plugins"
	"github.com/DrSmithFr/web-ide/pod/projects"
	"github.com/DrSmithFr/web-ide/pod/runtime"
	"github.com/DrSmithFr/web-ide/pod/sshx"
)

// Tools of the MCP endpoint of the core: the saved commands here, the IDE in mcp_ide.go and
// ask_user in mcp_ask.go. The tools of the plugins (the assistant tools, Host.AssistantTool,
// and the MCP-only ones, Host.MCPTool: the kanban_* tools) are listed after them. Texts read
// by the model are English.

type mcpTool struct {
	name, description string
	props             map[string]any
	required          []string
	run               func(ctx context.Context, s *Server, args json.RawMessage) (string, error)
	// rich answers with content blocks (an image…) instead of a text.
	rich func(ctx context.Context, s *Server, args json.RawMessage) ([]any, error)
}

func (t mcpTool) schema() map[string]any { return plugins.MCPSchema(t.props, t.required) }

// The schemas of the parameters, shared with the tools of the plugins.
var (
	str     = plugins.Str
	strList = plugins.StrList
	enum    = plugins.Enum
)

// mcpScope is the project a call works on, found from the working directory of the client.
type mcpScope struct {
	here   *projects.Project // project of the working directory (a worktree has its own)
	root   *projects.Project
	ticket int64 // ticket of the worktree the client works in, 0 elsewhere
}

func (s *Server) mcpScope(cwd string) (mcpScope, error) {
	if !filepath.IsAbs(cwd) {
		return mcpScope{}, fmt.Errorf("cwd must be an absolute path, got %q", cwd)
	}
	p, ok := s.Projects.At(cwd)
	if !ok {
		return mcpScope{}, fmt.Errorf("no project of the IDE holds %s: open the folder as a project in the IDE first", cwd)
	}
	root, err := s.Projects.Root(p.ID)
	if err != nil {
		return mcpScope{}, err
	}
	return mcpScope{here: p, root: root, ticket: p.Ticket}, nil
}

var mcpTools = []mcpTool{
	{
		name:        "list_commands",
		description: "Lists the saved commands of the project of your folder (Console ▶ menu of the IDE, written by the user): id, flag (serve, build, test, lint, setup, other), name, description, folder, port, whether it runs, and its bash.",
		run: func(ctx context.Context, s *Server, raw json.RawMessage) (string, error) {
			_, rt, err := s.mcpRuntime(raw)
			if err != nil {
				return "", err
			}
			text, _, err := s.commandsText(rt)
			return text, err
		},
	},
	{
		name:        "run_saved_command",
		description: "Runs a saved command of the project (see list_commands) in its console of the IDE, in the worktree of your folder, visible to the user: one console per command, so a command still running is not started again (you get its console and output) unless restart is true. Waits for its end or the timeout, then returns its output; it keeps running afterwards (a development server…).",
		props: map[string]any{"id": str("Id of the command (list_commands)"), "restart": map[string]any{"type": "boolean", "description": "Restart the command when it is running already (false by default)"},
			"timeout": map[string]any{"type": "integer", "description": "Seconds to wait before returning (20 by default, 600 max)"}},
		required: []string{"id"},
		run: func(ctx context.Context, s *Server, raw json.RawMessage) (string, error) {
			a, rt, err := s.mcpRuntime(raw)
			if err != nil {
				return "", err
			}
			return s.mcpRunSaved(ctx, rt, a.ID, a.Restart, a.Timeout)
		},
	},
}

// mcpArgs are the arguments of the tools on the saved commands.
type mcpArgs struct {
	Cwd     string `json:"cwd"`
	ID      string `json:"id"`
	Restart bool   `json:"restart"`
	Timeout int    `json:"timeout"`
}

// mcpRuntime opens the runtime of the project of the folder of the client (a worktree has
// its own).
func (s *Server) mcpRuntime(raw json.RawMessage) (mcpArgs, *runtime.Runtime, error) {
	a, err := bind[mcpArgs](raw)
	if err != nil {
		return a, nil, err
	}
	sc, err := s.mcpScope(a.Cwd)
	if err != nil {
		return a, nil, err
	}
	rt, err := s.openRuntime(sc.here.ID, sshx.Creds{})
	return a, rt, err
}

func findMCPTool(name string) *mcpTool {
	for i := range mcpTools {
		if mcpTools[i].name == name {
			return &mcpTools[i]
		}
	}
	return nil
}
