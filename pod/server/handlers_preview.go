package server

import (
	"fmt"
	"strings"

	"github.com/DrSmithFr/web-ide/pod/assistant"
	"github.com/DrSmithFr/web-ide/pod/commands"
	"github.com/DrSmithFr/web-ide/pod/runtime"
)

// sharePreviewIn resolves the app of share_preview in project (the assistant and the MCP).
func (s *Server) sharePreviewIn(project string, rt *runtime.Runtime, a assistant.Args) (assistant.ToolResult, error) {
	pv := &assistant.Preview{Project: project, Title: strings.TrimSpace(a.Str("title")), Command: strings.TrimSpace(a.Str("command")),
		Cwd: a.Str("cwd"), Port: a.Num("port"), CommandID: strings.TrimSpace(a.Str("command_id"))}
	list, err := s.projectCommands(rt)
	if err != nil && (pv.CommandID != "" || pv.Command == "") {
		return assistant.ToolResult{}, err
	}
	var saved *commands.Command
	switch {
	case pv.CommandID != "":
		cmd, ok := commands.Find(list, pv.CommandID)
		if !ok {
			return assistant.ToolResult{}, assistant.Usagef("no saved command %q (see list_commands)", pv.CommandID)
		}
		saved = &cmd
	case pv.Command == "":
		var serve []string
		for _, c := range commands.OfFlag(list, "serve") {
			if c.Port > 0 || pv.Port > 0 {
				serve = append(serve, fmt.Sprintf("%s (%s)", c.ID, c.Name))
				if saved == nil {
					saved = &c
				}
			}
		}
		if len(serve) == 0 {
			return assistant.ToolResult{}, assistant.Usagef("command is missing (the project has no saved serve command with a port)")
		}
		if len(serve) > 1 {
			return assistant.ToolResult{}, assistant.Usagef("several saved serve commands: pass command_id, one of %s", strings.Join(serve, ", "))
		}
	default:
		for _, c := range commands.OfFlag(list, "serve") {
			if strings.TrimSpace(c.Command) == pv.Command && (pv.Port == 0 || c.Port == 0 || pv.Port == c.Port) && saved == nil {
				saved = &c
			}
		}
	}
	if saved != nil {
		pv.CommandID, pv.Command, pv.Cwd = saved.ID, saved.Command, saved.Cwd
		if pv.Port == 0 {
			pv.Port = saved.Port
		}
		if pv.Title == "" {
			pv.Title = saved.Name
		}
	}
	if pv.Command == "" {
		return assistant.ToolResult{}, assistant.Usagef("command is missing")
	}
	if pv.Port < 1 || pv.Port > 65535 {
		return assistant.ToolResult{}, assistant.Usagef("port must go from 1 to 65535")
	}
	if pv.Cwd != "" {
		if pv.Cwd = assistant.RelPath(rt.Root, assistant.AbsPath(rt.Root, pv.Cwd)); pv.Cwd == "." {
			pv.Cwd = ""
		}
	}
	if pv.Title == "" {
		pv.Title = pv.Command
	}
	what := "the command"
	sum := pv.Command
	if saved != nil {
		what = fmt.Sprintf("the saved command %q (%s)", saved.Name, saved.ID)
		sum = saved.Name
	}
	res := assistant.OK(fmt.Sprintf("A card is shown to the user: %s starts when they click it (port %d), and the app opens on a temporary URL. Do not run the command yourself.", what, pv.Port),
		assistant.T("{command} → port {port}", map[string]any{"command": sum, "port": pv.Port}))
	res.Preview = pv
	return res, nil
}
