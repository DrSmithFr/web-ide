package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/DrSmithFr/web-ide/pod/assistant"
	"github.com/DrSmithFr/web-ide/pod/plugins"
	"strings"
	"time"

	"github.com/DrSmithFr/web-ide/pod/runtime"
	"github.com/DrSmithFr/web-ide/pod/sshx"
)

// Tools of the MCP endpoint on the IDE itself, moved from the assistant so that the CLI
// agents get them (#68): consoles, previews, and what shows in a window (files, diffs,
// messages, doodles); the tools of the plugins (Docker) are added by handlers_plugins.go. Each
// one reuses the code of the tool of the assistant and keeps its rule: reading and typing in a
// console run freely there too, and nothing of share_preview runs before the user clicks.

// mcpIDE binds the arguments of a tool of the IDE and opens the runtime of the folder of the
// client (a worktree has its own).
func (s *Server) mcpIDE(raw json.RawMessage) (assistant.Args, mcpScope, *runtime.Runtime, error) {
	var a assistant.Args
	if err := json.Unmarshal(raw, &a); err != nil {
		return nil, mcpScope{}, nil, err
	}
	sc, err := s.mcpScope(a.Str("cwd"))
	if err != nil {
		return nil, sc, nil, err
	}
	rt, err := s.openRuntime(sc.here.ID, sshx.Creds{})
	return a, sc, rt, err
}

// mcpText is the text of a result of a tool of the assistant, its failure as an error.
func mcpText(res assistant.ToolResult, err error) (string, error) {
	if err != nil {
		return "", err
	}
	if res.Status == "error" {
		return "", errors.New(strings.TrimPrefix(res.Content, "Error: "))
	}
	return res.Content, nil
}

// ideTool is a tool of the IDE answered with text.
func ideTool(f func(ctx context.Context, s *Server, a assistant.Args, sc mcpScope, rt *runtime.Runtime) (assistant.ToolResult, error)) func(context.Context, *Server, json.RawMessage) (string, error) {
	return func(ctx context.Context, s *Server, raw json.RawMessage) (string, error) {
		a, sc, rt, err := s.mcpIDE(raw)
		if err != nil {
			return "", err
		}
		return mcpText(f(ctx, s, a, sc, rt))
	}
}

func strArray(a assistant.Args, k string) []string {
	var out []string
	_ = json.Unmarshal(a[k], &out)
	return out
}

// mcpWindow is the window that shows what a tool of the MCP asks: one of the folder of the
// client, else one of its main project (the page attaches the worktree).
func (s *Server) mcpWindow(sc mcpScope) (*Client, error) {
	if c := s.uiClientFor(sc.here.ID, ""); c != nil {
		return c, nil
	}
	if c := s.uiClientFor(sc.root.ID, ""); c != nil {
		return c, nil
	}
	return nil, fmt.Errorf("no window of the IDE is open on this project: the user cannot see it")
}

// mcpUI runs a tool in a window of the project and waits for its result.
func (s *Server) mcpUI(ctx context.Context, sc mcpScope, name string, args any, timeout time.Duration) (plugins.UIResult, error) {
	c, err := s.mcpWindow(sc)
	if err != nil {
		return plugins.UIResult{}, err
	}
	res, _, answered := s.uiCallIn(ctx, c, sc.here.ID, "", name, args, true, timeout)
	if !answered {
		return res, fmt.Errorf("no window of the IDE answered: the user may not see it")
	}
	if res.Status == "error" {
		return res, errors.New(strings.TrimPrefix(res.Content, "Error: "))
	}
	return res, nil
}

// uiTextTool is a tool run by a window, answered with the text of the window.
func uiTextTool(name string, args func(a assistant.Args) any) func(context.Context, *Server, json.RawMessage) (string, error) {
	return func(ctx context.Context, s *Server, raw json.RawMessage) (string, error) {
		a, sc, _, err := s.mcpIDE(raw)
		if err != nil {
			return "", err
		}
		res, err := s.mcpUI(ctx, sc, name, args(a), 10*time.Second)
		return res.Content, err
	}
}

var mcpIDETools = []mcpTool{
	{
		name:        "list_consoles",
		description: "Lists the consoles of the IDE (terminals and commands) of the project of your folder, with their state.",
		run: ideTool(func(ctx context.Context, s *Server, a assistant.Args, sc mcpScope, rt *runtime.Runtime) (assistant.ToolResult, error) {
			return assistant.ListConsoles(rt), nil
		}),
	},
	{
		name:        "read_console",
		description: "Reads the end of the output of a console of the IDE (see list_consoles): a development server, a watcher, a terminal of the user.",
		props:       map[string]any{"console_id": str("Console id"), "lines": map[string]any{"type": "integer", "description": "Number of lines (200 by default)"}},
		required:    []string{"console_id"},
		run: ideTool(func(ctx context.Context, s *Server, a assistant.Args, sc mcpScope, rt *runtime.Runtime) (assistant.ToolResult, error) {
			return assistant.ReadConsole(rt, a.Str("console_id"), a.Num("lines"))
		}),
	},
	{
		name:        "console_input",
		description: "Types text in a console of the IDE (an interactive terminal, a program waiting for an answer). A newline is added unless enter is false. Returns the end of its output.",
		props:       map[string]any{"console_id": str("Console id"), "text": str("Text to type"), "enter": map[string]any{"type": "boolean", "description": "Press Enter after the text (true by default)"}},
		required:    []string{"console_id", "text"},
		run: ideTool(func(ctx context.Context, s *Server, a assistant.Args, sc mcpScope, rt *runtime.Runtime) (assistant.ToolResult, error) {
			return assistant.ConsoleInput(ctx, rt, a.Str("console_id"), a.Str("text"), a.Bool("enter", true))
		}),
	},
	{
		name: "share_preview",
		description: "Offers the user to try the app: a notification in the IDE that, on their click, starts the command in a console (unless it runs already), waits for the port, then opens the app on a temporary URL they can reach from any device (phone included). Nothing runs before the click: do not run the command yourself. " +
			"When the project has a saved serve command (list_commands), pass its command_id, or no command at all when there is only one with a port.",
		props: map[string]any{
			"title":      str(`Short name of what to try, e.g. "Login page"`),
			"command_id": str("Id of a saved serve command of the project (list_commands): it gives the command, the folder and the port"),
			"command":    str(`Command that starts the app (development server), e.g. "npm run dev"; omit it to use the saved serve command`),
			"port":       map[string]any{"type": "integer", "description": "Port the app listens on (default: the one of the saved command)"},
			"folder":     str("Folder to run it in, relative to the project root (default: the root)"),
		},
		required: []string{"title"},
		run: func(ctx context.Context, s *Server, raw json.RawMessage) (string, error) {
			a, sc, rt, err := s.mcpIDE(raw)
			if err != nil {
				return "", err
			}
			// cwd is the folder of the client here: the folder of the app is folder.
			a["cwd"] = a["folder"]
			res, err := s.sharePreviewIn(sc.here.ID, rt, a)
			if err != nil {
				return "", err
			}
			c, err := s.mcpWindow(sc)
			if err != nil {
				return "", err
			}
			s.uiCallIn(ctx, c, sc.here.ID, "", "share_preview", map[string]any{"spec": res.Preview}, false, 0)
			return fmt.Sprintf("The user is offered to try %q in the IDE: the app starts when they click it (port %d), and opens on a temporary URL. Do not run the command yourself; list_previews gives its URL once it runs.", res.Preview.Title, res.Preview.Port), nil
		},
	},
	{
		name:        "list_previews",
		description: "Lists the app previews running for the project (started from share_preview or by the user): title, command, port, URL, and the public URL when the user shared it.",
		run: func(ctx context.Context, s *Server, raw json.RawMessage) (string, error) {
			_, sc, _, err := s.mcpIDE(raw)
			if err != nil {
				return "", err
			}
			var rows []string
			for _, p := range s.Previews.List() {
				if p.Project != sc.here.ID && p.Project != sc.root.ID {
					continue
				}
				row := fmt.Sprintf("- %s: %s (port %d) → %s", p.Title, p.Command, p.AppPort, p.URL)
				if p.PublicURL != "" {
					row += ", public: " + p.PublicURL
				}
				rows = append(rows, row)
			}
			if len(rows) == 0 {
				return "No preview runs for this project.", nil
			}
			return strings.Join(rows, "\n"), nil
		},
	},
	{
		name:        "open_file",
		description: "Opens a file in the editor of the IDE to show it to the user, optionally selecting lines.",
		props: map[string]any{"path": str("File: absolute, or relative to your folder"), "line": map[string]any{"type": "integer", "description": "First line to show (starting at 1)"},
			"end_line": map[string]any{"type": "integer", "description": "Last line to select"}},
		required: []string{"path"},
		run: uiTextTool("open_file", func(a assistant.Args) any {
			return map[string]any{"path": a.Str("path"), "line": a.Num("line"), "end_line": a.Num("end_line")}
		}),
	},
	{
		name:        "open_diff",
		description: "Opens the uncommitted changes of a file (against git HEAD) in the diff view of the IDE, to show the user what changed.",
		props:       map[string]any{"path": str("File: absolute, or relative to your folder")},
		required:    []string{"path"},
		run: uiTextTool("open_diff", func(a assistant.Args) any {
			return map[string]any{"path": a.Str("path")}
		}),
	},
	{
		name:        "focus",
		description: "Brings an element of the IDE to the front: an open file, a panel (explorer, search, git, kanban, console, problems, docker, database, assistant, agents, structure, conflicts, info), a console or the problems list.",
		props: map[string]any{"target": enum("Kind of element", "file", "panel", "console", "problems"), "path": str("File (target=file)"),
			"panel": str("Panel (target=panel)"), "console_id": str("Console id (target=console), see list_consoles")},
		required: []string{"target"},
		run: uiTextTool("focus", func(a assistant.Args) any {
			return map[string]any{"target": a.Str("target"), "path": a.Str("path"), "panel": a.Str("panel"), "console_id": a.Str("console_id")}
		}),
	},
	{
		name:        "show_message",
		description: "Shows a short message to the user in the IDE (a notification of a few seconds): a long task finished, something to look at. Not for questions.",
		props:       map[string]any{"text": str("The message, one or two sentences"), "kind": enum("Look of the message (info by default)", "info", "ok", "warn", "error")},
		required:    []string{"text"},
		run: uiTextTool("show_message", func(a assistant.Args) any {
			return map[string]any{"text": a.Str("text"), "kind": a.Str("kind")}
		}),
	},
	{
		name: "request_doodle",
		description: "Asks the user to draw something (a layout, a diagram, an annotated screenshot) in the doodle editor of the IDE, and returns the drawing as an image with a text description of its elements. " +
			"Waits until they send it or close it (15 minutes at most).",
		props:    map[string]any{"title": str(`What to draw, shown on the editor, e.g. "Layout of the settings page"`)},
		required: []string{"title"},
		rich: func(ctx context.Context, s *Server, raw json.RawMessage) ([]any, error) {
			a, sc, _, err := s.mcpIDE(raw)
			if err != nil {
				return nil, err
			}
			res, err := s.mcpUI(ctx, sc, "request_doodle", map[string]any{"title": a.Str("title")}, 15*time.Minute)
			if err != nil {
				return nil, err
			}
			if res.Status == "cancelled" || res.Image == "" {
				return []any{textContent("The user closed the doodle without sending it.")}, nil
			}
			data, ok := strings.CutPrefix(res.Image, "data:image/png;base64,")
			if !ok {
				return nil, fmt.Errorf("the drawing came back in an unknown format")
			}
			return []any{textContent(res.Content), map[string]any{"type": "image", "mimeType": "image/png", "data": data}}, nil
		},
	},
}

func textContent(text string) map[string]any { return map[string]any{"type": "text", "text": text} }

func init() { mcpTools = append(mcpTools, mcpIDETools...) }
