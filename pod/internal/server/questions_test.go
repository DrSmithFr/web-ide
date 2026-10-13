package server

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/DrSmithFr/web-ide/pod/internal/agent"
)

// The Questions tool lists what waits for the user — a form of an ACP session, ask_user of a
// CLI agent through MCP, ask_user of the assistant — and answers it.
func TestQuestions(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node is needed by the fake agent")
	}
	bin, _ := filepath.Abs("../../../e2e/bin")
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	s, ts := newServer(t)
	a, _ := dial(t, ts, "secret-token-0123456789abcdef0123")
	dir := t.TempDir()
	id := a.call("projects.create", map[string]any{"type": "local", "path": dir})["result"].(map[string]any)["id"].(string)
	a.call("project.open", map[string]any{"id": id})
	list := func() []Question {
		var out []Question
		raw, _ := json.Marshal(a.call("questions.list", nil)["result"])
		_ = json.Unmarshal(raw, &out)
		return out
	}
	waitList := func(what string, ok func([]Question) bool) []Question {
		t.Helper()
		deadline := time.Now().Add(15 * time.Second)
		for {
			if l := list(); ok(l) {
				return l
			}
			if time.Now().After(deadline) {
				t.Fatalf("timeout: %s: %+v", what, list())
			}
			time.Sleep(50 * time.Millisecond)
		}
	}

	// An ACP session asks a form: listed with the message of the agent before it.
	sid := a.call("agents.create", map[string]any{"agent": "claude"})["result"].(map[string]any)["id"].(string)
	sess, _ := s.runtimes[id].Agents.Get(sid)
	waitList("session ready", func([]Question) bool { return sess.Info().State == "ready" })
	a.call("agents.prompt", map[string]any{"id": sid, "text": "ask me"})
	a.waitEvent("questions.changed", nil)
	l := waitList("the form", func(l []Question) bool { return len(l) == 1 })
	q := l[0]
	if q.Source != "acp" || q.Kind != "form" || q.Text != "Which color?" || q.Last != "I need to know your **favourite color**." || q.Project != id || q.Name != "Claude Code" || len(q.Schema) == 0 {
		t.Fatalf("form: %+v", q)
	}
	a.call("questions.answer", map[string]any{"id": q.ID, "action": "accept", "content": map[string]any{"question_0": "Red"}})
	waitList("answered", func(l []Question) bool { return len(l) == 0 && sess.Info().State == "ready" })
	if r := a.callRaw("questions.answer", map[string]any{"id": q.ID, "action": "accept"}); r["error"] == nil {
		t.Fatal("a question answered is closed")
	}

	// ask_user of a CLI agent through MCP: the call waits for the answer.
	m := &mcpClient{t: t, url: ts.URL, token: "secret-token-0123456789abcdef0123"}
	questions := []map[string]any{{"question": "Which database?", "options": []string{"SQLite", "Postgres"}}}
	got := make(chan string, 1)
	go func() {
		text, _ := m.tool("ask_user", map[string]any{"cwd": dir, "questions": questions, "context": "The tests need a database."})
		got <- text
	}()
	l = waitList("the MCP questions", func(l []Question) bool { return len(l) == 1 })
	if q = l[0]; q.Source != "mcp" || q.Kind != "ask" || q.Last != "The tests need a database." || len(q.Questions) != 1 || q.Questions[0].Options[1].Label != "Postgres" {
		t.Fatalf("mcp: %+v", q)
	}
	a.call("questions.answer", map[string]any{"id": q.ID, "answers": [][]string{{"Postgres"}}, "notes": []string{"the prod one"}})
	select {
	case text := <-got:
		if !strings.Contains(text, "Postgres") || !strings.Contains(text, "the prod one") {
			t.Fatalf("answers: %q", text)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the MCP call did not return")
	}
	if len(list()) != 0 {
		t.Fatal("answered questions leave the list")
	}

	// The user takes long: the call says so, and the agent waits again with the id.
	mcpAskWait = 200 * time.Millisecond
	defer func() { mcpAskWait = 4 * time.Minute }()
	text, _ := m.tool("ask_user", map[string]any{"cwd": dir, "questions": questions})
	if !strings.Contains(text, "has not answered yet") {
		t.Fatalf("timeout: %q", text)
	}
	q = list()[0]
	if !strings.Contains(text, q.ID) {
		t.Fatalf("the id to wait for: %q %s", text, q.ID)
	}
	a.call("questions.answer", map[string]any{"id": q.ID, "skip": true})
	if text, _ = m.tool("ask_user", map[string]any{"cwd": dir, "wait": q.ID}); !strings.Contains(text, "did not answer") {
		t.Fatalf("skipped, taken by the next call: %q", text)
	}
	if text, failed := m.tool("ask_user", map[string]any{"cwd": dir, "wait": q.ID}); !failed {
		t.Fatalf("taken once: %q", text)
	}

	// A conversation of the assistant asking.
	loc, root, _ := s.chatLoc(id)
	chat := &agent.Chat{ID: "c1", Title: "Plan the cache", Messages: []*agent.Message{
		{Role: "user", Content: agent.String("Plan it")},
		{Role: "assistant", Content: agent.String("Two ways to do it.")},
		{Role: "tool", ToolCallID: "x", Name: "ask_user", AskState: "pending", At: 5, Questions: []agent.Question{{Question: "Which one?", Options: []agent.Option{{Label: "A"}, {Label: "B"}}}}},
	}}
	if err := s.publishIdle(loc, root, chat, 0); err != nil {
		t.Fatal(err)
	}
	a.waitEvent("questions.changed", nil)
	l = list()
	if len(l) != 1 || l[0].Source != "assistant" || l[0].ID != "chat:c1:2" || l[0].Last != "Two ways to do it." || l[0].Title != "Plan the cache" {
		t.Fatalf("assistant: %+v", l)
	}
}
