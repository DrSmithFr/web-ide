package server

import (
	"context"
	"encoding/json"
	"fmt"
	"path"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/DrSmithFr/web-ide/pod/internal/agent"
	"github.com/DrSmithFr/web-ide/pod/internal/console"
	"github.com/DrSmithFr/web-ide/pod/internal/i18n"
	"github.com/DrSmithFr/web-ide/pod/internal/llm"
	"github.com/DrSmithFr/web-ide/pod/internal/projects"
	"github.com/DrSmithFr/web-ide/pod/internal/runtime"
	"github.com/DrSmithFr/web-ide/pod/internal/search"
)

// Tools of the agent that work on the project: files, search, shell commands, consoles,
// skills. The texts read by the model are English; the summaries are translated by the page.

// runtimeRef is the runtime of the project a run works in.
type runtimeRef struct {
	rt      *runtime.Runtime
	project *projects.Project
}

type toolResult struct {
	Content string
	Summary json.RawMessage
	Status  string
	Diff    []agent.DiffLine
	Page    *agent.Page
	Preview *agent.Preview
	Child   string
	Card    *agent.ActionCard
	Opened  string
}

func ok(content string, summary agent.Text) toolResult {
	return toolResult{Content: content, Summary: summary.Raw(), Status: "ok"}
}

func okPlain(content, summary string) toolResult {
	return toolResult{Content: content, Summary: agent.Plain(summary), Status: "ok"}
}

func fail(r *agentRun, err error) toolResult {
	return toolResult{Content: "Error: " + i18n.Translate("en", err), Summary: agent.Plain(i18n.Translate(r.lang, err)), Status: "error"}
}

func failf(format string, a ...any) error { return fmt.Errorf(format, a...) }

// args of a tool call, read field by field.
type toolArgs map[string]json.RawMessage

func (a toolArgs) str(k string) string {
	var s string
	if json.Unmarshal(a[k], &s) != nil {
		var n json.Number
		if json.Unmarshal(a[k], &n) == nil {
			return n.String()
		}
	}
	return s
}

func (a toolArgs) num(k string) int {
	var f float64
	if json.Unmarshal(a[k], &f) == nil {
		return int(f)
	}
	var s string
	if json.Unmarshal(a[k], &s) == nil {
		var n int
		fmt.Sscan(s, &n)
		return n
	}
	return 0
}

func (a toolArgs) boolean(k string, def bool) bool {
	var b bool
	if json.Unmarshal(a[k], &b) == nil {
		return b
	}
	return def
}

func (a toolArgs) has(k string) bool { return len(a[k]) > 0 && string(a[k]) != "null" }

// absPath is the absolute path of a path given by the model, relative to the root.
func absPath(root, p string) string {
	p = strings.TrimSpace(p)
	base := root
	if strings.HasPrefix(p, "/") {
		base = ""
	}
	return path.Clean("/" + base + "/" + p)
}

func relPath(root, p string) string {
	if p == root {
		return "."
	}
	return strings.TrimPrefix(p, strings.TrimSuffix(root, "/")+"/")
}

// agentTool runs a tool call of the model.
func (s *Server) agentTool(r *agentRun, ref *runtimeRef, call agent.ToolCall, mode string) (res toolResult) {
	name := call.Function.Name
	defer func() {
		if p := recover(); p != nil {
			res = fail(r, fmt.Errorf("%v", p))
		}
	}()
	var a toolArgs
	if err := json.Unmarshal([]byte(orEmpty(call.Function.Arguments)), &a); err != nil {
		return fail(r, failf("invalid JSON arguments: %.200s", call.Function.Arguments))
	}
	if agent.KanbanTools[name] {
		return s.kanbanTool(r, name, a, mode)
	}
	if agent.DockerTools[name] {
		return s.dockerTool(r, ref, name, a)
	}
	root := ref.rt.Root
	if mode != agent.Build {
		if agent.WriteTools[name] {
			if mode == agent.Plan {
				return fail(r, failf("Plan mode: files cannot be changed. Present the plan with exit_plan_mode; it will be carried out in Build mode."))
			}
			if mode == agent.Orchestrator {
				return fail(r, failf("Orchestrator mode: files cannot be changed. Propose the work with action_card, or open a conversation for it with open_conversation."))
			}
			return fail(r, failf("Briefing mode: files cannot be changed. Clarify the need and write it in tickets (kanban_create)."))
		}
		// A command that may change something waits for the user.
		if name == "bash" || name == "run_command" {
			cwd := root
			if a.str("cwd") != "" {
				cwd = absPath(root, a.str("cwd"))
			}
			if !agent.RunsFreely(a.str("command"), root, cwd) && !s.confirm(r, agent.Approval{Call: call, Kind: "command", Command: a.str("command")}) {
				which := map[string]string{agent.Plan: "Plan", agent.Briefing: "Briefing", agent.Orchestrator: "Orchestrator"}[mode]
				return toolResult{Content: "The user refused this command (" + which + " mode: only reading commands, and the build and test commands of the project, run freely).", Summary: agent.T("command refused", nil).Raw(), Status: "denied"}
			}
		}
	}
	var err error
	switch name {
	case "list_dir":
		res, err = listDir(ref.rt, a.str("path"))
	case "find_files":
		res, err = findFiles(r.ctx, ref.rt, a.str("pattern"))
	case "read_file":
		res, err = readFileTool(ref.rt, a.str("path"), a.num("start_line"), a.num("end_line"))
	case "search_text":
		res, err = searchText(r.ctx, ref.rt, a.str("query"), a.boolean("regex", false), a.str("include"))
	case "edit_file":
		res, err = s.editFile(r, ref.rt, call, a)
	case "write_file":
		res, err = s.writeFile(r, ref.rt, call, a)
	case "lsp_symbols", "lsp_workspace_symbols", "lsp_definition", "lsp_references", "lsp_hover", "lsp_diagnostics":
		res, err = s.lspTool(r, ref.rt, name, a)
	case "load_skill":
		res, err = s.loadSkill(ref.rt, a.str("name"))
	case "read_skill_file":
		res, err = s.readSkillFile(ref.rt, a.str("name"), a.str("file"))
	case "open_file", "focus":
		res, err = s.uiTool(r, name, a)
	case "board_draw_doodle":
		res, err = s.boardDoodle(r, a)
	case "board_draw_image":
		res, err = s.boardImage(r, ref.rt, a)
	case "kanban_next", "kanban_history", "list_conversations", "action_card", "open_conversation":
		res, err = s.orchestratorTool(r, ref, name, a)
	case "spawn_agent":
		res, err = s.spawnAgent(r, a)
	case "agent_reply":
		res, err = s.agentReply(r, a)
	case "agent_message":
		res, err = s.agentMessage(r, a)
	case "agent_stop":
		res, err = s.agentStop(r, a)
	case "agent_status":
		res, err = s.agentStatus(r)
	case "agent_note":
		res, err = s.agentNote(r, a)
	case "share_preview":
		res, err = sharePreview(r, ref.rt, a)
	case "bash":
		res, err = bashTool(r.ctx, ref.rt, a.str("command"), a.str("cwd"), a.num("timeout"))
	case "run_command":
		res, err = s.runCommand(r, ref.rt, a.str("command"), a.str("cwd"), a.num("timeout"))
	case "list_consoles":
		res = listConsoles(ref.rt)
	case "read_console":
		res, err = readConsole(ref.rt, a.str("console_id"), a.num("lines"))
	case "console_input":
		res, err = consoleInput(r.ctx, ref.rt, a.str("console_id"), a.str("text"), a.boolean("enter", true))
	default:
		err = failf("unknown tool: %s", name)
	}
	if err != nil {
		return fail(r, err)
	}
	return res
}

// confirm asks the user before a file change or a command; false when refused or stopped.
// "Apply without asking" covers the file changes; commands always ask.
func (s *Server) confirm(r *agentRun, req agent.Approval) bool {
	r.mu.Lock()
	if req.Kind == "edit" && opt(r.chat).AutoApply {
		r.mu.Unlock()
		return true
	}
	req.ID = newID()
	ch := make(chan bool, 1)
	r.approve = ch
	r.chat.Approval = &req
	prev := r.state
	r.state = "waiting_user"
	s.publish(r, -1)
	s.emitAgent(r.root, "agent.attention", map[string]any{"id": r.id, "title": r.chat.Title, "kind": req.Kind, "sub": r.chat.Parent != ""})
	r.mu.Unlock()
	var allowed bool
	select {
	case allowed = <-ch:
	case <-r.ctx.Done():
	}
	r.mu.Lock()
	r.approve, r.chat.Approval, r.state = nil, nil, prev
	s.publish(r, -1)
	r.mu.Unlock()
	return allowed
}

// ---------- files ----------

const (
	maxLines = 1500
	maxChars = 120_000
)

func listDir(rt *runtime.Runtime, p string) (toolResult, error) {
	abs := absPath(rt.Root, p)
	entries, err := rt.List(abs)
	if err != nil {
		return toolResult{}, err
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].Dir != entries[j].Dir {
			return entries[i].Dir
		}
		return entries[i].Name < entries[j].Name
	})
	var lines []string
	for i, e := range entries {
		if i == 500 {
			lines = append(lines, fmt.Sprintf("… %d more entries", len(entries)-500))
			break
		}
		if e.Dir {
			lines = append(lines, e.Name+"/")
		} else {
			lines = append(lines, fmt.Sprintf("%s  (%d o)", e.Name, e.Size))
		}
	}
	text := strings.Join(lines, "\n")
	if text == "" {
		text = "(empty folder)"
	}
	return ok(text, agent.Tn(len(entries), "{n} entry", "{n} entries", nil).With(relPath(rt.Root, abs)+": ", "")), nil
}

// globRegexp is the regular expression of a * ? ** glob (on the whole path when it has a /,
// else on its end).
func globRegexp(glob string) *regexp.Regexp {
	var re strings.Builder
	for i := 0; i < len(glob); i++ {
		switch c := glob[i]; c {
		case '*':
			if i+1 < len(glob) && glob[i+1] == '*' {
				re.WriteString(".*")
				i++
				if i+1 < len(glob) && glob[i+1] == '/' {
					i++
				}
			} else {
				re.WriteString("[^/]*")
			}
		case '?':
			re.WriteString("[^/]")
		default:
			re.WriteString(regexp.QuoteMeta(string(c)))
		}
	}
	if strings.Contains(glob, "/") {
		return regexp.MustCompile("(?i)^" + re.String() + "$")
	}
	return regexp.MustCompile("(?i)(^|/)" + re.String() + "$")
}

func findFiles(ctx context.Context, rt *runtime.Runtime, pattern string) (toolResult, error) {
	files, err := rt.Files(ctx)
	if err != nil {
		return toolResult{}, err
	}
	files = search.WithoutExcluded(files, rt.Excluded())
	var list []string
	if strings.ContainsAny(pattern, "*?") {
		re := globRegexp(pattern)
		for _, f := range files {
			if re.MatchString(f) {
				list = append(list, f)
			}
		}
	} else {
		q := strings.ToLower(pattern)
		for _, f := range files {
			if strings.Contains(strings.ToLower(f), q) {
				list = append(list, f)
			}
		}
	}
	shown := list
	if len(shown) > 200 {
		shown = shown[:200]
	}
	text := strings.Join(shown, "\n")
	if text == "" {
		text = "No file found."
	}
	if len(list) > len(shown) {
		text += fmt.Sprintf("\n… %d more (refine the pattern)", len(list)-len(shown))
	}
	return ok(text, agent.Tn(len(list), "{n} file", "{n} files", nil).With("“"+pattern+"”: ", "")), nil
}

// readText reads a text file of the project (the saved version).
func readText(rt *runtime.Runtime, abs string) (*runtime.FileContent, error) {
	f, err := rt.Read(abs)
	if err != nil {
		return nil, err
	}
	if f.Binary {
		return nil, failf("%s is a binary or too large file", relPath(rt.Root, abs))
	}
	return f, nil
}

func readFileTool(rt *runtime.Runtime, p string, start, end int) (toolResult, error) {
	if p == "" {
		return toolResult{}, failf("path is missing")
	}
	abs := absPath(rt.Root, p)
	f, err := readText(rt, abs)
	if err != nil {
		return toolResult{}, err
	}
	lines := strings.Split(f.Content, "\n")
	from := max(1, start)
	to := len(lines)
	if end > 0 && end < to {
		to = end
	}
	var b strings.Builder
	truncated := false
	for i := from; i <= to; i++ {
		row := fmt.Sprintf("%d\t%s\n", i, lines[i-1])
		if i-from >= maxLines || b.Len()+len(row) > maxChars {
			truncated = true
			to = i - 1
			break
		}
		b.WriteString(row)
	}
	out := b.String()
	if truncated {
		out += fmt.Sprintf("… (truncated: read on with start_line=%d)\n", to+1)
	}
	if from > len(lines) {
		out = fmt.Sprintf("(the file has only %d lines)", len(lines))
	}
	return ok(out, agent.T("{path}: lines {from}-{to} of {total}", map[string]any{"path": relPath(rt.Root, abs), "from": from, "to": to, "total": len(lines)})), nil
}

func searchText(ctx context.Context, rt *runtime.Runtime, query string, regex bool, include string) (toolResult, error) {
	if query == "" {
		return toolResult{}, failf("query is missing")
	}
	o := search.Options{Query: query, Regex: regex, Include: include, Max: 200, Exclude: rt.Excluded()}
	var res *search.Result
	var err error
	if rt.Local {
		res, err = search.Local(ctx, rt.Root, o)
	} else {
		res, err = search.Remote(ctx, rt.Runner, rt.Root, o)
	}
	if err != nil {
		return toolResult{}, err
	}
	var lines []string
	for _, m := range res.Matches {
		t := strings.TrimSpace(m.Text)
		if len(t) > 300 {
			t = t[:300]
		}
		lines = append(lines, fmt.Sprintf("%s:%d: %s", relPath(rt.Root, m.Path), m.Line, t))
	}
	text := strings.Join(lines, "\n")
	if text == "" {
		text = "No result."
	}
	plus := ""
	if res.Truncated {
		text += "\n… (results truncated, refine the search)"
		plus = "+"
	}
	return ok(text, agent.Tn(len(res.Matches), "{n} result", "{n} results", nil).With("“"+query+"”: ", plus)), nil
}

// counts of a diff: lines added and removed.
func counts(d []agent.DiffLine) (int, int) {
	add, del := 0, 0
	for _, l := range d {
		switch l.T {
		case "+":
			add++
		case "-":
			del++
		}
	}
	return add, del
}

func (s *Server) editFile(r *agentRun, rt *runtime.Runtime, call agent.ToolCall, a toolArgs) (toolResult, error) {
	p := a.str("path")
	if p == "" {
		return toolResult{}, failf("path is missing")
	}
	if !a.has("old_string") || !a.has("new_string") {
		return toolResult{}, failf("old_string and new_string are required")
	}
	oldStr, newStr := a.str("old_string"), a.str("new_string")
	if oldStr == newStr {
		return toolResult{}, failf("old_string and new_string are identical")
	}
	abs := absPath(rt.Root, p)
	f, err := readText(rt, abs)
	if err != nil {
		return toolResult{}, err
	}
	text := f.Content
	rel := relPath(rt.Root, abs)
	at := strings.Index(text, oldStr)
	if at < 0 {
		return toolResult{}, failf("old_string not found in %s (read the file again with read_file and copy the exact text, without the line numbers)", rel)
	}
	if strings.Contains(text[at+1:], oldStr) {
		return toolResult{}, failf("old_string appears several times in %s: add context to make it unique", rel)
	}
	next := text[:at] + newStr + text[at+len(oldStr):]
	diff := agent.DiffLines(text, next)
	if !s.confirm(r, agent.Approval{Call: call, Kind: "edit", Path: abs, Diff: diff}) {
		return toolResult{Content: "The user refused this change.", Summary: agent.T("change refused", nil).Raw(), Status: "denied", Diff: diff}, nil
	}
	if _, err := rt.Write(abs, next, f.Format, ""); err != nil {
		return toolResult{}, err
	}
	s.emitter(r.project)("git.changed", nil, "")
	line := strings.Count(text[:at], "\n") + 1
	add, del := counts(diff)
	res := okPlain(fmt.Sprintf("Change applied to %s (line %d).", rel, line), fmt.Sprintf("%s: +%d −%d", rel, add, del))
	res.Diff = diff
	return res, nil
}

func (s *Server) writeFile(r *agentRun, rt *runtime.Runtime, call agent.ToolCall, a toolArgs) (toolResult, error) {
	p := a.str("path")
	if p == "" {
		return toolResult{}, failf("path is missing")
	}
	if !a.has("content") {
		return toolResult{}, failf("content is missing")
	}
	content := a.str("content")
	abs := absPath(rt.Root, p)
	rel := relPath(rt.Root, abs)
	before := ""
	var format runtime.Format
	_, statErr := rt.FS.Stat(abs)
	created := statErr != nil
	if !created {
		f, err := readText(rt, abs)
		if err != nil {
			return toolResult{}, err
		}
		before, format = f.Content, f.Format
		if before == content {
			return ok(rel+" already has this content.", agent.T("{path}: unchanged", map[string]any{"path": rel})), nil
		}
	}
	diff := agent.DiffLines(before, content)
	if created && len(diff) > 0 && diff[0].T == "-" {
		diff = diff[1:] // the empty line of "nothing"
	}
	if !s.confirm(r, agent.Approval{Call: call, Kind: "edit", Path: abs, Diff: diff, Created: created}) {
		return toolResult{Content: "The user refused this write.", Summary: agent.T("write refused", nil).Raw(), Status: "denied", Diff: diff}, nil
	}
	if created {
		_ = rt.FS.Mkdir(path.Dir(abs))
	}
	if _, err := rt.Write(abs, content, format, ""); err != nil {
		return toolResult{}, err
	}
	s.emitter(r.project)("git.changed", nil, "")
	n := strings.Count(content, "\n") + 1
	word := "lines"
	if n == 1 {
		word = "line"
	}
	verb, one, other := "replaced", "{path} replaced ({n} line)", "{path} replaced ({n} lines)"
	if created {
		verb, one, other = "created", "{path} created ({n} line)", "{path} created ({n} lines)"
	}
	res := ok(fmt.Sprintf("%s %s (%d %s).", rel, verb, n, word), agent.Tn(n, one, other, map[string]any{"path": rel}))
	res.Diff = diff
	return res, nil
}

// ---------- skills ----------

func (s *Server) loadSkill(rt *runtime.Runtime, name string) (toolResult, error) {
	if name == "" {
		return toolResult{}, failf("name is missing")
	}
	sk, err := s.LLM.ReadSkill(llm.Project{Root: rt.Root, FS: rt.FS}, name)
	if err != nil {
		return toolResult{}, err
	}
	text, _ := sk["content"].(string)
	if files, _ := sk["files"].([]string); len(files) > 0 {
		text += "\n\n---\nOther files of the skill (read_skill_file):"
		for _, f := range files {
			text += "\n- " + f
		}
	}
	return ok(text, agent.T("skill {name} loaded", map[string]any{"name": name})), nil
}

func (s *Server) readSkillFile(rt *runtime.Runtime, name, file string) (toolResult, error) {
	if name == "" || file == "" {
		return toolResult{}, failf("name and file are required")
	}
	text, err := s.LLM.ReadSkillFile(llm.Project{Root: rt.Root, FS: rt.FS}, name, file)
	if err != nil {
		return toolResult{}, err
	}
	return okPlain(text, name+"/"+file), nil
}

// ---------- shell and consoles ----------

func bashTool(ctx context.Context, rt *runtime.Runtime, command, cwd string, timeout int) (toolResult, error) {
	if strings.TrimSpace(command) == "" {
		return toolResult{}, failf("command is missing")
	}
	if cwd != "" {
		cwd = absPath(rt.Root, cwd)
	}
	if timeout <= 0 {
		timeout = 120
	}
	res, err := runShell(ctx, rt, command, cwd, timeout)
	if err != nil {
		return toolResult{}, err
	}
	secs := fmt.Sprintf("%.1f", float64(res.DurationMs)/1000)
	out := agent.TrimEnd(agent.PlainOutput(res.Output))
	if out == "" {
		out = "(no output)"
	}
	state := fmt.Sprintf("Exit code %d", res.Code)
	switch {
	case res.Canceled:
		state = "Canceled"
	case res.TimedOut:
		state = "Stopped after the timeout (" + secs + " s)"
	}
	if res.Truncated {
		state += " (output cut in the middle)"
	}
	tr := toolResult{Content: state + "\n" + out, Status: "ok"}
	switch {
	case res.TimedOut:
		tr.Summary = agent.T("timeout", nil).Raw()
	case res.Canceled:
		tr.Summary = agent.T("canceled", nil).Raw()
	default:
		tr.Summary = agent.Plain(fmt.Sprintf("code %d · %s s", res.Code, secs))
	}
	if res.Code != 0 || res.TimedOut || res.Canceled {
		tr.Status = "error"
	}
	return tr, nil
}

func consoleText(rt *runtime.Runtime, id string) (string, *consoleInfo, error) {
	info, buf, err := rt.Consoles.Attach(id)
	if err != nil {
		return "", nil, err
	}
	return agent.PlainOutput(string(buf)), &consoleInfo{Title: info.Title, Exited: info.Exited, Code: info.Code}, nil
}

type consoleInfo struct {
	Title  string
	Exited bool
	Code   int
}

func (s *Server) runCommand(r *agentRun, rt *runtime.Runtime, command, cwd string, timeout int) (toolResult, error) {
	if strings.TrimSpace(command) == "" {
		return toolResult{}, failf("command is missing")
	}
	if timeout <= 0 {
		timeout = 20
	}
	limit := time.Duration(min(max(timeout, 1), 600)) * time.Second
	info, err := s.startCommand(rt, r.project, command, cwd)
	if err != nil {
		return toolResult{}, err
	}
	// The console comes to the front in the windows of the project (if any).
	go s.uiTool(r, "focus", toolArgs{"target": json.RawMessage(`"console"`), "console_id": jsonString(info.ID), "quiet": json.RawMessage("true")})
	until := time.Now().Add(limit)
	var text string
	var now *consoleInfo
	for {
		text, now, err = consoleText(rt, info.ID)
		if err != nil {
			return toolResult{}, err
		}
		if now.Exited || time.Now().After(until) || r.ctx.Err() != nil {
			break
		}
		time.Sleep(150 * time.Millisecond)
	}
	out := agent.Tail(text, 300, 12000)
	if !now.Exited {
		return ok(fmt.Sprintf("The command is still running (console %s). Output so far:\n%s\n\nFollow it with read_console, interact with console_input.", info.ID, out), agent.T("{command}: still running", map[string]any{"command": command})), nil
	}
	if out == "" {
		out = "(no output)"
	}
	tr := toolResult{Content: fmt.Sprintf("Exit code: %d (console %s)\n%s", now.Code, info.ID, out), Summary: agent.Plain(fmt.Sprintf("code %d", now.Code)), Status: "ok"}
	if now.Code != 0 {
		tr.Status = "error"
	}
	return tr, nil
}

// startCommand runs a command in a new console of a project, shown in its windows.
func (s *Server) startCommand(rt *runtime.Runtime, project, command, cwd string) (console.Info, error) {
	title := command
	if r := []rune(title); len(r) > 60 {
		title = string(r[:57]) + "…"
	}
	if cwd != "" {
		cwd = absPath(rt.Root, cwd)
	}
	info, err := rt.Consoles.Create("task", title, []string{"sh", "-c", command}, cwd, 160, 40)
	if err != nil {
		return info, err
	}
	s.emitter(project)("console.created", info, "")
	return info, nil
}

func jsonString(s string) json.RawMessage {
	data, _ := json.Marshal(s)
	return data
}

func listConsoles(rt *runtime.Runtime) toolResult {
	list := rt.Consoles.List()
	var rows []string
	for _, c := range list {
		kind := "terminal"
		if c.Kind == "task" {
			kind = "command"
		}
		state := " · running"
		if c.Exited {
			state = fmt.Sprintf(" · exited (code %d)", c.Code)
		}
		rows = append(rows, fmt.Sprintf("%s · %s · %s%s", c.ID, kind, c.Title, state))
	}
	text := strings.Join(rows, "\n")
	if text == "" {
		text = "No open console."
	}
	return ok(text, agent.Tn(len(list), "{n} console", "{n} consoles", nil))
}

func readConsole(rt *runtime.Runtime, id string, lines int) (toolResult, error) {
	if id == "" {
		return toolResult{}, failf("console_id is missing")
	}
	text, info, err := consoleText(rt, id)
	if err != nil {
		return toolResult{}, err
	}
	if lines <= 0 {
		lines = 200
	}
	state, sum := "running", agent.T("running", nil)
	if info.Exited {
		state = fmt.Sprintf("exited (code %d)", info.Code)
		sum = agent.T("exited (code {code})", map[string]any{"code": info.Code})
	}
	out := agent.Tail(text, lines, 12000)
	if out == "" {
		out = "(no output)"
	}
	return ok(fmt.Sprintf("Console \"%s\", %s:\n%s", info.Title, state, out), sum.With(info.Title+" · ", "")), nil
}

func consoleInput(ctx context.Context, rt *runtime.Runtime, id, text string, enter bool) (toolResult, error) {
	if text != "" || enter {
		data := text
		if enter {
			data += "\r"
		}
		if err := rt.Consoles.Input(id, []byte(data)); err != nil {
			return toolResult{}, failf("console not found: %s", id)
		}
	}
	select {
	case <-time.After(800 * time.Millisecond):
	case <-ctx.Done():
	}
	out, _, err := consoleText(rt, id)
	if err != nil {
		return toolResult{}, err
	}
	return ok("Text sent. End of the output:\n"+agent.Tail(out, 40, 12000), agent.T("typed in {id}", map[string]any{"id": id})), nil
}
