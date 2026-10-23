package ai

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DrSmithFr/web-ide/plugins/ai/engine"
	"github.com/DrSmithFr/web-ide/plugins/ai/llm"
	"github.com/DrSmithFr/web-ide/pod/assistant"
)

// kanban_milestone in the assistant: any conversation reads and creates milestones; a
// briefing is linked to the ones it creates and may change them, others may not.
func TestAssistantMilestone(t *testing.T) {
	dir := t.TempDir()
	s, id := newTestA(t, dir)
	run := func(chat string) *assistantRun {
		return &assistantRun{id: chat, ctx: context.Background(), chat: &engine.Chat{ID: chat, Title: "Exports"}, project: id, root: id,
			loc: llm.ChatLocation{Project: id, IdeDir: filepath.Join(dir, ".ide")}}
	}
	call := func(r *assistantRun, mode, name, js string) (string, error) {
		var args assistant.Args
		if err := json.Unmarshal([]byte(js), &args); err != nil {
			t.Fatal(err)
		}
		res, err := s.kanbanCall(r, name, args, mode)
		return res.Content, err
	}
	brief, build := run("b1"), run("c1")
	out, err := call(brief, engine.Briefing, "kanban_milestone", `{"action":"create","title":"Exports","description":"CSV and JSON"}`)
	if err != nil || !strings.Contains(out, "Milestone 1 created") || !strings.Contains(out, "linked to it") {
		t.Fatalf("create: %v %s", err, out)
	}
	if _, err := call(brief, engine.Briefing, "kanban_milestone", `{"action":"story_add","id":1,"text":"As a user, I export my orders"}`); err != nil {
		t.Fatal(err)
	}
	if _, err := call(build, engine.Build, "kanban_milestone", `{"action":"story_add","id":1,"text":"x"}`); err == nil || !strings.Contains(err.Error(), "not linked") {
		t.Fatalf("a conversation changed a milestone it is not linked to: %v", err)
	}
	if out, err := call(build, engine.Build, "kanban_create", `{"title":"CSV export","milestone":1}`); err != nil || !strings.Contains(out, "#1") {
		t.Fatalf("ticket in a milestone: %v %s", err, out)
	}
	md, err := call(build, engine.Build, "kanban_milestone", `{"action":"get","id":1}`)
	if err != nil || !strings.Contains(md, "As a user, I export my orders") || !strings.Contains(md, "- #1 [New] CSV export") || !strings.Contains(md, "Briefing: Exports (chat b1)") {
		t.Fatalf("get: %v %s", err, md)
	}
}
