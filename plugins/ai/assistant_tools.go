package ai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"regexp"
	"sort"
	"strings"
	"time"

	"antennae.space/ide/lib/i18n"
	"antennae.space/ide/plugins/ai/engine"
	"antennae.space/ide/plugins/ai/llm"
	"antennae.space/ide/pod/assistant"
	"antennae.space/ide/pod/caps"
	"antennae.space/ide/pod/console"
	"antennae.space/ide/pod/fsx"
	"antennae.space/ide/pod/plugins"
	"antennae.space/ide/pod/projects"
	"antennae.space/ide/pod/runtime"
)

// Tools of the assistant that work on the project: files, search, shell commands, consoles,
// skills. The texts read by the model are English; the summaries are translated by the page.

// runtimeRef is the runtime of the project a run works in.
type runtimeRef struct {
	rt      *runtime.Runtime
	project *projects.Project
}

func fail(r *assistantRun, err error) assistant.ToolResult {
	failure := "error"
	if errors.As(err, new(assistant.UsageError)) || errors.As(err, new(plugins.UsageError)) {
		failure = "usage"
	}
	return assistant.ToolResult{Content: "Error: " + i18n.Translate("en", err), Summary: assistant.Plain(i18n.Translate(r.lang, err)), Status: "error", Failure: failure}
}

func failf(format string, a ...any) error { return fmt.Errorf(format, a...) }

// assistantTool runs a tool call of the model.
func (s *A) assistantTool(r *assistantRun, ref *runtimeRef, call engine.ToolCall, mode string) (res assistant.ToolResult) {
	name := call.Function.Name
	defer func() {
		if p := recover(); p != nil {
			res = fail(r, fmt.Errorf("%v", p))
		}
	}()
	var a assistant.Args
	if err := json.Unmarshal([]byte(orEmpty(call.Function.Arguments)), &a); err != nil {
		return fail(r, assistant.Usagef("invalid JSON arguments: %.200s", call.Function.Arguments))
	}
	if res, ok := s.followed(r, ref, call, a); ok {
		return res
	}
	if engine.KanbanTools[name] {
		return s.kanbanTool(r, name, a, mode)
	}
	if err := s.missingFor(ref.rt, name); err != nil {
		return fail(r, err)
	}
	r.mu.Lock()
	options := opt(r.chat)
	r.mu.Unlock()
	if res, found, err := s.pod.RunAssistantTool(r.ctx, ref.rt, name, []byte(orEmpty(call.Function.Arguments)), r.lang, options); found {
		if err != nil {
			return fail(r, err)
		}
		return res
	}
	root := ref.rt.Root
	which := map[string]string{engine.Plan: "Plan", engine.Briefing: "Briefing", engine.Orchestrator: "Orchestrator"}[mode]
	// Out of Build mode, files change only in the scratch folder.
	if mode != engine.Build && engine.WriteTools[name] && !assistant.InZone(assistant.AbsPath(root, a.Str("path")), assistant.Scratch) {
		then := map[string]string{
			engine.Plan:         "Present the plan with exit_plan_mode; it will be carried out in Build mode.",
			engine.Briefing:     "Clarify the need and write it in tickets (kanban_create).",
			engine.Orchestrator: "Propose the work with action_card, or open a conversation for it with open_conversation.",
		}[mode]
		return fail(r, failf("%s mode: files of the project cannot be changed (only scratch files in %s). %s", which, assistant.Scratch, then))
	}
	// A command that may change something out of its zone waits for the user: the project and
	// the scratch folder in Build mode; the scratch folder, reading commands and the builds and
	// tests of the project in the other modes.
	if name == "bash" || name == "run_command" {
		cmd, cwd := a.Str("command"), root
		if a.Str("cwd") != "" {
			cwd = assistant.AbsPath(root, a.Str("cwd"))
		}
		free := assistant.StaysIn(cmd, cwd, root, assistant.Scratch)
		if mode != engine.Build {
			free = assistant.RunsFreely(cmd, root, cwd) || assistant.StaysIn(cmd, cwd, assistant.Scratch)
		}
		if !free && !s.confirm(r, root, engine.Approval{Call: call, Kind: "command", Command: cmd}) {
			why := "Build mode: commands that touch nothing out of the project and " + assistant.Scratch + " run freely"
			if mode != engine.Build {
				why = which + " mode: only reading commands, the build and test commands of the project, and commands confined to " + assistant.Scratch + ", run freely"
			}
			return assistant.ToolResult{Content: "The user refused this command (" + why + ").", Summary: assistant.T("command refused", nil).Raw(), Status: "denied"}
		}
	}
	// A saved command was written by the user: it runs freely in Build mode, and its builds,
	// tests and lints in the other modes.
	if name == "run_saved_command" && mode != engine.Build {
		if cmd, err := s.findCommand(ref.rt, a.Str("id")); err == nil && cmd.Flag != "build" && cmd.Flag != "test" && cmd.Flag != "lint" &&
			!s.confirm(r, root, engine.Approval{Call: call, Kind: "command", Command: cmd.Command}) {
			why := which + " mode: only the build, test and lint commands of the project run freely"
			return assistant.ToolResult{Content: "The user refused this command (" + why + ").", Summary: assistant.T("command refused", nil).Raw(), Status: "denied"}
		}
	}
	var err error
	switch name {
	case "list_dir":
		res, err = listDir(ref.rt, a.Str("path"))
	case "find_files":
		res, err = findFiles(r.ctx, ref.rt, a.Str("pattern"))
	case "read_file":
		res, err = readFileTool(ref.rt, a.Str("path"), a.Num("start_line"), a.Num("end_line"))
	case "search_text":
		res, err = s.searchText(r.ctx, ref.rt, a.Str("query"), a.Bool("regex", false), a.Str("include"))
	case "edit_file":
		res, err = s.editFile(r, ref.rt, call, a)
	case "write_file":
		res, err = s.writeFile(r, ref.rt, call, a)
	case "lsp_symbols", "lsp_workspace_symbols", "lsp_definition", "lsp_references", "lsp_hover", "lsp_diagnostics":
		res, err = s.lspTool(r, ref.rt, name, a)
	case "load_skill":
		res, err = s.loadSkill(ref.rt, a.Str("name"))
	case "read_skill_file":
		res, err = s.readSkillFile(ref.rt, a.Str("name"), a.Str("file"))
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
	case "agent_adopt":
		res, err = s.agentAdopt(r, a)
	case "agent_resume":
		res, err = s.agentResume(r, a)
	case "agent_note":
		res, err = s.agentNote(r, a)
	case "share_preview":
		if pv, ok := s.pod.Capability(ref.rt, "previews").(caps.Previews); ok {
			res, err = pv.Share(r.project, ref.rt, a)
		} else {
			err = i18n.Errorf("the %s plugin is disabled on this project", "Previews")
		}
	case "bash":
		res, err = bashTool(r.ctx, ref.rt, a.Str("command"), a.Str("cwd"), a.Num("timeout"), s.toolProgress(r, call.ID), s.keptFor(r))
	case "run_command":
		res, err = s.runCommand(r, ref.rt, a.Str("command"), a.Str("cwd"), a.Num("timeout"), s.toolProgress(r, call.ID))
	case "list_consoles":
		res = assistant.ListConsoles(ref.rt)
	case "list_commands":
		res, err = s.listCommandsTool(ref.rt)
	case "run_saved_command":
		res, err = s.runSavedTool(r, ref.rt, a, s.toolProgress(r, call.ID))
	case "read_console":
		res, err = assistant.ReadConsole(ref.rt, a.Str("console_id"), a.Num("lines"))
	case "console_input":
		res, err = assistant.ConsoleInput(r.ctx, ref.rt, a.Str("console_id"), a.Str("text"), a.Bool("enter", true))
	default:
		err = assistant.Usagef("unknown tool: %s", name)
	}
	if err != nil {
		return fail(r, err)
	}
	s.addNested(r, ref, name, a, &res)
	return res
}

// toolProgress shows the output of a tool call while it runs: the page follows it in its
// block. Not saved (the result replaces it), and only the end of a long output.
func (s *A) toolProgress(r *assistantRun, callID string) func(string) {
	return func(text string) {
		term := assistant.ColorOutput(text)
		if term != "" {
			term = assistant.Tail(term, 400, 16000)
		}
		text = assistant.Tail(assistant.PlainOutput(text), 400, 16000)
		r.mu.Lock()
		defer r.mu.Unlock()
		for i := len(r.chat.Messages) - 1; i >= 0; i-- {
			if m := r.chat.Messages[i]; m.Role == "tool" && m.ToolCallID == callID {
				if m.Status == "" {
					m.Content, m.Term = assistant.String(text), term
					s.emitAssistant(r.root, "assistant.update", updateOf(r.chat, r.state, r.ahead, i))
				}
				return
			}
		}
	}
}

// confirm asks the user before a file change or a command; false when refused or stopped.
// Changes in the project or the scratch folder apply without asking; "Apply without asking"
// covers the others; commands always ask.
func (s *A) confirm(r *assistantRun, root string, req engine.Approval) bool {
	r.mu.Lock()
	if req.Kind == "edit" && (opt(r.chat).AutoApply || assistant.InZone(req.Path, root, assistant.Scratch)) {
		r.mu.Unlock()
		return true
	}
	req.ID = assistant.NewID()
	ch := make(chan bool, 1)
	r.approve = ch
	r.chat.Approval = &req
	prev := r.state
	r.state = "waiting_user"
	s.publish(r, -1)
	s.emitAssistant(r.root, "assistant.attention", map[string]any{"id": r.id, "title": r.chat.Title, "kind": req.Kind, "sub": r.chat.Parent != ""})
	s.notifyChat(true, r.root, r.id, r.chat.Title)
	r.mu.Unlock()
	asked := time.Now()
	var allowed bool
	select {
	case allowed = <-ch:
	case <-r.ctx.Done():
	}
	r.mu.Lock()
	r.waited += time.Since(asked)
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

func listDir(rt *runtime.Runtime, p string) (assistant.ToolResult, error) {
	abs := assistant.AbsPath(rt.Root, p)
	entries, err := rt.List(abs)
	if err != nil {
		return assistant.ToolResult{}, err
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
	return assistant.OK(text, assistant.Tn(len(entries), "{n} entry", "{n} entries", nil).With(assistant.RelPath(rt.Root, abs)+": ", "")), nil
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

func findFiles(ctx context.Context, rt *runtime.Runtime, pattern string) (assistant.ToolResult, error) {
	files, err := rt.Files(ctx)
	if err != nil {
		return assistant.ToolResult{}, err
	}
	files = runtime.WithoutExcluded(files, rt.Excluded())
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
	return assistant.OK(text, assistant.Tn(len(list), "{n} file", "{n} files", nil).With("“"+pattern+"”: ", "")), nil
}

// readText reads a text file of the project (the saved version).
func readText(rt *runtime.Runtime, abs string) (*runtime.FileContent, error) {
	f, err := rt.Read(abs)
	if err != nil {
		return nil, err
	}
	if f.Binary {
		return nil, failf("%s is a binary or too large file", assistant.RelPath(rt.Root, abs))
	}
	return f, nil
}

func readFileTool(rt *runtime.Runtime, p string, start, end int) (assistant.ToolResult, error) {
	if p == "" {
		return assistant.ToolResult{}, assistant.Usagef("path is missing")
	}
	abs := assistant.AbsPath(rt.Root, p)
	f, err := readText(rt, abs)
	if err != nil {
		return assistant.ToolResult{}, err
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
	return assistant.OK(out, assistant.T("{path}: lines {from}-{to} of {total}", map[string]any{"path": assistant.RelPath(rt.Root, abs), "from": from, "to": to, "total": len(lines)})), nil
}

func (s *A) searchText(ctx context.Context, rt *runtime.Runtime, query string, regex bool, include string) (assistant.ToolResult, error) {
	if query == "" {
		return assistant.ToolResult{}, assistant.Usagef("query is missing")
	}
	// The search is the Search plugin's (its search capability); needs.go offers the tool only
	// where it is on.
	sr, ok := s.pod.Capability(rt, "search").(caps.Search)
	if !ok {
		return assistant.ToolResult{}, i18n.Errorf("the %s plugin is disabled on this project", "Search")
	}
	res, err := sr.Grep(ctx, rt, caps.SearchOptions{Query: query, Regex: regex, Include: include, Max: 200})
	if err != nil {
		return assistant.ToolResult{}, err
	}
	var lines []string
	for _, m := range res.Matches {
		t := strings.TrimSpace(m.Text)
		if len(t) > 300 {
			t = t[:300]
		}
		lines = append(lines, fmt.Sprintf("%s:%d: %s", assistant.RelPath(rt.Root, m.Path), m.Line, t))
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
	return assistant.OK(text, assistant.Tn(len(res.Matches), "{n} result", "{n} results", nil).With("“"+query+"”: ", plus)), nil
}

// counts of a diff: lines added and removed.
func counts(d []assistant.DiffLine) (int, int) {
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

func (s *A) editFile(r *assistantRun, rt *runtime.Runtime, call engine.ToolCall, a assistant.Args) (assistant.ToolResult, error) {
	p := a.Str("path")
	if p == "" {
		return assistant.ToolResult{}, assistant.Usagef("path is missing")
	}
	if !a.Has("old_string") || !a.Has("new_string") {
		return assistant.ToolResult{}, assistant.Usagef("old_string and new_string are required")
	}
	oldStr, newStr := a.Str("old_string"), a.Str("new_string")
	if oldStr == newStr {
		return assistant.ToolResult{}, assistant.Usagef("old_string and new_string are identical")
	}
	abs := assistant.AbsPath(rt.Root, p)
	f, err := readText(rt, abs)
	if err != nil {
		return assistant.ToolResult{}, err
	}
	text := f.Content
	rel := assistant.RelPath(rt.Root, abs)
	at := strings.Index(text, oldStr)
	if at < 0 {
		return assistant.ToolResult{}, assistant.Usagef("old_string not found in %s (read the file again with read_file and copy the exact text, without the line numbers)", rel)
	}
	if strings.Contains(text[at+1:], oldStr) {
		return assistant.ToolResult{}, assistant.Usagef("old_string appears several times in %s: add context to make it unique", rel)
	}
	next := text[:at] + newStr + text[at+len(oldStr):]
	diff := assistant.DiffLines(text, next)
	if !s.confirm(r, rt.Root, engine.Approval{Call: call, Kind: "edit", Path: abs, Diff: diff}) {
		return assistant.ToolResult{Content: "The user refused this change.", Summary: assistant.T("change refused", nil).Raw(), Status: "denied", Diff: diff}, nil
	}
	if err := save(rt, abs, next, f.Format); err != nil {
		return assistant.ToolResult{}, err
	}
	s.pod.Emit(r.project, "git.changed", nil)
	line := strings.Count(text[:at], "\n") + 1
	add, del := counts(diff)
	res := assistant.OKPlain(fmt.Sprintf("Change applied to %s (line %d).", rel, line), fmt.Sprintf("%s: +%d −%d", rel, add, del))
	res.Diff = diff
	return res, nil
}

// save writes a file changed by the assistant: through the runtime in the project (the
// windows follow it), directly elsewhere (the scratch folder, or a change the user approved).
func save(rt *runtime.Runtime, abs, content string, f runtime.Format) error {
	if fsx.Within(rt.Root, abs) {
		_, err := rt.Write(abs, content, f, "")
		return err
	}
	data, err := runtime.EncodeText(content, f)
	if err != nil {
		return err
	}
	return rt.FS.Write(abs, data)
}

func (s *A) writeFile(r *assistantRun, rt *runtime.Runtime, call engine.ToolCall, a assistant.Args) (assistant.ToolResult, error) {
	p := a.Str("path")
	if p == "" {
		return assistant.ToolResult{}, assistant.Usagef("path is missing")
	}
	if !a.Has("content") {
		return assistant.ToolResult{}, assistant.Usagef("content is missing")
	}
	content := a.Str("content")
	abs := assistant.AbsPath(rt.Root, p)
	rel := assistant.RelPath(rt.Root, abs)
	before := ""
	var format runtime.Format
	_, statErr := rt.FS.Stat(abs)
	created := statErr != nil
	if !created {
		f, err := readText(rt, abs)
		if err != nil {
			return assistant.ToolResult{}, err
		}
		before, format = f.Content, f.Format
		if before == content {
			return assistant.OK(rel+" already has this content.", assistant.T("{path}: unchanged", map[string]any{"path": rel})), nil
		}
	}
	diff := assistant.DiffLines(before, content)
	if created && len(diff) > 0 && diff[0].T == "-" {
		diff = diff[1:] // the empty line of "nothing"
	}
	if !s.confirm(r, rt.Root, engine.Approval{Call: call, Kind: "edit", Path: abs, Diff: diff, Created: created}) {
		return assistant.ToolResult{Content: "The user refused this write.", Summary: assistant.T("write refused", nil).Raw(), Status: "denied", Diff: diff}, nil
	}
	if created {
		_ = rt.FS.Mkdir(path.Dir(abs))
	}
	if err := save(rt, abs, content, format); err != nil {
		return assistant.ToolResult{}, err
	}
	s.pod.Emit(r.project, "git.changed", nil)
	n := strings.Count(content, "\n") + 1
	word := "lines"
	if n == 1 {
		word = "line"
	}
	verb, one, other := "replaced", "{path} replaced ({n} line)", "{path} replaced ({n} lines)"
	if created {
		verb, one, other = "created", "{path} created ({n} line)", "{path} created ({n} lines)"
	}
	res := assistant.OK(fmt.Sprintf("%s %s (%d %s).", rel, verb, n, word), assistant.Tn(n, one, other, map[string]any{"path": rel}))
	res.Diff = diff
	return res, nil
}

// ---------- skills ----------

func (s *A) loadSkill(rt *runtime.Runtime, name string) (assistant.ToolResult, error) {
	if name == "" {
		return assistant.ToolResult{}, assistant.Usagef("name is missing")
	}
	sk, err := s.LLM.ReadSkill(llm.Project{Root: rt.Root, FS: rt.FS, IdeDir: rt.IdeDir}, name)
	if err != nil {
		return assistant.ToolResult{}, err
	}
	text, _ := sk["content"].(string)
	if files, _ := sk["files"].([]string); len(files) > 0 {
		text += "\n\n---\nOther files of the skill (read_skill_file):"
		for _, f := range files {
			text += "\n- " + f
		}
	}
	return assistant.OK(text, assistant.T("skill {name} loaded", map[string]any{"name": name})), nil
}

func (s *A) readSkillFile(rt *runtime.Runtime, name, file string) (assistant.ToolResult, error) {
	if name == "" || file == "" {
		return assistant.ToolResult{}, assistant.Usagef("name and file are required")
	}
	text, err := s.LLM.ReadSkillFile(llm.Project{Root: rt.Root, FS: rt.FS, IdeDir: rt.IdeDir}, name, file)
	if err != nil {
		return assistant.ToolResult{}, err
	}
	return assistant.OKPlain(text, name+"/"+file), nil
}

// ---------- shell and consoles ----------

// gitDefaults runs before a command of the model: ticket commits start with "#<n>", so git
// must not take "#" for the comment character (a rebase --continue would strip the subject).
const gitDefaults = "export GIT_CONFIG_COUNT=1 GIT_CONFIG_KEY_0=core.commentChar GIT_CONFIG_VALUE_0=auto\n"

func bashTool(ctx context.Context, rt *runtime.Runtime, command, cwd string, timeout int, progress func(string), k *runtime.Kept) (assistant.ToolResult, error) {
	if strings.TrimSpace(command) == "" {
		return assistant.ToolResult{}, assistant.Usagef("command is missing")
	}
	if cwd != "" {
		cwd = assistant.AbsPath(rt.Root, cwd)
	}
	if timeout <= 0 {
		timeout = 120
	}
	res, err := rt.RunShellIn(ctx, gitDefaults+command, cwd, timeout, progress, k)
	if err != nil {
		return assistant.ToolResult{}, err
	}
	return shellToolResult(res), nil
}

// shellToolResult is the result of the bash tool for the model.
func shellToolResult(res runtime.ShellResult) assistant.ToolResult {
	secs := fmt.Sprintf("%.1f", float64(res.DurationMs)/1000)
	out := assistant.TrimEnd(assistant.PlainOutput(res.Output))
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
	tr := assistant.ToolResult{Content: state + "\n" + out, Status: "ok", Term: assistant.TrimEnd(assistant.ColorOutput(res.Output))}
	switch {
	case res.TimedOut:
		tr.Summary = assistant.T("timeout", nil).Raw()
	case res.Canceled:
		tr.Summary = assistant.T("canceled", nil).Raw()
	default:
		tr.Summary = assistant.Plain(fmt.Sprintf("code %d · %s s", res.Code, secs))
		tr.Exit = &res.Code
	}
	if res.Code != 0 || res.TimedOut || res.Canceled {
		tr.Status, tr.Failure = "error", "exit"
	}
	return tr
}

func (s *A) runCommand(r *assistantRun, rt *runtime.Runtime, command, cwd string, timeout int, progress func(string)) (assistant.ToolResult, error) {
	if strings.TrimSpace(command) == "" {
		return assistant.ToolResult{}, assistant.Usagef("command is missing")
	}
	if timeout <= 0 {
		timeout = 20
	}
	limit := time.Duration(min(max(timeout, 1), 600)) * time.Second
	info, err := s.startCommand(rt, r.project, command, cwd)
	if err != nil {
		return assistant.ToolResult{}, err
	}
	// The console comes to the front in the windows of the project (if any).
	go s.uiTool(r, "focus", assistant.Args{"target": json.RawMessage(`"console"`), "console_id": jsonString(info.ID), "quiet": json.RawMessage("true")})
	until := time.Now().Add(limit)
	if rt.Keeper != nil {
		s.toolRunning(r, func(run *engine.Running) { run.Console, run.Until = info.ID, until.UnixMilli() })
	}
	return s.waitCommand(r, rt, command, info.ID, until, progress)
}

// waitCommand follows the console of run_command until the command ends or its deadline.
func (s *A) waitCommand(r *assistantRun, rt *runtime.Runtime, command, console string, until time.Time, progress func(string)) (assistant.ToolResult, error) {
	text, now, err := assistant.FollowConsole(r.ctx, rt, console, until, progress)
	if err != nil {
		return assistant.ToolResult{}, err
	}
	return assistant.ConsoleResult(command, console, text, now), nil
}

// startCommand runs a command in a new console of a project, shown in its windows.
func (s *A) startCommand(rt *runtime.Runtime, project, command, cwd string) (console.Info, error) {
	title := command
	if r := []rune(title); len(r) > 60 {
		title = string(r[:57]) + "…"
	}
	if cwd != "" {
		cwd = assistant.AbsPath(rt.Root, cwd)
	}
	info, err := rt.Consoles.Create("task", title, []string{"sh", "-c", command}, cwd, 160, 40)
	if err != nil {
		return info, err
	}
	s.pod.Emit(project, "console.created", info)
	return info, nil
}

func jsonString(s string) json.RawMessage {
	data, _ := json.Marshal(s)
	return data
}
