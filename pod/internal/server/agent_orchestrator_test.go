package server

import (
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/DrSmithFr/web-ide/pod/internal/llm"
)

func TestOrchestrator(t *testing.T) {
	s, ts := newServer(t)
	model := &fakeModel{}
	mts := model.serve(t)
	if err := s.LLM.SaveServer(llm.Server{ID: "s1", Kind: "llamacpp", URL: mts.URL, Parallel: 4}, false); err != nil {
		t.Fatal(err)
	}
	a, _ := dial(t, ts, "secret-token-0123456789abcdef0123")
	id := a.call("projects.create", map[string]any{"type": "local", "path": t.TempDir()})["result"].(map[string]any)["id"].(string)
	a.call("project.open", map[string]any{"id": id})
	for _, title := range []string{"Small one", "Urgent one"} {
		a.call("kanban.create", map[string]any{"title": title})
	}
	a.call("kanban.update", map[string]any{"id": 2, "patch": map[string]any{"priority": "high"}})
	for i := 1; i <= 2; i++ {
		a.call("kanban.plan", map[string]any{"id": i, "plan": "p", "goals": []string{"g"}})
	}
	open := func(chat string) map[string]any {
		return a.call("agent.open", map[string]any{"id": chat})["result"].(map[string]any)["chat"].(map[string]any)
	}
	var mu sync.Mutex
	tools := map[string]map[string]bool{}
	var nextOut string
	model.answer = func(req map[string]any) []string {
		last := lastMessage(req)
		content := fmt.Sprint(last["content"])
		sys := req["messages"].([]any)[0].(map[string]any)["content"].(string)
		mu.Lock()
		defer mu.Unlock()
		switch {
		case strings.Contains(sys, "**Orchestrator mode**"):
			tools["orchestrator"] = toolNames(req)
			switch {
			case content == "What next?":
				return toolCalls([3]string{"n", "kanban_next", `{}`})
			case strings.HasPrefix(content, "Can start now"):
				nextOut = content
				return toolCalls([3]string{"c", "action_card", `{"kind":"start_dev","ticket":2,"label":"Start #2","reason":"high priority"}`})
			case strings.HasPrefix(content, "I have an idea"):
				return toolCalls([3]string{"o", "open_conversation", `{"mode":"briefing","message":"Idea: dark mode","send":true}`})
			case content == "Dig":
				return toolCalls([3]string{"d", "spawn_agent", `{"title":"Level 1","task":"Research and delegate"}`})
			}
			return text("ok")
		case strings.Contains(sys, "# You are a sub-agent"):
			task := fmt.Sprint(req["messages"].([]any)[1].(map[string]any)["content"])
			if strings.Contains(task, "Research and delegate") {
				tools["level1"] = toolNames(req)
				switch {
				case last["role"] == "user" && !strings.Contains(content, "report:"):
					return toolCalls([3]string{"e", "spawn_agent", `{"title":"Level 2","task":"Look deeper"}`})
				case strings.Contains(content, "report: done]"):
					return toolCalls([3]string{"f", "agent_report", `{"summary":"Level 1 done with the help of level 2."}`})
				}
				return text("waiting")
			}
			tools["level2"] = toolNames(req)
			if last["role"] == "user" {
				return toolCalls([3]string{"g", "spawn_agent", `{"title":"Level 3","task":"x"}`})
			}
			return toolCalls([3]string{"h", "agent_report", `{"summary":"Level 2 done."}`})
		}
		// The briefing opened for the idea.
		return text("Let's clarify the dark mode.")
	}
	send := func(chat, msg string) {
		a.call("agent.send", map[string]any{"id": chat, "text": msg, "server": "s1", "model": "m", "mode": "orchestrator"})
	}

	// What next: the high priority ticket first, a card to start it.
	send("o1", "What next?")
	a.waitUpdate("o1", idle)
	ms := open("o1")["messages"].([]any)
	card := ms[len(ms)-2].(map[string]any)["card"].(map[string]any)
	if !strings.Contains(nextOut, "- #2 [To do] (High) Urgent one\n- #1") || card["kind"] != "start_dev" || card["ticket"] != float64(2) {
		t.Fatalf("next: %q / card %+v", nextOut, card)
	}
	mu.Lock()
	ot := tools["orchestrator"]
	mu.Unlock()
	if !ot["edit_file"] || ot["kanban_create"] || ot["exit_plan_mode"] || !ot["action_card"] || !ot["kanban_history"] || !ot["spawn_agent"] {
		t.Fatalf("orchestrator tools: %v", ot)
	}
	// The card records what the click did.
	a.call("agent.card", map[string]any{"id": "o1", "index": len(ms) - 2, "state": "done", "result": "started"})
	ms = open("o1")["messages"].([]any)
	if c := ms[len(ms)-2].(map[string]any)["card"].(map[string]any); c["state"] != "done" {
		t.Fatalf("card state: %+v", c)
	}

	// An idea: a Briefing conversation opened for the user, the idea sent.
	a.call("agent.watch", map[string]any{"id": "o2"})
	send("o2", "I have an idea: a dark mode")
	ev := a.waitEvent("agent.open", nil)
	opened := ev["chat"].(string)
	if ev["from"] != "o2" {
		t.Fatalf("agent.open: %+v", ev)
	}
	a.waitUpdate(opened, idle)
	b := open(opened)
	bm := b["messages"].([]any)
	if b["mode"] != "briefing" || bm[0].(map[string]any)["content"] != "Idea: dark mode" || bm[1].(map[string]any)["content"] != "Let's clarify the dark mode." {
		t.Fatalf("briefing: %+v", b)
	}

	// Grandchildren: under an Orchestrator a child may delegate once more; level 3 is refused.
	send("o3", "Dig")
	deadline := time.Now().Add(15 * time.Second)
	for {
		ms := open("o3")["messages"].([]any)
		if strings.Contains(fmt.Sprint(ms[len(ms)-1].(map[string]any)["content"]), "ok") && strings.Contains(fmt.Sprint(ms), "Level 1 done with the help of level 2.") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("no report of level 1: %v", ms)
		}
		time.Sleep(100 * time.Millisecond)
	}
	mu.Lock()
	l1, l2 := tools["level1"], tools["level2"]
	mu.Unlock()
	if !l1["spawn_agent"] || !l1["agent_report"] || l2["spawn_agent"] || !l2["agent_report"] || l1["action_card"] {
		t.Fatalf("tools: level 1 %v, level 2 %v", l1, l2)
	}
}
