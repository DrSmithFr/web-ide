package server

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"antennae.space/ide/plugins/ai/llm"
)

// The AGENTS.md of a folder reaches the model with the first read there, once, and comes
// back in the system prompt after a compaction.
func TestAssistantNestedInstructions(t *testing.T) {
	s, ts := newServer(t)
	model := &fakeModel{}
	mts := model.serve(t)
	if err := llmOf(s).SaveServer(llm.Server{ID: "s1", Kind: "llamacpp", URL: mts.URL}, false); err != nil {
		t.Fatal(err)
	}
	a, _ := dial(t, ts, "secret-token-0123456789abcdef0123")
	dir := t.TempDir()
	sub := filepath.Join(dir, "pkg", "sub")
	_ = os.MkdirAll(sub, 0o755)
	_ = os.WriteFile(filepath.Join(dir, "pkg", "CLAUDE.md"), []byte("Rule of pkg."), 0o644)
	_ = os.WriteFile(filepath.Join(sub, "AGENTS.md"), []byte("Rule of sub."), 0o644)
	_ = os.WriteFile(filepath.Join(sub, "x.txt"), []byte("x\n"), 0o644)
	id := a.call("projects.create", map[string]any{"type": "local", "path": dir})["result"].(map[string]any)["id"].(string)
	a.call("project.open", map[string]any{"id": id})
	open := func() map[string]any {
		return a.call("assistant.open", map[string]any{"id": "n1"})["result"].(map[string]any)["chat"].(map[string]any)
	}
	model.answer = func(req map[string]any) []string {
		if sys := req["messages"].([]any)[0].(map[string]any)["content"].(string); strings.HasPrefix(sys, "You summarize") {
			return text("SUMMARY")
		}
		if m := lastMessage(req); m["role"] == "user" && m["content"] == "Read" {
			return toolCalls([3]string{"r1", "read_file", `{"path":"pkg/sub/x.txt"}`}, [3]string{"r2", "read_file", `{"path":"pkg/sub/x.txt"}`})
		}
		return text("Done.")
	}
	a.call("assistant.send", map[string]any{"id": "n1", "text": "Read", "server": "s1", "model": "m"})
	a.waitUpdate("n1", idle)
	msgs := open()["messages"].([]any)
	first, second := msgs[2].(map[string]any)["content"].(string), msgs[3].(map[string]any)["content"].(string)
	if !strings.Contains(first, `<instructions path="pkg/CLAUDE.md">`) || !strings.Contains(first, "Rule of pkg.") ||
		!strings.Contains(first, `<instructions path="pkg/sub/AGENTS.md">`) || !strings.Contains(first, "Rule of sub.") ||
		strings.Contains(second, "<instructions") {
		t.Fatalf("tool results: %q / %q", first, second)
	}
	nested, _ := open()["nested"].([]any)
	if len(nested) != 2 || nested[0].(map[string]any)["path"] != "pkg/CLAUDE.md" {
		t.Fatalf("nested kept with the conversation: %v", nested)
	}

	// A compaction drops the tool results: the system prompt carries the instructions.
	system := func() string {
		model.mu.Lock()
		defer model.mu.Unlock()
		for i := len(model.requests) - 1; i >= 0; i-- {
			if sys := model.requests[i]["messages"].([]any)[0].(map[string]any)["content"].(string); !strings.HasPrefix(sys, "You summarize") {
				return sys
			}
		}
		return ""
	}
	// A compaction keeps the last steps: they have to be many.
	for i := 0; i < 12; i++ {
		a.call("assistant.send", map[string]any{"id": "n1", "text": "Again", "server": "s1", "model": "m"})
		a.waitUpdate("n1", idle)
	}
	if strings.Contains(system(), "Rule of sub.") {
		t.Fatal("instructions in the prompt before any compaction")
	}
	a.call("assistant.compact", map[string]any{"id": "n1"})
	a.waitUpdate("n1", func(u map[string]any) bool { return u["resetAt"] != nil })
	a.call("assistant.send", map[string]any{"id": "n1", "text": "More", "server": "s1", "model": "m"})
	a.waitUpdate("n1", func(u map[string]any) bool { return idle(u) && u["count"].(float64) > u["resetAt"].(float64)+1 })
	got := system()
	if !strings.Contains(got, "Rule of sub.") || !strings.Contains(got, "pkg/CLAUDE.md") {
		t.Fatalf("system prompt after the compaction: %.300q", got)
	}
}
