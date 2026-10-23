package server

import (
	"testing"

	"github.com/DrSmithFr/web-ide/pod/llm"
)

func TestAssistantEffort(t *testing.T) {
	s, ts := newServer(t)
	model := &fakeModel{models: []string{"m"}}
	mts := model.serve(t)
	if err := llmOf(s).SaveServer(llm.Server{ID: "effort", Kind: "llamacpp", URL: mts.URL}, false); err != nil {
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
