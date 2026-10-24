package ai

import (
	"encoding/json"

	"antennae.space/ide/plugins/ai/engine"
	"antennae.space/ide/pod/assistant"
	"antennae.space/ide/pod/runtime"
)

// Saved commands of the project for the assistant: list_commands and run_saved_command. The
// list and the start are the pod's (shared with the MCP); the wait is the conversation's.

func (s *A) listCommandsTool(rt *runtime.Runtime) (assistant.ToolResult, error) {
	text, n, err := s.pod.CommandsText(rt)
	if err != nil {
		return assistant.ToolResult{}, err
	}
	return assistant.OK(text, assistant.Tn(n, "{n} command", "{n} commands", nil)), nil
}

func (s *A) runSavedTool(r *assistantRun, rt *runtime.Runtime, a assistant.Args, progress func(string)) (assistant.ToolResult, error) {
	cmd, console, running, err := s.pod.StartSaved(rt, a.Str("id"), a.Bool("restart", false))
	if err != nil {
		return assistant.ToolResult{}, err
	}
	if running != "" {
		return assistant.OK(running, assistant.T("{command}: already running", map[string]any{"command": cmd.Name})), nil
	}
	go s.uiTool(r, "focus", assistant.Args{"target": json.RawMessage(`"console"`), "console_id": jsonString(console), "quiet": json.RawMessage("true")})
	until := assistant.CommandWait(a.Num("timeout"))
	if rt.Keeper != nil {
		s.toolRunning(r, func(run *engine.Running) { run.Console, run.Until = console, until.UnixMilli() })
	}
	return s.waitCommand(r, rt, cmd.Name, console, until, progress)
}
