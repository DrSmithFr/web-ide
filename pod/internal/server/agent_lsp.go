package server

import (
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/DrSmithFr/web-ide/pod/internal/agent"
	"github.com/DrSmithFr/web-ide/pod/internal/lsp"
	"github.com/DrSmithFr/web-ide/pod/internal/runtime"
)

// Language server tools of the agent. A file is opened in its server for the request (the pod
// counts the opens: a file a window already has open stays open, with the unsaved text).

var symbolKinds = map[int]string{
	1: "File", 2: "Module", 3: "Namespace", 4: "Package", 5: "Class", 6: "Method", 7: "Property", 8: "Field", 9: "Constructor", 10: "Enum",
	11: "Interface", 12: "Function", 13: "Variable", 14: "Constant", 15: "String", 16: "Number", 17: "Boolean", 18: "Array", 19: "Object",
	20: "Key", 21: "Null", 22: "Enum member", 23: "Struct", 24: "Event", 25: "Operator", 26: "Type parameter",
}

func kindName(k int) string {
	if n, ok := symbolKinds[k]; ok {
		return n
	}
	return "?"
}

func langOf(rt *runtime.Runtime, abs string) (string, error) {
	lang := lsp.LangOf(abs)
	if lang == "" {
		return "", failf("no language server for %s", relPath(rt.Root, abs))
	}
	return lang, nil
}

// withDoc runs f with the file open in its language server and its current text.
func withDoc[T any](rt *runtime.Runtime, abs string, f func(lang, text string) (T, error)) (T, error) {
	var zero T
	lang, err := langOf(rt, abs)
	if err != nil {
		return zero, err
	}
	fc, err := readText(rt, abs)
	if err != nil {
		return zero, err
	}
	text := fc.Content
	if fc.Buffer != nil {
		text = *fc.Buffer
	}
	uri := lsp.URI(abs)
	open, _ := json.Marshal(map[string]any{"textDocument": map[string]any{"uri": uri, "languageId": lsp.LanguageID(abs), "version": 1, "text": text}})
	if err := rt.LSP.Notify(lang, "textDocument/didOpen", open); err != nil {
		return zero, err
	}
	defer func() {
		closing, _ := json.Marshal(map[string]any{"textDocument": map[string]any{"uri": uri}})
		_ = rt.LSP.Notify(lang, "textDocument/didClose", closing)
	}()
	return f(lang, text)
}

func lspRequest(r *agentRun, rt *runtime.Runtime, lang, method string, params any) (json.RawMessage, error) {
	data, _ := json.Marshal(params)
	return rt.LSP.Request(r.ctx, lang, method, data)
}

type lspRange struct {
	Start struct {
		Line      int `json:"line"`
		Character int `json:"character"`
	} `json:"start"`
}

type docSymbol struct {
	Name           string      `json:"name"`
	Detail         string      `json:"detail"`
	Kind           int         `json:"kind"`
	SelectionRange lspRange    `json:"selectionRange"`
	Children       []docSymbol `json:"children"`
	// SymbolInformation (old servers): flat, with a location.
	ContainerName string `json:"containerName"`
	Location      *struct {
		URI   string   `json:"uri"`
		Range lspRange `json:"range"`
	} `json:"location"`
}

func flattenSymbols(list []docSymbol, depth int, out *[]string) {
	for _, s := range list {
		d := ""
		if s.Detail != "" {
			d = " " + s.Detail
		}
		*out = append(*out, fmt.Sprintf("%s%s %s%s · line %d", strings.Repeat("  ", depth), kindName(s.Kind), s.Name, d, s.SelectionRange.Start.Line+1))
		flattenSymbols(s.Children, depth+1, out)
	}
}

func pathFromURI(u string) string {
	if p, err := url.Parse(u); err == nil && p.Scheme == "file" {
		return p.Path
	}
	return strings.TrimPrefix(u, "file://")
}

// columnOf is the column of a symbol in a line (a whole word first).
func columnOf(line, symbol string) int {
	if symbol == "" {
		return -1
	}
	word := func(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == '$' }
	for from := 0; ; {
		i := strings.Index(line[from:], symbol)
		if i < 0 {
			break
		}
		at := from + i
		before := at == 0 || !word([]rune(line[:at])[len([]rune(line[:at]))-1])
		after := at+len(symbol) >= len(line) || !word([]rune(line[at+len(symbol):])[0])
		if before && after {
			return at
		}
		from = at + 1
	}
	return strings.Index(line, symbol)
}

type location struct {
	path string
	line int
	col  int
}

func toLocations(raw json.RawMessage) []location {
	var list []json.RawMessage
	if json.Unmarshal(raw, &list) != nil {
		if len(raw) == 0 || string(raw) == "null" {
			return nil
		}
		list = []json.RawMessage{raw}
	}
	var out []location
	for _, item := range list {
		var l struct {
			URI                  string    `json:"uri"`
			Range                *lspRange `json:"range"`
			TargetURI            string    `json:"targetUri"`
			TargetSelectionRange *lspRange `json:"targetSelectionRange"`
			TargetRange          *lspRange `json:"targetRange"`
		}
		if json.Unmarshal(item, &l) != nil {
			continue
		}
		switch {
		case l.TargetURI != "":
			rg := l.TargetSelectionRange
			if rg == nil {
				rg = l.TargetRange
			}
			if rg != nil {
				out = append(out, location{pathFromURI(l.TargetURI), rg.Start.Line, rg.Start.Character})
			}
		case l.URI != "" && l.Range != nil:
			out = append(out, location{pathFromURI(l.URI), l.Range.Start.Line, l.Range.Start.Character})
		}
	}
	return out
}

func locationLines(rt *runtime.Runtime, locs []location) []string {
	cache := map[string][]string{}
	var out []string
	for _, l := range locs {
		if _, ok := cache[l.path]; !ok {
			if fc, err := readText(rt, l.path); err == nil {
				cache[l.path] = strings.Split(fc.Content, "\n")
			} else {
				cache[l.path] = nil
			}
		}
		text := ""
		if lines := cache[l.path]; l.line < len(lines) {
			text = strings.TrimSpace(lines[l.line])
			if len(text) > 200 {
				text = text[:200]
			}
		}
		out = append(out, fmt.Sprintf("%s:%d:%d  %s", relPath(rt.Root, l.path), l.line+1, l.col+1, text))
	}
	return out
}

var severities = []string{"", "error", "warning", "info", "hint"}

func formatDiagnostics(rt *runtime.Runtime, abs string, items []lsp.Diagnostic) []string {
	var out []string
	for _, d := range items {
		sev := "error"
		if d.Severity > 0 && d.Severity < len(severities) {
			sev = severities[d.Severity]
		}
		src := ""
		if d.Source != "" {
			src = " (" + d.Source + ")"
		}
		out = append(out, fmt.Sprintf("%s:%d:%d [%s] %s%s", relPath(rt.Root, abs), d.Range.Start.Line+1, d.Range.Start.Character+1, sev, d.Message, src))
	}
	return out
}

func (s *Server) lspTool(r *agentRun, rt *runtime.Runtime, name string, a toolArgs) (toolResult, error) {
	if name == "lsp_diagnostics" {
		return lspDiagnostics(r, rt, a.str("path"))
	}
	if name == "lsp_workspace_symbols" {
		return lspWorkspaceSymbols(r, rt, a.str("query"), a.str("path"))
	}
	p := a.str("path")
	if p == "" {
		return toolResult{}, failf("path is missing")
	}
	abs := absPath(rt.Root, p)
	rel := relPath(rt.Root, abs)
	if name == "lsp_symbols" {
		return withDoc(rt, abs, func(lang, _ string) (toolResult, error) {
			raw, err := lspRequest(r, rt, lang, "textDocument/documentSymbol", map[string]any{"textDocument": map[string]any{"uri": lsp.URI(abs)}})
			if err != nil {
				return toolResult{}, err
			}
			var list []docSymbol
			_ = json.Unmarshal(raw, &list)
			var rows []string
			if len(list) > 0 && list[0].Location != nil {
				for _, sy := range list {
					in := ""
					if sy.ContainerName != "" {
						in = " (in " + sy.ContainerName + ")"
					}
					rows = append(rows, fmt.Sprintf("%s %s%s · line %d", kindName(sy.Kind), sy.Name, in, sy.Location.Range.Start.Line+1))
				}
			} else {
				flattenSymbols(list, 0, &rows)
			}
			text := strings.Join(rows, "\n")
			if text == "" {
				text = "No symbol."
			}
			return ok(text, agent.Tn(len(rows), "{n} symbol", "{n} symbols", nil).With(rel+": ", "")), nil
		})
	}
	line := a.num("line")
	symbol := a.str("symbol")
	if line < 1 {
		return toolResult{}, failf("line is missing (starting at 1)")
	}
	return withDoc(rt, abs, func(lang, text string) (toolResult, error) {
		lines := strings.Split(text, "\n")
		if line > len(lines) {
			return toolResult{}, failf("%s has no line %d", rel, line)
		}
		lineText := lines[line-1]
		col := columnOf(lineText, symbol)
		if col < 0 {
			t := strings.TrimSpace(lineText)
			if len(t) > 200 {
				t = t[:200]
			}
			return toolResult{}, failf("\"%s\" is not on line %d: %s", symbol, line, t)
		}
		// LSP positions count UTF-16 units: convert the byte column.
		units := len(utf16Of(lineText[:col]))
		pos := map[string]any{"textDocument": map[string]any{"uri": lsp.URI(abs)}, "position": map[string]any{"line": line - 1, "character": units}}
		if name == "lsp_hover" {
			raw, err := lspRequest(r, rt, lang, "textDocument/hover", pos)
			if err != nil {
				return toolResult{}, err
			}
			hover := strings.TrimSpace(hoverText(raw))
			if hover == "" {
				return ok("No information.", agent.T("nothing", nil).With(symbol+": ", "")), nil
			}
			return ok(hover, agent.T("documentation", nil).With(symbol+": ", "")), nil
		}
		method := "textDocument/references"
		params := map[string]any{"textDocument": pos["textDocument"], "position": pos["position"], "context": map[string]any{"includeDeclaration": false}}
		if name == "lsp_definition" {
			method = "textDocument/definition"
			params = pos
		}
		raw, err := lspRequest(r, rt, lang, method, params)
		if err != nil {
			return toolResult{}, err
		}
		locs := toLocations(raw)
		shown := locs
		if len(shown) > 100 {
			shown = shown[:100]
		}
		rows := locationLines(rt, shown)
		if len(locs) > len(shown) {
			rows = append(rows, fmt.Sprintf("… %d more", len(locs)-len(shown)))
		}
		out := strings.Join(rows, "\n")
		if name == "lsp_definition" {
			if out == "" {
				out = "No definition."
			}
			return ok(out, agent.Tn(len(locs), "{n} definition", "{n} definitions", nil).With(symbol+": ", "")), nil
		}
		if out == "" {
			out = "No reference."
		}
		return ok(out, agent.Tn(len(locs), "{n} reference", "{n} references", nil).With(symbol+": ", "")), nil
	})
}

func utf16Of(s string) []uint16 {
	var out []uint16
	for _, r := range s {
		if r >= 0x10000 {
			out = append(out, 0, 0)
		} else {
			out = append(out, 0)
		}
	}
	return out
}

func hoverText(raw json.RawMessage) string {
	var h struct {
		Contents json.RawMessage `json:"contents"`
	}
	if json.Unmarshal(raw, &h) != nil || len(h.Contents) == 0 {
		return ""
	}
	part := func(c json.RawMessage) string {
		var s string
		if json.Unmarshal(c, &s) == nil {
			return s
		}
		var v struct {
			Value string `json:"value"`
		}
		_ = json.Unmarshal(c, &v)
		return v.Value
	}
	var list []json.RawMessage
	if json.Unmarshal(h.Contents, &list) == nil {
		var parts []string
		for _, c := range list {
			parts = append(parts, part(c))
		}
		return strings.Join(parts, "\n\n")
	}
	return part(h.Contents)
}

func lspWorkspaceSymbols(r *agentRun, rt *runtime.Runtime, query, p string) (toolResult, error) {
	ref := ""
	if p != "" {
		ref = absPath(rt.Root, p)
	} else if f := opt(r.chat).ActiveFile; f != "" {
		ref = absPath(rt.Root, f)
	}
	if ref == "" {
		return toolResult{}, failf("give path: a file of the language")
	}
	lang, err := langOf(rt, ref)
	if err != nil {
		return toolResult{}, err
	}
	raw, err := lspRequest(r, rt, lang, "workspace/symbol", map[string]any{"query": query})
	if err != nil {
		return toolResult{}, err
	}
	var list []docSymbol
	_ = json.Unmarshal(raw, &list)
	var rows []string
	for i, sy := range list {
		if i == 100 {
			rows = append(rows, fmt.Sprintf("… %d more", len(list)-100))
			break
		}
		loc := ""
		if sy.Location != nil {
			loc = fmt.Sprintf("%s:%d", relPath(rt.Root, pathFromURI(sy.Location.URI)), sy.Location.Range.Start.Line+1)
		}
		in := ""
		if sy.ContainerName != "" {
			in = " (" + sy.ContainerName + ")"
		}
		rows = append(rows, fmt.Sprintf("%s %s%s · %s", kindName(sy.Kind), sy.Name, in, loc))
	}
	text := strings.Join(rows, "\n")
	if text == "" {
		text = "No symbol."
	}
	return ok(text, agent.Tn(len(list), "{n} symbol", "{n} symbols", nil).With("“"+query+"”: ", "")), nil
}

func lspDiagnostics(r *agentRun, rt *runtime.Runtime, p string) (toolResult, error) {
	if p == "" {
		all := rt.LSP.AllDiagnostics()
		uris := make([]string, 0, len(all))
		for u := range all {
			uris = append(uris, u)
		}
		sort.Strings(uris)
		var rows []string
		for _, u := range uris {
			rows = append(rows, formatDiagnostics(rt, pathFromURI(u), all[u])...)
		}
		n := len(rows)
		if len(rows) > 300 {
			rows = rows[:300]
		}
		text := strings.Join(rows, "\n")
		if text == "" {
			text = "No diagnostic in the open files."
		}
		return ok(text, agent.Tn(n, "{n} diagnostic", "{n} diagnostics", nil)), nil
	}
	abs := absPath(rt.Root, p)
	rel := relPath(rt.Root, abs)
	uri := lsp.URI(abs)
	_, before := rt.LSP.Diagnostics(uri)
	// Open it in its server and wait a little for the diagnostics it publishes.
	rows, err := withDoc(rt, abs, func(lang, _ string) ([]string, error) {
		until := time.Now().Add(4 * time.Second)
		for time.Now().Before(until) && r.ctx.Err() == nil {
			if _, seq := rt.LSP.Diagnostics(uri); seq != before {
				break
			}
			time.Sleep(150 * time.Millisecond)
		}
		items, _ := rt.LSP.Diagnostics(uri)
		return formatDiagnostics(rt, abs, items), nil
	})
	if err != nil {
		return toolResult{}, err
	}
	text := strings.Join(rows, "\n")
	if text == "" {
		text = "No diagnostic for " + rel + "."
	}
	return ok(text, agent.Tn(len(rows), "{n} diagnostic", "{n} diagnostics", nil).With(rel+": ", "")), nil
}
