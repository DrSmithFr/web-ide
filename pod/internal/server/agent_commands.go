package server

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/DrSmithFr/web-ide/pod/internal/agent"
	"github.com/DrSmithFr/web-ide/pod/internal/commands"
	"github.com/DrSmithFr/web-ide/pod/internal/runtime"
)

// Saved commands of the project for the models: list_commands and run_saved_command, for
// the assistant and the MCP endpoint (Claude Code). One console per command, as in the page.

// commandsText lists the saved commands and their state in rt.
func (s *Server) commandsText(rt *runtime.Runtime) (string, int, error) {
	list, err := s.projectCommands(rt)
	if err != nil {
		return "", 0, err
	}
	if len(list) == 0 {
		return "No saved command: the user writes them in the Console (▶ menu, Edit commands…).", 0, nil
	}
	var b strings.Builder
	for _, c := range list {
		fmt.Fprintf(&b, "%s · %s · %s", c.ID, c.Flag, c.Name)
		if c.Description != "" {
			fmt.Fprintf(&b, " — %s", c.Description)
		}
		if c.Cwd != "" {
			fmt.Fprintf(&b, " · folder %s", c.Cwd)
		}
		if c.Port > 0 {
			fmt.Fprintf(&b, " · port %d", c.Port)
		}
		state := " · not running"
		for _, o := range rt.Consoles.OfCommand(c.ID) {
			if o.Exited {
				state = fmt.Sprintf(" · exited (code %d, console %s)", o.Code, o.ID)
			} else {
				state = " · running (console " + o.ID + ")"
				break
			}
		}
		b.WriteString(state + "\n")
		for _, line := range strings.Split(strings.TrimRight(c.Command, "\n"), "\n") {
			b.WriteString("    " + line + "\n")
		}
	}
	return strings.TrimRight(b.String(), "\n"), len(list), nil
}

func (s *Server) listCommandsTool(rt *runtime.Runtime) (toolResult, error) {
	text, n, err := s.commandsText(rt)
	if err != nil {
		return toolResult{}, err
	}
	return ok(text, agent.Tn(n, "{n} command", "{n} commands", nil)), nil
}

// startSaved runs a saved command for a model: the console started, or the text answered
// when it runs already (restart not asked).
func (s *Server) startSaved(rt *runtime.Runtime, id string, restart bool) (commands.Command, string, string, error) {
	if strings.TrimSpace(id) == "" {
		return commands.Command{}, "", "", usagef("id is missing (see list_commands)")
	}
	cmd, err := s.findCommand(rt, id)
	if err != nil {
		return cmd, "", "", err
	}
	r, err := s.runSaved(rt, cmd, restart, "")
	if err != nil {
		return cmd, "", "", err
	}
	if r.Running != nil {
		text, _, _ := consoleText(rt, r.Running.ID)
		out := agent.Tail(text, 40, 4000)
		if out == "" {
			out = "(no output yet)"
		}
		return cmd, "", fmt.Sprintf("“%s” is already running in console %s (one console per command): nothing started. End of its output:\n%s\n\nPass restart=true to restart it, or follow it with read_console.", cmd.Name, r.Running.ID, out), nil
	}
	return cmd, r.Console.ID, "", nil
}

// commandWait is how long run_saved_command waits (20 s by default, 600 at most).
func commandWait(timeout int) time.Time {
	if timeout <= 0 {
		timeout = 20
	}
	return time.Now().Add(time.Duration(min(timeout, 600)) * time.Second)
}

func (s *Server) runSavedTool(r *agentRun, rt *runtime.Runtime, a toolArgs, progress func(string)) (toolResult, error) {
	cmd, console, running, err := s.startSaved(rt, a.str("id"), a.boolean("restart", false))
	if err != nil {
		return toolResult{}, err
	}
	if running != "" {
		return ok(running, agent.T("{command}: already running", map[string]any{"command": cmd.Name})), nil
	}
	go s.uiTool(r, "focus", toolArgs{"target": json.RawMessage(`"console"`), "console_id": jsonString(console), "quiet": json.RawMessage("true")})
	until := commandWait(a.num("timeout"))
	if rt.Keeper != nil {
		s.toolRunning(r, func(run *agent.Running) { run.Console, run.Until = console, until.UnixMilli() })
	}
	return s.waitCommand(r, rt, cmd.Name, console, until, progress)
}

// mcpRunSaved is run_saved_command of the MCP endpoint, in the project of its folder.
func (s *Server) mcpRunSaved(ctx context.Context, rt *runtime.Runtime, id string, restart bool, timeout int) (string, error) {
	cmd, console, running, err := s.startSaved(rt, id, restart)
	if err != nil || running != "" {
		return running, err
	}
	text, now, err := followConsole(ctx, rt, console, commandWait(timeout), nil)
	if err != nil {
		return "", err
	}
	return consoleResult(cmd.Name, console, text, now).Content, nil
}
