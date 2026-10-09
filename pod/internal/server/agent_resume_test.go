package server

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/DrSmithFr/web-ide/pod/internal/agent"
	"github.com/DrSmithFr/web-ide/pod/internal/config"
	"github.com/DrSmithFr/web-ide/pod/internal/hfcache"
	"github.com/DrSmithFr/web-ide/pod/internal/keeper"
	"github.com/DrSmithFr/web-ide/pod/internal/llm"
	"github.com/DrSmithFr/web-ide/pod/internal/projects"
	"github.com/DrSmithFr/web-ide/pod/internal/sessions"
	"github.com/DrSmithFr/web-ide/pod/internal/settings"
	"github.com/DrSmithFr/web-ide/pod/internal/sshx"
	"github.com/DrSmithFr/web-ide/pod/internal/store"
)

// podAt is a pod on the data folder dir with the keeper at sock, as main starts it: the runs
// of the pod before it are taken back before it listens.
func podAt(t *testing.T, dir, sock string) (*Server, *httptest.Server) {
	t.Helper()
	st, _ := store.Open(dir)
	cfg, _ := config.Load(st)
	reg, _ := projects.Load(st)
	sets, _ := settings.Load(st)
	s := &Server{Cfg: cfg, Store: st, Token: "secret-token-0123456789abcdef0123", Projects: reg, Settings: sets,
		Sessions: sessions.New(st), LLM: llm.New(st), Models: hfcache.New(st.Path("models", "hf")), Pool: sshx.NewPool(sshx.NewHostKeys(st.Path("known_hosts"))),
		Static: fstest.MapFS{"index.html": {Data: []byte("<html>app</html>")}}}
	c, _, err := keeper.Dial(sock)
	if err != nil {
		t.Fatal(err)
	}
	s.Keeper, s.LLM.Relay = c, llm.KeeperRelay{C: c}
	s.Init()
	s.ResumeRuns()
	ts := httptest.NewServer(s)
	return s, ts
}

// stopPod stops a pod as SIGTERM does (its goroutines stay in the test, frozen).
func stopPod(s *Server, ts *httptest.Server) {
	ts.Close()
	s.Shutdown()
	s.Keeper.Close()
}

func keeperFor(t *testing.T) string {
	dir, _ := os.MkdirTemp("", "kr")
	t.Cleanup(func() { os.RemoveAll(dir) })
	sock := filepath.Join(dir, "k.sock")
	ln, err := keeper.Listen(sock)
	if err != nil {
		t.Fatal(err)
	}
	k := keeper.NewServer("test")
	go k.Serve(ln)
	t.Cleanup(k.Close)
	return sock
}

func chatOf(t *testing.T, s *Server, project, id string) []map[string]any {
	loc, _, _ := s.chatLoc(project)
	raw, err := s.LLM.GetChat(loc, id)
	if err != nil {
		return nil
	}
	var c struct{ Messages []map[string]any }
	_ = json.Unmarshal(raw, &c)
	return c.Messages
}

func waitFor(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatal("timeout: " + what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func lastContent(ms []map[string]any) string {
	if len(ms) == 0 {
		return ""
	}
	s, _ := ms[len(ms)-1]["content"].(string)
	return s
}

// The pod restarts while an answer streams, then while its command runs, then while an edit
// waits for its approval: each time the new pod takes the run back where it was, and the
// model is asked once per step.
func TestResumeRuns(t *testing.T) {
	sock := keeperFor(t)
	data := t.TempDir()
	ws := t.TempDir()
	os.WriteFile(filepath.Join(ws, "a.txt"), []byte("un\n"), 0o644)
	model := &fakeModel{models: []string{"m"}, delay: 30 * time.Millisecond}
	mts := model.serve(t)
	model.answer = func(req map[string]any) []string {
		msgs := req["messages"].([]any)
		results := 0
		for _, m := range msgs {
			if m.(map[string]any)["role"] == "tool" {
				results++
			}
		}
		switch {
		case lastMessage(req)["role"] == "user" && results == 0:
			var parts []string
			for i := 0; i < 20; i++ {
				parts = append(parts, `{"choices":[{"delta":{"content":"part`+string(rune('a'+i))+` "}}]}`)
			}
			return append(parts, toolCalls([3]string{"b1", "bash", `{"command":"sleep 1.5; echo done-bash"}`})...)
		case results == 1:
			return toolCalls([3]string{"e1", "edit_file", `{"path":"a.txt","old_string":"un","new_string":"deux"}`})
		}
		return text("finished")
	}
	requests := func() int { model.mu.Lock(); defer model.mu.Unlock(); return len(model.requests) }

	s1, ts1 := podAt(t, data, sock)
	if err := s1.LLM.SaveServer(llm.Server{ID: "s1", Kind: "llamacpp", URL: mts.URL}, false); err != nil {
		t.Fatal(err)
	}
	a, _ := dial(t, ts1, "secret-token-0123456789abcdef0123")
	id := a.call("projects.create", map[string]any{"type": "local", "path": ws})["result"].(map[string]any)["id"].(string)
	a.call("project.open", map[string]any{"id": id})
	a.call("agent.send", map[string]any{"id": "rs", "text": "Go", "server": "s1", "model": "m"})

	// 1. In the middle of the answer.
	time.Sleep(250 * time.Millisecond)
	stopPod(s1, ts1)
	s2, ts2 := podAt(t, data, sock)
	waitFor(t, "the answer and its command", func() bool {
		ms := chatOf(t, s2, id, "rs")
		return len(ms) >= 3 && ms[2]["role"] == "tool" && ms[2]["status"] != nil && ms[2]["status"] != ""
	})
	ms := chatOf(t, s2, id, "rs")
	if c, _ := ms[1]["content"].(string); !strings.HasPrefix(c, "parta ") || !strings.Contains(c, "partt ") || ms[1]["error"] != nil {
		t.Fatalf("the answer after the restart: %+v", ms[1])
	}
	if !strings.Contains(ms[2]["content"].(string), "done-bash") {
		t.Fatalf("the command: %+v", ms[2])
	}
	waitFor(t, "the next step", func() bool { return requests() >= 2 })
	if n := requests(); n != 2 { // the answer was not asked again
		t.Fatalf("%d requests after the first restart", n)
	}

	// 2. The edit waits for its approval: it is asked again by the next pod.
	waitFor(t, "the approval", func() bool {
		r := s2.run("rs")
		if r == nil {
			return false
		}
		r.mu.Lock()
		defer r.mu.Unlock()
		return r.chat.Approval != nil
	})
	stopPod(s2, ts2)
	s3, ts3 := podAt(t, data, sock)
	defer func() { ts3.Close(); s3.Shutdown() }()
	b, _ := dial(t, ts3, "secret-token-0123456789abcdef0123")
	b.call("project.open", map[string]any{"id": id})
	u := b.waitUpdate("rs", func(u map[string]any) bool { return u["approval"] != nil })
	b.call("agent.approve", map[string]any{"id": "rs", "approval": u["approval"].(map[string]any)["id"], "allow": true})
	waitFor(t, "the end", func() bool { return lastContent(chatOf(t, s3, id, "rs")) == "finished" })
	if got, _ := os.ReadFile(filepath.Join(ws, "a.txt")); string(got) != "deux\n" {
		t.Fatalf("file: %q", got)
	}
	ms = chatOf(t, s3, id, "rs")
	var roles []string
	for _, m := range ms {
		roles = append(roles, m["role"].(string))
	}
	if strings.Join(roles, ",") != "user,assistant,tool,assistant,tool,assistant" {
		t.Fatalf("messages: %v", roles)
	}
	if n := requests(); n != 3 { // one per answer: none asked again
		t.Fatalf("%d requests in all", n)
	}
}

// Without the stream in the relay (the keeper restarted), the run is not taken back: it is
// closed as interrupted when opened, as without keeper.
func TestResumeWithoutRelay(t *testing.T) {
	sock := keeperFor(t)
	data := t.TempDir()
	s1, ts1 := podAt(t, data, sock)
	a, _ := dial(t, ts1, "secret-token-0123456789abcdef0123")
	id := a.call("projects.create", map[string]any{"type": "local", "path": t.TempDir()})["result"].(map[string]any)["id"].(string)
	loc, root, _ := s1.chatLoc(id)
	if err := s1.saveChat(loc, &agent.Chat{ID: "gone", Server: "s1", Model: "m", Running: &agent.Running{Stream: "unknown"},
		Messages: []*agent.Message{{Role: "user", Content: agent.String("Go")}}}); err != nil {
		t.Fatal(err)
	}
	stopPod(s1, ts1)
	data2, _ := json.Marshal([]runningEntry{{ID: "gone", Loc: loc, Root: root, Project: id}})
	os.WriteFile(filepath.Join(data, "running.json"), data2, 0o600)

	s2, ts2 := podAt(t, data, sock)
	defer func() { ts2.Close(); s2.Shutdown() }()
	if s2.run("gone") != nil {
		t.Fatal("taken back without its stream")
	}
	b, _ := dial(t, ts2, "secret-token-0123456789abcdef0123")
	b.call("project.open", map[string]any{"id": id})
	ms := b.call("agent.open", map[string]any{"id": "gone"})["result"].(map[string]any)["chat"].(map[string]any)["messages"].([]any)
	if last := ms[len(ms)-1].(map[string]any); !strings.Contains(last["error"].(string), "Interrupted") {
		t.Fatalf("not interrupted: %+v", last)
	}
}
