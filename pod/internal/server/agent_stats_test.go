package server

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/DrSmithFr/web-ide/pod/internal/agent"
	"github.com/DrSmithFr/web-ide/pod/internal/llm"
)

// A run records when each message came, the approval waits and the kind of the failures;
// agent.stats counts them, with the sub-agents, and for the whole project.
func TestAgentStats(t *testing.T) {
	s, ts := newServer(t)
	model := &fakeModel{}
	mts := model.serve(t)
	if err := s.LLM.SaveServer(llm.Server{ID: "s1", Kind: "llamacpp", URL: mts.URL}, false); err != nil {
		t.Fatal(err)
	}
	a, _ := dial(t, ts, "secret-token-0123456789abcdef0123")
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("un\n"), 0o644)
	id := a.call("projects.create", map[string]any{"type": "local", "path": dir})["result"].(map[string]any)["id"].(string)
	a.call("project.open", map[string]any{"id": id})

	step := 0
	model.answer = func(req map[string]any) []string {
		step++
		switch step {
		case 1: // a forgotten parameter, and an edit
			return toolCalls([3]string{"r1", "read_file", `{}`}, [3]string{"e1", "edit_file", `{"path":"a.txt","old_string":"un","new_string":"deux"}`})
		case 2:
			return toolCalls([3]string{"b1", "bash", `{"command":"exit 3"}`})
		}
		return text("Done.")
	}
	before := time.Now().UnixMilli()
	a.call("agent.send", map[string]any{"id": "st", "text": "Go", "server": "s1", "model": "m"})
	u := a.waitUpdate("st", func(u map[string]any) bool { return u["approval"] != nil })
	time.Sleep(60 * time.Millisecond) // the user reads the change
	a.call("agent.approve", map[string]any{"id": "st", "approval": u["approval"].(map[string]any)["id"], "allow": true})
	a.waitUpdate("st", idle)

	ms := a.call("agent.open", map[string]any{"id": "st"})["result"].(map[string]any)["chat"].(map[string]any)["messages"].([]any)
	for _, m := range ms {
		if at, _ := m.(map[string]any)["at"].(float64); int64(at) < before {
			t.Fatalf("message without its time: %+v", m)
		}
	}

	// A sub-agent of the conversation, and another conversation of the project.
	loc, _, _ := s.chatLoc(id)
	kid := &agent.Chat{ID: "kid", Title: "Kid", Parent: "st", Messages: []*agent.Message{
		{Role: "user", Content: agent.String("task")},
		{Role: "assistant", Model: "other", ToolCalls: []agent.ToolCall{{ID: "k1"}}},
		{Role: "tool", Name: "read_file", Status: "denied"},
	}}
	kid.Messages[1].ToolCalls[0].Function.Name = "read_file"
	if err := s.saveChat(loc, kid); err != nil {
		t.Fatal(err)
	}
	if r := s.run("st"); r != nil {
		t.Fatal("still running")
	}
	st, _ := s.loadChat(loc, "st")
	st.Children = []string{"kid"}
	if err := s.saveChat(loc, st); err != nil {
		t.Fatal(err)
	}

	var res struct {
		Stats    statsJSON `json:"stats"`
		Own      statsJSON `json:"own"`
		Children []struct {
			ID    string    `json:"id"`
			Stats statsJSON `json:"stats"`
		} `json:"children"`
	}
	decode(t, a.call("agent.stats", map[string]any{"id": "st"})["result"], &res)
	own := res.Own
	if own.Answers != 3 || own.ToolCalls != 3 || own.Failures["usage"] != 1 || own.Failures["exit"] != 1 || own.WithTools != 2 || own.Multi != 1 {
		t.Fatalf("own: %+v", own)
	}
	if edit := own.tool("edit_file"); edit.WaitMs < 60 || edit.Calls != 1 {
		t.Fatalf("approval wait: %+v", own.Tools)
	}
	if len(res.Children) != 1 || res.Children[0].ID != "kid" || res.Children[0].Stats.Failures["denied"] != 1 {
		t.Fatalf("children: %+v", res.Children)
	}
	if res.Stats.Answers != 4 || res.Stats.ToolCalls != 4 || res.Stats.Failures["denied"] != 1 || res.Stats.Context == nil {
		t.Fatalf("total: %+v", res.Stats)
	}

	var project struct {
		Stats  statsJSON `json:"stats"`
		Models []string  `json:"models"`
	}
	decode(t, a.call("agent.stats", map[string]any{"project": true, "model": "other"})["result"], &project)
	if project.Stats.Conversations != 2 || project.Stats.Answers != 1 || project.Stats.Context != nil || len(project.Models) != 2 {
		t.Fatalf("project: %+v %v", project.Stats, project.Models)
	}
}

type statsJSON struct {
	Answers   int            `json:"answers"`
	ToolCalls int            `json:"toolCalls"`
	WithTools int            `json:"withTools"`
	Multi     int            `json:"multi"`
	Failures  map[string]int `json:"failures"`
	Tools     []struct {
		Name   string `json:"name"`
		Calls  int    `json:"calls"`
		WaitMs int64  `json:"waitMs"`
	} `json:"tools"`
	Conversations int             `json:"conversations"`
	Context       json.RawMessage `json:"context"`
}

func (s statsJSON) tool(name string) (t struct {
	Name   string `json:"name"`
	Calls  int    `json:"calls"`
	WaitMs int64  `json:"waitMs"`
}) {
	for _, x := range s.Tools {
		if x.Name == name {
			return x
		}
	}
	return
}

func decode(t *testing.T, v any, out any) {
	t.Helper()
	data, _ := json.Marshal(v)
	if err := json.Unmarshal(data, out); err != nil {
		t.Fatal(err)
	}
}
