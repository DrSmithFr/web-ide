package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/DrSmithFr/web-ide/pod/internal/llm"
)

// fakeModel is an OpenAI-compatible server whose answers are written by the test.
type fakeModel struct {
	mu       sync.Mutex
	requests []map[string]any
	answer   func(req map[string]any) []string
}

func (f *fakeModel) serve(t *testing.T) *httptest.Server {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/chat/completions":
			var req map[string]any
			_ = json.NewDecoder(r.Body).Decode(&req)
			f.mu.Lock()
			f.requests = append(f.requests, req)
			answer := f.answer
			f.mu.Unlock()
			w.Header().Set("Content-Type", "text/event-stream")
			for _, c := range answer(req) {
				fmt.Fprintf(w, "data: %s\n\n", c)
				w.(http.Flusher).Flush()
			}
			fmt.Fprint(w, "data: [DONE]\n\n")
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(ts.Close)
	return ts
}

func lastMessage(req map[string]any) map[string]any {
	msgs, _ := req["messages"].([]any)
	if len(msgs) == 0 {
		panic(fmt.Sprintf("request without messages: %v", req))
	}
	return msgs[len(msgs)-1].(map[string]any)
}

func toolCalls(calls ...[3]string) []string {
	var parts []string
	for i, c := range calls {
		parts = append(parts, fmt.Sprintf(`{"index":%d,"id":%q,"type":"function","function":{"name":%q,"arguments":%q}}`, i, c[0], c[1], c[2]))
	}
	return []string{`{"choices":[{"delta":{"tool_calls":[` + strings.Join(parts, ",") + `]}}]}`, `{"choices":[{"finish_reason":"tool_calls","delta":{}}]}`}
}

func text(s string) []string {
	data, _ := json.Marshal(s)
	return []string{`{"choices":[{"delta":{"content":` + string(data) + `}}]}`, `{"choices":[{"finish_reason":"stop","delta":{}}]}`}
}

// waitUpdate waits for an agent.update of a conversation matching pred.
func (w *wsClient) waitUpdate(id string, pred func(u map[string]any) bool) map[string]any {
	w.t.Helper()
	deadline := time.After(10 * time.Second)
	var seen []string
	for {
		select {
		case e := <-w.events:
			if e["event"] != "agent.update" {
				continue
			}
			d := e["data"].(map[string]any)
			seen = append(seen, fmt.Sprintf("%v:%v/%v/%v", d["id"], d["state"], d["from"], d["count"]))
			if d["id"] != id {
				continue
			}
			if pred(d) {
				return d
			}
		case <-deadline:
			w.t.Fatalf("no update of %s matching (seen %v)", id, seen)
		}
	}
}

func idle(u map[string]any) bool { return u["state"] == "idle" }

func TestAgentLoop(t *testing.T) {
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
	open := func(chat string) map[string]any {
		return a.call("agent.open", map[string]any{"id": chat})["result"].(map[string]any)["chat"].(map[string]any)
	}
	messages := func(chat string) []map[string]any {
		var out []map[string]any
		for _, m := range open(chat)["messages"].([]any) {
			out = append(out, m.(map[string]any))
		}
		return out
	}

	// Tools, then a change waiting for the user, then the answer.
	model.answer = func(req map[string]any) []string {
		if lastMessage(req)["role"] == "user" {
			return toolCalls([3]string{"r1", "read_file", `{"path":"a.txt"}`}, [3]string{"e1", "edit_file", `{"path":"a.txt","old_string":"un","new_string":"deux"}`})
		}
		return text("Done.")
	}
	a.call("agent.send", map[string]any{"id": "c1", "text": "Change a.txt", "server": "s1", "model": "m"})
	u := a.waitUpdate("c1", func(u map[string]any) bool { return u["approval"] != nil })
	ap := u["approval"].(map[string]any)
	if ap["kind"] != "edit" || !strings.HasSuffix(ap["path"].(string), "a.txt") || u["state"] != "waiting_user" {
		t.Fatalf("approval: %+v", u)
	}
	a.call("agent.approve", map[string]any{"id": "c1", "approval": ap["id"], "allow": true})
	a.waitUpdate("c1", idle)
	if got, _ := os.ReadFile(filepath.Join(dir, "a.txt")); string(got) != "deux\n" {
		t.Fatalf("file: %q", got)
	}
	ms := messages("c1")
	var roles []string
	for _, m := range ms {
		roles = append(roles, m["role"].(string))
	}
	if strings.Join(roles, ",") != "user,assistant,tool,tool,assistant" || ms[4]["content"] != "Done." || !strings.Contains(ms[2]["content"].(string), "1\tun") || ms[3]["status"] != "ok" {
		t.Fatalf("messages: %v / %+v", roles, ms)
	}
	model.mu.Lock()
	sys := model.requests[0]["messages"].([]any)[0].(map[string]any)["content"].(string)
	tools := len(model.requests[0]["tools"].([]any))
	second := lastMessage(model.requests[1])
	model.mu.Unlock()
	if !strings.Contains(sys, "Open project") || tools < 20 || second["role"] != "tool" {
		t.Fatalf("requests: system %.80q, %d tools, last %+v", sys, tools, second)
	}

	// Questions: the turn stops, the answers start it again.
	model.answer = func(req map[string]any) []string {
		last := lastMessage(req)
		if last["role"] == "user" {
			return toolCalls([3]string{"q1", "ask_user", `{"questions":[{"question":"Which format?","options":["CSV","JSON"]}]}`})
		}
		return text("OK: " + last["content"].(string))
	}
	a.call("agent.send", map[string]any{"id": "c2", "text": "Export", "server": "s1", "model": "m"})
	a.waitUpdate("c2", idle)
	ms = messages("c2")
	if q := ms[len(ms)-1]; q["askState"] != "pending" || len(q["questions"].([]any)) != 1 {
		t.Fatalf("questions: %+v", q)
	}
	a.call("agent.answer", map[string]any{"id": "c2", "index": len(ms) - 1, "answers": [][]string{{"JSON"}}})
	a.waitUpdate("c2", idle)
	ms = messages("c2")
	if last := ms[len(ms)-1]; !strings.Contains(last["content"].(string), "→ JSON") {
		t.Fatalf("after the answers: %+v", last)
	}

	// Plan mode: a command that may change something waits for the user; refused.
	model.answer = func(req map[string]any) []string {
		if lastMessage(req)["role"] == "user" {
			return toolCalls([3]string{"b1", "bash", `{"command":"rm a.txt"}`}, [3]string{"b2", "bash", `{"command":"ls"}`})
		}
		return text("Fine.")
	}
	a.call("agent.send", map[string]any{"id": "c3", "text": "Look", "server": "s1", "model": "m", "mode": "plan"})
	u = a.waitUpdate("c3", func(u map[string]any) bool { return u["approval"] != nil })
	a.call("agent.approve", map[string]any{"id": "c3", "approval": u["approval"].(map[string]any)["id"], "allow": false})
	a.waitUpdate("c3", idle)
	ms = messages("c3")
	if ms[2]["status"] != "denied" || ms[3]["status"] != "ok" || !strings.Contains(ms[3]["content"].(string), "a.txt") {
		t.Fatalf("plan mode commands: %+v / %+v", ms[2], ms[3])
	}
	if _, err := os.Stat(filepath.Join(dir, "a.txt")); err != nil {
		t.Fatal("the refused command ran")
	}

	// Ticket commits start with "#<n>": git run by the model does not take "#" for a comment.
	model.answer = func(req map[string]any) []string {
		if lastMessage(req)["role"] == "user" {
			return toolCalls([3]string{"g1", "bash", `{"command":"git config core.commentChar"}`})
		}
		return text("Fine.")
	}
	a.call("agent.send", map[string]any{"id": "c3g", "text": "Git", "server": "s1", "model": "m"})
	a.waitUpdate("c3g", idle)
	if ms = messages("c3g"); !strings.Contains(ms[2]["content"].(string), "auto") {
		t.Fatalf("comment character of git: %+v", ms[2])
	}

	// One conversation at a time on the server (Parallel 1): the second one is queued.
	release := make(chan struct{})
	model.answer = func(req map[string]any) []string {
		if strings.Contains(fmt.Sprint(lastMessage(req)["content"]), "slow") {
			<-release
		}
		return text("ok")
	}
	a.call("agent.send", map[string]any{"id": "c4", "text": "slow", "server": "s1", "model": "m"})
	a.waitUpdate("c4", func(u map[string]any) bool { return u["stream"] != nil && u["stream"] != "" })
	a.call("agent.send", map[string]any{"id": "c5", "text": "fast", "server": "s1", "model": "m"})
	a.waitUpdate("c5", func(u map[string]any) bool { return u["state"] == "queued" })
	// A message sent to a running conversation is queued, then sent at its next step.
	if r := a.call("agent.send", map[string]any{"id": "c4", "text": "and more", "server": "s1", "model": "m"})["result"].(map[string]any); r["queued"] != true {
		t.Fatalf("queued: %+v", r)
	}
	close(release)
	done := map[string]bool{}
	a.waitEvent("agent.update", func(d map[string]any) bool {
		if d["state"] == "idle" {
			done[d["id"].(string)] = true
		}
		return done["c4"] && done["c5"]
	})
	ms = messages("c4")
	if len(ms) != 4 || ms[2]["role"] != "user" || ms[3]["content"] != "ok" {
		t.Fatalf("queued message: %+v", ms)
	}

	// Stop: the run ends; nothing was written, nothing is added.
	block := make(chan struct{})
	model.answer = func(req map[string]any) []string {
		<-block
		return text("late")
	}
	a.call("agent.send", map[string]any{"id": "c6", "text": "wait", "server": "s1", "model": "m"})
	a.waitUpdate("c6", func(u map[string]any) bool { return u["stream"] != nil && u["stream"] != "" })
	a.call("agent.stop", map[string]any{"id": "c6"})
	a.waitUpdate("c6", idle)
	close(block)
	ms = messages("c6")
	if len(ms) != 1 || ms[0]["role"] != "user" {
		t.Fatalf("stopped: %+v", ms)
	}
}
