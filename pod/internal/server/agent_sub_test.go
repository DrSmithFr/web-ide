package server

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/DrSmithFr/web-ide/pod/internal/llm"
)

// isChild tells a request of a sub-agent (its system prompt) from one of its parent.
func isChild(req map[string]any) bool {
	sys := req["messages"].([]any)[0].(map[string]any)["content"].(string)
	return strings.Contains(sys, "# You are a sub-agent")
}

func toolNames(req map[string]any) map[string]bool {
	out := map[string]bool{}
	tools, _ := req["tools"].([]any)
	for _, t := range tools {
		out[t.(map[string]any)["function"].(map[string]any)["name"].(string)] = true
	}
	return out
}

var childID = regexp.MustCompile(`Sub-agent (\w+) \(`)

func TestSubAgents(t *testing.T) {
	s, ts := newServer(t)
	model := &fakeModel{}
	mts := model.serve(t)
	// Several conversations at once: a child blocked on the model does not hold its parent.
	if err := s.LLM.SaveServer(llm.Server{ID: "s1", Kind: "llamacpp", URL: mts.URL, Parallel: 4}, false); err != nil {
		t.Fatal(err)
	}
	a, _ := dial(t, ts, "secret-token-0123456789abcdef0123")
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("un\n"), 0o644)
	id := a.call("projects.create", map[string]any{"type": "local", "path": dir})["result"].(map[string]any)["id"].(string)
	a.call("project.open", map[string]any{"id": id})
	open := func(chat string) map[string]any {
		return a.call("agent.open", map[string]any{"id": chat})["result"].(map[string]any)["chat"].(map[string]any)
	}
	contents := func(chat string) string {
		var out []string
		for _, m := range open(chat)["messages"].([]any) {
			mm := m.(map[string]any)
			out = append(out, fmt.Sprintf("%v/%v:%v", mm["role"], mm["kind"], mm["content"]))
		}
		return strings.Join(out, "\n")
	}
	var mu sync.Mutex
	var childTools, parentTools map[string]bool
	// Parent: spawns, then answers the question of the child, then relays its report.
	// Child: a note and a question, then its report once answered.
	model.answer = func(req map[string]any) []string {
		last := lastMessage(req)
		content := fmt.Sprint(last["content"])
		mu.Lock()
		defer mu.Unlock()
		if isChild(req) {
			childTools = toolNames(req)
			switch {
			case last["role"] == "user":
				return toolCalls([3]string{"n1", "agent_note", `{"title":"Read a.txt","text":"It says un."}`}, [3]string{"k1", "agent_ask", `{"question":"Which word instead?"}`})
			case strings.Contains(content, "Answer of your parent: deux"):
				return toolCalls([3]string{"p1", "agent_report", `{"summary":"Replaced by deux.","files_changed":["a.txt"],"status":"done"}`})
			}
			return text("unexpected")
		}
		parentTools = toolNames(req)
		switch {
		case content == "Delegate":
			return toolCalls([3]string{"s1", "spawn_agent", `{"title":"Change the word","task":"Change the word of a.txt","files":["a.txt"]}`})
		case strings.Contains(content, "asks] Which word instead?"):
			child := childID.FindStringSubmatch(content)[1]
			return toolCalls([3]string{"r1", "agent_reply", fmt.Sprintf(`{"child":%q,"answer":"deux"}`, child)})
		case strings.Contains(content, "report: done]"):
			return text("The sub-agent replaced the word.")
		}
		return text("Waiting.")
	}
	a.call("agent.send", map[string]any{"id": "p1", "text": "Delegate", "server": "s1", "model": "m"})
	deadline := time.Now().Add(15 * time.Second)
	for !strings.Contains(contents("p1"), "The sub-agent replaced the word.") {
		if time.Now().After(deadline) {
			t.Fatalf("parent never got the report:\n%s", contents("p1"))
		}
		time.Sleep(100 * time.Millisecond)
	}
	parent := open("p1")
	children := parent["children"].([]any)
	if len(children) != 1 {
		t.Fatalf("children: %v", children)
	}
	cid := children[0].(string)
	pc := contents("p1")
	for _, want := range []string{"note] Read a.txt", "asks] Which word instead?", "report: done]\nReplaced by deux.\nFiles changed: a.txt"} {
		if !strings.Contains(pc, want) {
			t.Fatalf("parent lacks %q:\n%s", want, pc)
		}
	}
	// The child: its task, fresh context, ended with its report.
	for time.Now().Before(deadline) && s.run(cid) != nil {
		time.Sleep(50 * time.Millisecond)
	}
	child := open(cid)
	sa := child["agent"].(map[string]any)
	cc := contents(cid)
	if child["parent"] != "p1" || sa["status"] != "done" || sa["report"] != "Replaced by deux." || !strings.HasPrefix(cc, "user/agent_task:Task given by the parent conversation:\n\nChange the word of a.txt\n\nFiles to read first: a.txt") || strings.Contains(cc, "Delegate") {
		t.Fatalf("child: %+v\n%s", sa, cc)
	}
	mu.Lock()
	if childTools["spawn_agent"] || childTools["ask_user"] || !childTools["agent_report"] || !parentTools["spawn_agent"] || parentTools["agent_report"] {
		t.Fatalf("tools: child %v, parent %v", childTools, parentTools)
	}
	mu.Unlock()
	// The list knows the parent of the child.
	found := false
	for _, c := range a.call("llm.chats.list", nil)["result"].([]any) {
		if c := c.(map[string]any); c["id"] == cid {
			found = c["parent"] == "p1" && c["status"] == "done"
		}
	}
	if !found {
		t.Fatal("child not listed with its parent")
	}

	// A child answering without tools is told once to report, then its answer is its report;
	// agent_stop stops another one.
	model.answer = func(req map[string]any) []string {
		last := lastMessage(req)
		content := fmt.Sprint(last["content"])
		if isChild(req) {
			return text("All good.")
		}
		switch {
		case content == "Two":
			return toolCalls([3]string{"s2", "spawn_agent", `{"title":"Chatty","task":"Say hi"}`})
		case strings.Contains(content, "report: done]\nAll good."):
			return text("Got it.")
		}
		return text("Waiting.")
	}
	a.call("agent.send", map[string]any{"id": "p2", "text": "Two", "server": "s1", "model": "m"})
	for !strings.Contains(contents("p2"), "Got it.") {
		if time.Now().After(deadline.Add(10 * time.Second)) {
			t.Fatalf("no implicit report:\n%s", contents("p2"))
		}
		time.Sleep(100 * time.Millisecond)
	}
	c2 := open("p2")["children"].([]any)[0].(string)
	if cc := contents(c2); !strings.Contains(cc, "user/agent_nudge:You are a sub-agent: end with agent_report") {
		t.Fatalf("no nudge:\n%s", cc)
	}

	// agent_stop: the child ends as stopped, its parent is not woken; stopped by the user
	// from its thread, the parent is told.
	block := make(chan struct{})
	defer close(block)
	model.answer = func(req map[string]any) []string {
		content := fmt.Sprint(lastMessage(req)["content"])
		if isChild(req) {
			select {
			case <-block:
			case <-time.After(20 * time.Second):
			}
			return text("late")
		}
		switch {
		case content == "Slow ones":
			return toolCalls([3]string{"s3", "spawn_agent", `{"title":"Slow A","task":"Wait"}`}, [3]string{"s4", "spawn_agent", `{"title":"Slow B","task":"Wait"}`})
		case strings.HasPrefix(content, "Stop "):
			return toolCalls([3]string{"x1", "agent_stop", fmt.Sprintf(`{"child":%q}`, strings.TrimPrefix(content, "Stop "))})
		}
		return text("Fine.")
	}
	a.call("agent.send", map[string]any{"id": "p3", "text": "Slow ones", "server": "s1", "model": "m"})
	a.waitUpdate("p3", idle)
	kids := open("p3")["children"].([]any)
	ca, cb := kids[0].(string), kids[1].(string)
	a.call("agent.send", map[string]any{"id": "p3", "text": "Stop " + ca, "server": "s1", "model": "m"})
	a.waitUpdate("p3", idle)
	for time.Now().Before(deadline.Add(20*time.Second)) && s.run(ca) != nil {
		time.Sleep(50 * time.Millisecond)
	}
	if st := open(ca)["agent"].(map[string]any)["status"]; st != "stopped" {
		t.Fatalf("stopped by the parent: %v", st)
	}
	for time.Now().Before(deadline.Add(20*time.Second)) && s.run(cb) == nil {
		time.Sleep(50 * time.Millisecond)
	}
	a.call("agent.stop", map[string]any{"id": cb})
	for !strings.Contains(contents("p3"), "report: stopped]\nStopped by the user.") {
		if time.Now().After(deadline.Add(20 * time.Second)) {
			t.Fatalf("parent not told of the stop:\n%s", contents("p3"))
		}
		time.Sleep(100 * time.Millisecond)
	}
	if strings.Count(contents("p3"), "report: stopped]") != 1 {
		t.Fatalf("the stop of the parent was reported to it:\n%s", contents("p3"))
	}
}

// A child on a cloud server chosen by its parent: the server listed in the parent prompt, its
// usage summed, an unknown model refused, an error of the provider reported to the parent.
func TestSubAgentServers(t *testing.T) {
	s, ts := newServer(t)
	local, cloud := &fakeModel{}, &fakeModel{}
	lts, cts := local.serve(t), cloud.serve(t)
	if err := s.LLM.SaveServer(llm.Server{ID: "s1", Kind: "llamacpp", URL: lts.URL, Parallel: 4}, false); err != nil {
		t.Fatal(err)
	}
	// The fake answers /v1/models with 404: the typed models are the list.
	if err := s.LLM.SaveServer(llm.Server{ID: "c1", Name: "Cloud", Kind: "openai", URL: cts.URL, APIKey: "sk", Parallel: 4, Children: true, Note: "strong at code, paid",
		Models: []llm.ModelConf{{ID: "big", Tools: true}, {ID: "broke", Tools: true}}}, false); err != nil {
		t.Fatal(err)
	}
	a, _ := dial(t, ts, "secret-token-0123456789abcdef0123")
	id := a.call("projects.create", map[string]any{"type": "local", "path": t.TempDir()})["result"].(map[string]any)["id"].(string)
	a.call("project.open", map[string]any{"id": id})
	contents := func(chat string) string {
		var out []string
		for _, m := range a.call("agent.open", map[string]any{"id": chat})["result"].(map[string]any)["chat"].(map[string]any)["messages"].([]any) {
			mm := m.(map[string]any)
			out = append(out, fmt.Sprintf("%v:%v", mm["role"], mm["content"]))
		}
		return strings.Join(out, "\n")
	}
	var system string
	local.answer = func(req map[string]any) []string {
		content := fmt.Sprint(lastMessage(req)["content"])
		switch content {
		case "Go":
			system = req["messages"].([]any)[0].(map[string]any)["content"].(string)
			return toolCalls([3]string{"x", "spawn_agent", `{"title":"Nope","task":"t","server":"Cloud","model":"missing"}`}, [3]string{"y", "spawn_agent", `{"title":"Cloudy","task":"t","server":"Cloud","model":"big"}`})
		case "Again":
			return toolCalls([3]string{"z", "spawn_agent", `{"title":"Poor","task":"t","server":"Cloud","model":"broke"}`})
		}
		return text("ok")
	}
	cloud.answer = func(req map[string]any) []string {
		if req["model"] == "broke" {
			return []string{`{"error":{"message":"Insufficient credits"}}`}
		}
		return append(toolCalls([3]string{"r", "agent_report", `{"summary":"Done in the cloud."}`})[:1],
			`{"choices":[{"finish_reason":"tool_calls","delta":{}}],"usage":{"prompt_tokens":100,"completion_tokens":20,"cost":0.5}}`)
	}
	a.call("agent.send", map[string]any{"id": "p", "text": "Go", "server": "s1", "model": "m"})
	deadline := time.Now().Add(15 * time.Second)
	for !strings.Contains(contents("p"), "Done in the cloud.") {
		if time.Now().After(deadline) {
			t.Fatalf("no report:\n%s", contents("p"))
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !strings.Contains(system, "- Cloud, models: big, broke — strong at code, paid (4 at once)") {
		t.Fatalf("servers not in the parent prompt:\n%s", system)
	}
	pc := contents("p")
	if !strings.Contains(pc, `Cloud has no model "missing" (models: big, broke)`) || !strings.Contains(pc, "started on Cloud (big): Cloudy") {
		t.Fatalf("parent:\n%s", pc)
	}
	kids := a.call("agent.open", map[string]any{"id": "p"})["result"].(map[string]any)["chat"].(map[string]any)["children"].([]any)
	child := a.call("agent.open", map[string]any{"id": kids[0]})["result"].(map[string]any)["chat"].(map[string]any)
	if sa := child["agent"].(map[string]any); child["server"] != "c1" || child["model"] != "big" || sa["tokens"] != float64(120) || sa["cost"] != 0.5 {
		t.Fatalf("child: %v %v %+v", child["server"], child["model"], child["agent"])
	}

	// The provider fails: the child ends in error, its parent is told.
	a.call("agent.send", map[string]any{"id": "p", "text": "Again", "server": "s1", "model": "m"})
	for !strings.Contains(contents("p"), "report: error]") {
		if time.Now().After(deadline.Add(10 * time.Second)) {
			t.Fatalf("no error report:\n%s", contents("p"))
		}
		time.Sleep(100 * time.Millisecond)
	}
	if pc := contents("p"); !strings.Contains(pc, "Insufficient credits") {
		t.Fatalf("error report:\n%s", pc)
	}
}
