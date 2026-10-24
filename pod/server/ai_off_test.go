package server

import (
	"strings"
	"testing"

	"antennae.space/ide/plugins/ai/engine"
	"antennae.space/ide/pod/assistant"
)

// The AI plugin turned off on a project: the Questions tool leaves its questions out, the
// Kanban keeps working without development agent nor times, and the agents are refused.
func TestAIOff(t *testing.T) {
	s, ts := newServer(t)
	a, _ := dial(t, ts, s.Token)
	dir := t.TempDir()
	id := a.call("projects.create", map[string]any{"type": "local", "path": dir})["result"].(map[string]any)["id"].(string)
	a.call("project.open", map[string]any{"id": id})

	loc, root, _ := aiOf(s).ChatLoc(id)
	chat := &engine.Chat{ID: "c1", Title: "Ask", Messages: []*engine.Message{
		{Role: "user", Content: assistant.String("Ask me")},
		{Role: "tool", ToolCallID: "x", Name: "ask_user", AskState: "pending", At: 5, Questions: []assistant.Question{{Question: "Which one?", Options: []assistant.Option{{Label: "A"}, {Label: "B"}}}}},
	}}
	if err := saveTestChat(s, loc, chat); err != nil {
		t.Fatal(err)
	}
	aiOf(s).NoteAsks(root, chat, 0)
	if l := a.call("questions.list", nil)["result"].([]any); len(l) != 1 {
		t.Fatalf("AI on: the question of the conversation is listed: %v", l)
	}

	a.call("plugins.set", map[string]any{"id": "ai", "enabled": false})
	if l := a.call("questions.list", nil)["result"].([]any); len(l) != 0 {
		t.Fatalf("AI off: its questions are left out: %v", l)
	}
	if e := a.callRaw("agents.list", nil)["error"]; e == nil {
		t.Fatal("AI off: the agents are refused")
	}
	a.call("kanban.create", map[string]any{"title": "Greet"})
	if r := a.call("kanban.get", map[string]any{"id": 1})["result"].(map[string]any); r["title"] != "Greet" {
		t.Fatalf("AI off: the kanban works: %v", r)
	}
	a.call("kanban.meta.set", map[string]any{"values": map[string]string{"devAgent": "claude"}})
	e := a.callRaw("kanban.agent", map[string]any{"id": 1, "role": "dev"})["error"]
	if e == nil || !strings.Contains(e.(map[string]any)["message"].(string), "AI plugin is disabled") {
		t.Fatalf("AI off: no development agent: %v", e)
	}
}
