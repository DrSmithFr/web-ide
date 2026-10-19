package server

import (
	"context"
	"fmt"
	"strings"

	"github.com/DrSmithFr/web-ide/pod/internal/assistant"
	"github.com/DrSmithFr/web-ide/pod/internal/commands"
	"github.com/DrSmithFr/web-ide/pod/internal/runtime"
)

// Saved commands of the project for the models: list_commands and run_saved_command of the MCP
// endpoint (Claude Code), and the list and the start lent to the assistant (the AI plugin). One
// console per command, as in the page.

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

// startSaved runs a saved command for a model: the console started, or the text answered
// when it runs already (restart not asked).
func (s *Server) startSaved(rt *runtime.Runtime, id string, restart bool) (commands.Command, string, string, error) {
	if strings.TrimSpace(id) == "" {
		return commands.Command{}, "", "", assistant.Usagef("id is missing (see list_commands)")
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
		text, _, _ := assistant.ConsoleText(rt, r.Running.ID)
		out := assistant.Tail(text, 40, 4000)
		if out == "" {
			out = "(no output yet)"
		}
		return cmd, "", fmt.Sprintf("“%s” is already running in console %s (one console per command): nothing started. End of its output:\n%s\n\nPass restart=true to restart it, or follow it with read_console.", cmd.Name, r.Running.ID, out), nil
	}
	return cmd, r.Console.ID, "", nil
}

// mcpRunSaved is run_saved_command of the MCP endpoint, in the project of its folder.
func (s *Server) mcpRunSaved(ctx context.Context, rt *runtime.Runtime, id string, restart bool, timeout int) (string, error) {
	cmd, console, running, err := s.startSaved(rt, id, restart)
	if err != nil || running != "" {
		return running, err
	}
	text, now, err := assistant.FollowConsole(ctx, rt, console, assistant.CommandWait(timeout), nil)
	if err != nil {
		return "", err
	}
	return assistant.ConsoleResult(cmd.Name, console, text, now).Content, nil
}
