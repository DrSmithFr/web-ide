package server

import (
	"testing"

	"github.com/DrSmithFr/web-ide/pod/internal/assistant"
	"github.com/DrSmithFr/web-ide/pod/internal/llm"
)

func TestEffortFor(t *testing.T) {
	user := &assistant.Message{Role: "user"}
	answer := &assistant.Message{Role: "assistant"}
	tool := func(name, status string) *assistant.Message {
		return &assistant.Message{Role: "tool", Name: name, Status: status}
	}
	set := func(level string) *assistant.Message {
		return &assistant.Message{Role: "tool", Name: "set_effort", Status: "ok", Effort: level}
	}
	dyn, withTool := assistant.Options{}, assistant.Options{Effort: "auto", EffortTool: true}
	cases := []struct {
		name string
		o    assistant.Options
		msgs []*assistant.Message
		want string
	}{
		{"a message of the user", dyn, []*assistant.Message{user}, "xhigh"},
		{"tool results", dyn, []*assistant.Message{user, answer, tool("read_file", "ok"), tool("bash", "ok")}, "medium"},
		{"a failed call", dyn, []*assistant.Message{user, answer, tool("read_file", "ok"), tool("bash", "error")}, "xhigh"},
		{"a refused change", dyn, []*assistant.Message{user, answer, tool("edit_file", "denied")}, "xhigh"},
		{"mechanical calls", dyn, []*assistant.Message{user, answer, tool("edit_file", "ok"), tool("write_file", "ok")}, "low"},
		{"an edit and a command", dyn, []*assistant.Message{user, answer, tool("edit_file", "ok"), tool("bash", "ok")}, "medium"},
		{"only the last step counts", dyn, []*assistant.Message{user, answer, tool("bash", "error"), answer, tool("read_file", "ok")}, "medium"},
		{"a summary is no message of the user", dyn, []*assistant.Message{user, answer, {Role: "user", Kind: "summary"}, answer, tool("edit_file", "ok")}, "low"},
		{"fixed by the user", assistant.Options{Effort: "low"}, []*assistant.Message{user}, "low"},
		{"chosen by the model", withTool, []*assistant.Message{user, answer, set("low"), answer, tool("bash", "error")}, "low"},
		{"the model's choice ends with the user", withTool, []*assistant.Message{set("low"), user}, "xhigh"},
		{"set_effort ignored when turned off", dyn, []*assistant.Message{user, answer, set("low"), tool("read_file", "ok")}, "medium"},
	}
	for _, c := range cases {
		if got := effortFor(&assistant.Chat{Messages: c.msgs}, c.o); got != c.want {
			t.Errorf("%s: %s, want %s", c.name, got, c.want)
		}
	}
	// A session of a ticket keeps the effort of its complexity, the dynamic one included.
	low := &assistant.Chat{Messages: []*assistant.Message{user}, Ticket: &assistant.TicketLink{ID: 1, Role: "dev", Effort: "low"}}
	if got := effortFor(low, dyn); got != "low" {
		t.Fatalf("effort of a low ticket: %s", got)
	}
	auto := &assistant.Chat{Messages: []*assistant.Message{user, answer, tool("read_file", "ok")}, Ticket: &assistant.TicketLink{ID: 1, Role: "dev", Effort: "auto"}}
	if got := effortFor(auto, assistant.Options{Effort: "low"}); got != "medium" {
		t.Fatalf("dynamic effort of a medium ticket: %s", got)
	}
}

func TestAssistantEffort(t *testing.T) {
	s, ts := newServer(t)
	model := &fakeModel{models: []string{"m"}}
	mts := model.serve(t)
	if err := s.LLM.SaveServer(llm.Server{ID: "effort", Kind: "llamacpp", URL: mts.URL}, false); err != nil {
		t.Fatal(err)
	}
	a, _ := dial(t, ts, "secret-token-0123456789abcdef0123")
	id := a.call("projects.create", map[string]any{"type": "local", "path": t.TempDir()})["result"].(map[string]any)["id"].(string)
	a.call("project.open", map[string]any{"id": id})
	sent := func(i int) (effort string, setEffort bool) {
		model.mu.Lock()
		defer model.mu.Unlock()
		req := model.requests[i]
		effort, _ = req["chat_template_kwargs"].(map[string]any)["reasoning_effort"].(string)
		tools, _ := req["tools"].([]any)
		for _, d := range tools {
			setEffort = setEffort || d.(map[string]any)["function"].(map[string]any)["name"] == "set_effort"
		}
		return
	}

	// Dynamic with the tool: the model lowers its effort for the next answer.
	model.answer = func(req map[string]any) []string {
		if lastMessage(req)["role"] == "user" {
			return toolCalls([3]string{"s1", "set_effort", `{"level":"low","reason":"simple"}`})
		}
		return text("Done.")
	}
	a.call("assistant.send", map[string]any{"id": "e1", "text": "Go", "server": "effort", "model": "m", "options": map[string]any{"effortTool": true}})
	a.waitUpdate("e1", idle)
	if e, tool := sent(0); e != "xhigh" || !tool {
		t.Fatalf("first answer: %s, set_effort offered %v", e, tool)
	}
	if e, _ := sent(1); e != "low" {
		t.Fatalf("after set_effort: %s", e)
	}
	ms := a.call("assistant.open", map[string]any{"id": "e1"})["result"].(map[string]any)["chat"].(map[string]any)["messages"].([]any)
	if last := ms[len(ms)-1].(map[string]any); last["effort"] != "low" {
		t.Fatalf("the answer keeps its effort: %+v", last)
	}

	// Fixed by the user: no tool.
	model.answer = func(map[string]any) []string { return text("OK.") }
	a.call("assistant.send", map[string]any{"id": "e2", "text": "Go", "server": "effort", "model": "m", "options": map[string]any{"effort": "medium", "effortTool": true}})
	a.waitUpdate("e2", idle)
	if e, tool := sent(2); e != "medium" || tool {
		t.Fatalf("fixed effort: %s, set_effort offered %v", e, tool)
	}
}
