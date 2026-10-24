package ai

import (
	"encoding/json"
	"strings"

	"antennae.space/ide/lib/i18n"
	"antennae.space/ide/pod/runtime"
)

// toolNeeds are the tools of the assistant that need a capability of another plugin: the
// consoles and saved commands need terminal (Exec), the previews previews, the lsp_* tools
// language (Languages), search_text search (Search). The server keeps the same check for the tools of its MCP endpoint.
var toolNeeds = map[string]string{
	"run_command": "terminal", "list_consoles": "terminal", "read_console": "terminal", "console_input": "terminal",
	"list_commands": "terminal", "run_saved_command": "terminal",
	"share_preview": "previews", "list_previews": "previews",
	"lsp_symbols": "language", "lsp_workspace_symbols": "language", "lsp_definition": "language", "lsp_references": "language",
	"lsp_hover": "language", "lsp_diagnostics": "language",
	"search_text": "search",
}

// needsOf is the capability a tool needs: toolNeeds, and tickets for the kanban tools.
func needsOf(tool string) string {
	if strings.HasPrefix(tool, "kanban_") {
		return "tickets"
	}
	return toolNeeds[tool]
}

// missingFor is the error of a tool whose capability the project of rt lacks (nil: it runs).
func (s *A) missingFor(rt *runtime.Runtime, tool string) error {
	if c := needsOf(tool); c != "" && !s.pod.CapabilityOn(rt, c) {
		return i18n.Errorf("%s needs the %s capability, off on this project (Settings > Plugins)", tool, c)
	}
	return nil
}

// offered drops the definitions of the tools whose capability the project of rt lacks.
func (s *A) offered(rt *runtime.Runtime, defs []json.RawMessage) []json.RawMessage {
	out := defs[:0:0]
	for _, d := range defs {
		var f struct {
			Function struct{ Name string } `json:"function"`
		}
		_ = json.Unmarshal(d, &f)
		if s.missingFor(rt, f.Function.Name) == nil {
			out = append(out, d)
		}
	}
	return out
}
