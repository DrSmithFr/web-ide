package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DrSmithFr/web-ide/pod/internal/llm"
)

type mcpClient struct {
	t     *testing.T
	url   string
	token string
	n     int
}

func (m *mcpClient) post(body any) (*http.Response, map[string]any) {
	m.t.Helper()
	data, _ := json.Marshal(body)
	req, _ := http.NewRequest(http.MethodPost, m.url+"/mcp", bytes.NewReader(data))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	if m.token != "" {
		req.Header.Set("Authorization", "Bearer "+m.token)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		m.t.Fatal(err)
	}
	defer res.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(res.Body).Decode(&out)
	return res, out
}

func (m *mcpClient) rpc(method string, params any) map[string]any {
	m.t.Helper()
	m.n++
	_, out := m.post(map[string]any{"jsonrpc": "2.0", "id": m.n, "method": method, "params": params})
	if out["error"] != nil {
		m.t.Fatalf("%s: %v", method, out["error"])
	}
	return out["result"].(map[string]any)
}

// tool calls a tool and returns its text; isError tells whether the tool failed.
func (m *mcpClient) tool(name string, args map[string]any) (string, bool) {
	m.t.Helper()
	r := m.rpc("tools/call", map[string]any{"name": name, "arguments": args})
	text := r["content"].([]any)[0].(map[string]any)["text"].(string)
	return text, r["isError"] == true
}

func (m *mcpClient) ok(name string, args map[string]any) string {
	m.t.Helper()
	text, failed := m.tool(name, args)
	if failed {
		m.t.Fatalf("%s: %s", name, text)
	}
	return text
}

func TestMCPKanban(t *testing.T) {
	for k, v := range map[string]string{"GIT_AUTHOR_NAME": "t", "GIT_AUTHOR_EMAIL": "t@x", "GIT_COMMITTER_NAME": "t", "GIT_COMMITTER_EMAIL": "t@x", "GIT_CONFIG_GLOBAL": os.DevNull} {
		t.Setenv(k, v)
	}
	_, ts := newServer(t)
	a, _ := dial(t, ts, "secret-token-0123456789abcdef0123")
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("un\n"), 0o644)
	gitIn(t, dir, "init", "-q", "-b", "main")
	gitIn(t, dir, "add", "-A")
	gitIn(t, dir, "commit", "-q", "-m", "init")
	id := a.call("projects.create", map[string]any{"type": "local", "path": dir})["result"].(map[string]any)["id"].(string)
	a.call("project.open", map[string]any{"id": id})

	// Without the token, nothing.
	anon := &mcpClient{t: t, url: ts.URL}
	if res, _ := anon.post(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "initialize"}); res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("no token: %d", res.StatusCode)
	}
	m := &mcpClient{t: t, url: ts.URL, token: "secret-token-0123456789abcdef0123"}
	init := m.rpc("initialize", map[string]any{"protocolVersion": "2025-03-26", "capabilities": map[string]any{}, "clientInfo": map[string]any{"name": "test"}})
	if init["protocolVersion"] != "2025-03-26" || !strings.Contains(init["instructions"].(string), "/open?path=") {
		t.Fatalf("initialize: %v", init)
	}
	if res, _ := m.post(map[string]any{"jsonrpc": "2.0", "method": "notifications/initialized"}); res.StatusCode != http.StatusAccepted {
		t.Fatalf("notification: %d", res.StatusCode)
	}
	if tools := m.rpc("tools/list", nil)["tools"].([]any); len(tools) != len(mcpTools) {
		t.Fatalf("tools: %d", len(tools))
	}

	// A folder outside any project is refused; a subfolder of the project selects it.
	if text, failed := m.tool("kanban_list", map[string]any{"cwd": t.TempDir()}); !failed || !strings.Contains(text, "no project") {
		t.Fatalf("outside: %s", text)
	}
	os.Mkdir(filepath.Join(dir, "sub"), 0o755)
	cwd := filepath.Join(dir, "sub")
	if text := m.ok("kanban_create", map[string]any{"cwd": cwd, "title": "Export", "description": "CSV export"}); !strings.Contains(text, "#1") {
		t.Fatalf("create: %s", text)
	}
	a.waitEvent("kanban.changed", func(d map[string]any) bool { return d["id"] == float64(1) })
	if text, failed := m.tool("kanban_create", map[string]any{"cwd": cwd, "title": "x", "description": strings.Repeat("é", 1501)}); !failed || !strings.Contains(text, "too long") {
		t.Fatalf("long description: %s", text)
	}
	if text, failed := m.tool("kanban_move", map[string]any{"cwd": cwd, "id": 1, "status": "review", "test_summary": "t"}); !failed || !strings.Contains(text, "cannot move") {
		t.Fatalf("move from New: %s", text)
	}
	if text, failed := m.tool("kanban_set_plan", map[string]any{"cwd": cwd, "id": 1, "plan": "# Plan", "goals": []any{"g"}}); !failed || !strings.Contains(text, "size is required") {
		t.Fatalf("plan without a size: %s", text)
	}
	m.ok("kanban_set_plan", map[string]any{"cwd": cwd, "id": 1, "plan": "# Plan", "goals": []any{map[string]any{"title": "exported"}}, "size": "m"})
	m.ok("kanban_add_note", map[string]any{"cwd": cwd, "id": 1, "text": "comma separator"})
	md := m.ok("kanban_get", map[string]any{"cwd": cwd, "id": 1})
	if !strings.Contains(md, "Status: To do · priority: Normal · size: M") || !strings.Contains(md, "(Claude, ") || !strings.Contains(md, "- [ ] (id 1) exported") {
		t.Fatalf("get: %s", md)
	}
	tk := a.call("kanban.get", map[string]any{"id": 1})["result"].(map[string]any)
	notes := tk["notes"].([]any)
	if n := notes[len(notes)-1].(map[string]any); n["author"] != "claude" {
		t.Fatalf("author: %v", n)
	}

	// Start: branch and worktree; in the worktree the ticket is the default one.
	text := m.ok("kanban_start", map[string]any{"cwd": cwd, "id": 1})
	wt := filepath.Join(dir, ".ide", "worktrees", "1-export")
	if !strings.Contains(text, "Worktree: "+wt) || !strings.Contains(text, "In progress") {
		t.Fatalf("start: %s", text)
	}
	os.WriteFile(filepath.Join(wt, "export.go"), []byte("package x\n"), 0o644)
	gitIn(t, wt, "add", "-A")
	gitIn(t, wt, "commit", "-q", "-m", "#1 export")
	m.ok("kanban_goal", map[string]any{"cwd": wt, "action": "check", "goal": 1})
	m.ok("kanban_move", map[string]any{"cwd": wt, "status": "review", "test_summary": "open the export"})
	tk = a.call("kanban.get", map[string]any{"id": 1})["result"].(map[string]any)
	if tk["status"] != "review" || tk["goalsDone"] != float64(1) {
		t.Fatalf("after dev: %v %v", tk["status"], tk["goalsDone"])
	}
	// Claude edits and deletes goals, adds test feedback and marks it handled.
	m.ok("kanban_goal", map[string]any{"cwd": wt, "action": "add", "title": "extra"})
	m.ok("kanban_goal", map[string]any{"cwd": wt, "action": "edit", "goal": 1, "title": "exported as CSV"})
	m.ok("kanban_goal", map[string]any{"cwd": wt, "action": "delete", "goal": 2})
	if text := m.ok("kanban_feedback", map[string]any{"cwd": wt, "action": "add", "kind": "bug", "text": "no header row"}); !strings.Contains(text, "Feedback 1 added") {
		t.Fatalf("feedback add: %s", text)
	}
	if text, failed := m.tool("kanban_feedback", map[string]any{"cwd": wt, "action": "add", "kind": "nope", "text": "x"}); !failed || !strings.Contains(text, "unknown feedback kind") {
		t.Fatalf("feedback of an unknown kind: %s", text)
	}
	md = m.ok("kanban_get", map[string]any{"cwd": wt})
	if !strings.Contains(md, "- [x] (id 1) exported as CSV") || strings.Contains(md, "extra") || !strings.Contains(md, "(id 1, Bug, ") {
		t.Fatalf("goals and feedback of Claude: %s", md)
	}
	m.ok("kanban_feedback", map[string]any{"cwd": wt, "action": "done", "feedback": 1})

	// Lineage: a child waits for the step of its parent, then works in its worktree, where
	// it becomes the default ticket.
	text = m.ok("kanban_create", map[string]any{"cwd": cwd, "title": "Export step 2", "parent": 1})
	m.ok("kanban_set_plan", map[string]any{"cwd": cwd, "id": 2, "plan": "p", "goals": []any{"g"}, "size": "s"})
	if text := m.ok("kanban_list", map[string]any{"cwd": cwd}); !strings.Contains(text, "#2 [To do] (Normal) Export step 2 · size S · goals 0/1 · child of #1 · blocked by #1 (previous step not validated)") {
		t.Fatalf("list with a lineage: %s", text)
	}
	if text, failed := m.tool("kanban_start", map[string]any{"cwd": cwd, "id": 2}); !failed || !strings.Contains(text, "#1 (previous step not validated)") || !strings.Contains(text, "Only the user") {
		t.Fatalf("start of a blocked child: %s", text)
	}
	a.call("kanban.step", map[string]any{"id": 1})
	if text := m.ok("kanban_start", map[string]any{"cwd": cwd, "id": 2}); !strings.Contains(text, "Worktree: "+wt) || !strings.Contains(text, "step of the lineage of #1") {
		t.Fatalf("start of the child: %s", text)
	}
	if md := m.ok("kanban_get", map[string]any{"cwd": wt}); !strings.HasPrefix(md, "# Ticket #2") || !strings.Contains(md, "## Lineage\n- Child of #1") {
		t.Fatalf("default ticket of the lineage: %s", md)
	}

	// Prompts.
	if prompts := m.rpc("prompts/list", nil)["prompts"].([]any); len(prompts) != len(mcpRoles) {
		t.Fatalf("prompts: %v", prompts)
	}
	p := m.rpc("prompts/get", map[string]any{"name": "plan", "arguments": map[string]string{"ticket": "#1"}})
	msg := p["messages"].([]any)[0].(map[string]any)["content"].(map[string]any)["text"].(string)
	if !strings.Contains(msg, "ticket #1") || !strings.Contains(msg, "kanban_set_plan") {
		t.Fatalf("prompt: %s", msg)
	}
	promptText := func(p map[string]any) string {
		return p["messages"].([]any)[0].(map[string]any)["content"].(map[string]any)["text"].(string)
	}
	if msg := promptText(m.rpc("prompts/get", map[string]any{"name": "fix", "arguments": map[string]string{"ticket": "1"}})); !strings.Contains(msg, "Handle the open **test feedback** of ticket #1") {
		t.Fatalf("fix prompt: %s", msg)
	}
	if msg := promptText(m.rpc("prompts/get", map[string]any{"name": "fix", "arguments": map[string]string{"ticket": "1", "feedback": "3"}})); !strings.Contains(msg, "feedback** with id 3 (only this one) of ticket #1") {
		t.Fatalf("fix prompt of one feedback: %s", msg)
	}
}

func TestMCPConversation(t *testing.T) {
	raw := json.RawMessage(`{"title":"Export","messages":[
		{"role":"user","content":"I need an export","display":"I need an export"},
		{"role":"assistant","content":"","tool_calls":[{"id":"c1","function":{"name":"ask_user","arguments":"{\"questions\":[{\"question\":\"Format?\"}]}"}}]},
		{"role":"tool","tool_call_id":"c1","content":"Format?: CSV"},
		{"role":"assistant","content":[{"type":"text","text":"Noted: CSV."}]},
		{"role":"assistant","content":"","tool_calls":[{"id":"c2","function":{"name":"read_file","arguments":"{}"}}]},
		{"role":"tool","tool_call_id":"c2","content":"secret file content"}]}`)
	text := conversationText(raw, 0, false)
	for _, want := range []string{"# Conversation: Export", "## User\nI need an export", "Format?", "## Answers of the user\nFormat?: CSV", "Noted: CSV."} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q in:\n%s", want, text)
		}
	}
	if strings.Contains(text, "secret file content") || strings.Contains(text, "call read_file") {
		t.Error("other tool results are left out")
	}
	// For a review: the last messages, with the calls, their errors and a failed answer.
	raw = json.RawMessage(`{"title":"Dev","messages":[
		{"role":"user","content":"Develop"},
		{"role":"assistant","content":"","tool_calls":[{"id":"b1","function":{"name":"bash","arguments":"{\"command\":\"pkill -x web-ide-pod\"}"}}]},
		{"role":"tool","tool_call_id":"b1","name":"bash","status":"ok","content":"Exit code 0"},
		{"role":"assistant","content":"","tool_calls":[{"id":"b2","function":{"name":"bash","arguments":"{\"command\":\"make\"}"}}]},
		{"role":"tool","tool_call_id":"b2","name":"bash","status":"ok","content":"Interrupted: the pod stopped before this tool ended."},
		{"role":"assistant","content":"","error":"Interrupted: the pod stopped."}]}`)
	text = conversationText(raw, 5, true)
	for _, want := range []string{"(1 earlier messages left out)", "- call bash {\"command\":\"pkill -x web-ide-pod\"}", "→ ok: Interrupted: the pod stopped before", "## The answer failed\nInterrupted: the pod stopped."} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q in:\n%s", want, text)
		}
	}
	if strings.Contains(text, "## User\nDevelop") || strings.Contains(text, "Exit code 0") {
		t.Errorf("earlier messages or plain results kept:\n%s", text)
	}
}

func TestOpenLink(t *testing.T) {
	_, ts := newServer(t)
	a, _ := dial(t, ts, "secret-token-0123456789abcdef0123")
	dir := t.TempDir()
	file := filepath.Join(dir, "a.go")
	os.WriteFile(file, []byte("package a\n"), 0o644)
	id := a.call("projects.create", map[string]any{"type": "local", "path": dir})["result"].(map[string]any)["id"].(string)
	link := ts.URL + "/open?path=" + file + "&line=3"
	noRedirect := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

	// Without a token: the pairing page.
	if res, _ := noRedirect.Get(link); res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("anonymous: %d", res.StatusCode)
	}
	// No window on the project: the browser goes to the project page.
	req, _ := http.NewRequest(http.MethodGet, link, nil)
	req.Header.Set("Authorization", "Bearer secret-token-0123456789abcdef0123")
	res, _ := noRedirect.Do(req)
	if loc := res.Header.Get("Location"); res.StatusCode != http.StatusFound || !strings.HasPrefix(loc, "/project/"+id+"?open=") {
		t.Fatalf("no window: %d %s", res.StatusCode, loc)
	}
	// A window has it: it is asked to open the file.
	a.call("project.open", map[string]any{"id": id})
	req, _ = http.NewRequest(http.MethodPost, link, nil)
	req.Header.Set("Authorization", "Bearer secret-token-0123456789abcdef0123")
	res, _ = http.DefaultClient.Do(req)
	var out map[string]any
	json.NewDecoder(res.Body).Decode(&out)
	res.Body.Close()
	if out["opened"] != true {
		t.Fatalf("opened: %v", out)
	}
	a.waitEvent("ide.open", func(d map[string]any) bool { return d["path"] == file && d["line"] == float64(3) })
}

// A project opened through a symbolic link is found from the folder the link leads to;
// the links to the IDE use its public address.
func TestMCPSymlinkedProject(t *testing.T) {
	s, ts := newServer(t)
	s.Cfg.PublicURL = "https://ide.example.ts.net/"
	a, _ := dial(t, ts, "secret-token-0123456789abcdef0123")
	real := t.TempDir()
	link := filepath.Join(t.TempDir(), "apps")
	if err := os.Symlink(real, link); err != nil {
		t.Skip(err)
	}
	id := a.call("projects.create", map[string]any{"type": "local", "path": link})["result"].(map[string]any)["id"].(string)
	m := &mcpClient{t: t, url: ts.URL, token: "secret-token-0123456789abcdef0123"}
	if text := m.ok("kanban_list", map[string]any{"cwd": real}); !strings.Contains(text, "No ticket.") {
		t.Fatalf("list: %s", text)
	}
	// Links to the IDE use its public address.
	m.ok("kanban_create", map[string]any{"cwd": real, "title": "Export"})
	got := m.ok("kanban_get", map[string]any{"cwd": real, "id": 1})
	for _, want := range []string{"Ticket in the IDE: https://ide.example.ts.net/project/" + id + "?ticket=1", "Project in the IDE: https://ide.example.ts.net/project/" + id} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
}

// Claude Code answers the questions of a linked conversation, then writes in it.
func TestMCPReplyAndAnswer(t *testing.T) {
	s, ts := newServer(t)
	model := &fakeModel{}
	mts := model.serve(t)
	if err := s.LLM.SaveServer(llm.Server{ID: "s1", Kind: "llamacpp", URL: mts.URL}, false); err != nil {
		t.Fatal(err)
	}
	a, _ := dial(t, ts, "secret-token-0123456789abcdef0123")
	dir := t.TempDir()
	id := a.call("projects.create", map[string]any{"type": "local", "path": dir})["result"].(map[string]any)["id"].(string)
	a.call("project.open", map[string]any{"id": id})
	a.call("kanban.create", map[string]any{"title": "Export"})
	model.answer = func(req map[string]any) []string {
		last := lastMessage(req)
		if last["role"] == "user" && strings.Contains(last["content"].(string), "Export") {
			return toolCalls([3]string{"q1", "ask_user", `{"questions":[{"question":"Which format?","options":["CSV","JSON"]}]}`})
		}
		return text("OK: " + last["content"].(string))
	}
	a.call("agent.send", map[string]any{"id": "c1", "text": "Export", "server": "s1", "model": "m", "ticket": map[string]any{"id": 1, "role": "briefing"}})
	a.waitUpdate("c1", idle)

	m := &mcpClient{t: t, url: ts.URL, token: "secret-token-0123456789abcdef0123"}
	args := func(more map[string]any) map[string]any {
		out := map[string]any{"cwd": dir, "id": 1, "chat": "c1"}
		for k, v := range more {
			out[k] = v
		}
		return out
	}
	if text := m.ok("kanban_conversation", args(nil)); !strings.Contains(text, "Waiting for the answers") || !strings.Contains(text, "[options: CSV | JSON]") {
		t.Fatalf("pending questions: %s", text)
	}
	if text, failed := m.tool("kanban_answer", args(map[string]any{"answers": [][]string{{"CSV"}, {"x"}}})); !failed || !strings.Contains(text, "one answer per question") {
		t.Fatalf("wrong count: %s", text)
	}
	if text, failed := m.tool("kanban_reply", args(map[string]any{"chat": "nope", "message": "hi"})); !failed || !strings.Contains(text, "not linked") {
		t.Fatalf("unlinked: %s", text)
	}
	m.ok("kanban_answer", args(map[string]any{"answers": [][]string{{"JSON"}}}))
	a.waitUpdate("c1", idle)
	m.ok("kanban_reply", args(map[string]any{"message": "Keep the dates in ISO 8601."}))
	a.waitUpdate("c1", idle)

	chat := a.call("agent.open", map[string]any{"id": "c1"})["result"].(map[string]any)["chat"].(map[string]any)
	var ask, reply map[string]any
	for _, x := range chat["messages"].([]any) {
		msg := x.(map[string]any)
		if msg["askState"] == "answered" {
			ask = msg
		}
		if msg["role"] == "user" && msg["author"] == "claude" {
			reply = msg
		}
	}
	if ask == nil || ask["author"] != "claude" || !strings.Contains(ask["content"].(string), "→ JSON") {
		t.Fatalf("answers: %+v", ask)
	}
	if reply == nil || reply["display"] != "Keep the dates in ISO 8601." {
		t.Fatalf("reply: %+v", chat["messages"])
	}
	if text := m.ok("kanban_conversation", args(nil)); !strings.Contains(text, "## Claude Code (you)\nKeep the dates") || !strings.Contains(text, "OK: Keep the dates") {
		t.Fatalf("conversation: %s", text)
	}
}
