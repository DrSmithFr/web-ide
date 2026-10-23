package server

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/DrSmithFr/web-ide/plugins/ai/acp"
	"github.com/DrSmithFr/web-ide/pod/projects"
)

// A ticket developed by an ACP agent: its session in the worktree, opened with the briefing
// and the MCP server of the IDE (the fake agent calls kanban_get through it), prompted again
// for a correction, ended with the worktree.
func TestKanbanDevAgent(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node is needed by the fake agent")
	}
	bin, _ := filepath.Abs("../../e2e/bin")
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	for k, v := range map[string]string{"GIT_AUTHOR_NAME": "t", "GIT_AUTHOR_EMAIL": "t@x", "GIT_COMMITTER_NAME": "t", "GIT_COMMITTER_EMAIL": "t@x", "GIT_CONFIG_GLOBAL": os.DevNull} {
		t.Setenv(k, v)
	}
	s, ts := newServer(t)
	s.MCPBase = ts.URL
	a, _ := dial(t, ts, "secret-token-0123456789abcdef0123")
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("a\n"), 0o644)
	gitIn(t, dir, "init", "-q", "-b", "main")
	gitIn(t, dir, "add", "-A")
	gitIn(t, dir, "commit", "-q", "-m", "init")
	id := a.call("projects.create", map[string]any{"type": "local", "path": dir})["result"].(map[string]any)["id"].(string)
	a.call("project.open", map[string]any{"id": id})
	a.call("kanban.create", map[string]any{"title": "Greet"})
	a.call("kanban.plan", map[string]any{"id": 1, "plan": "p", "goals": []string{"g"}})

	// The assistant develops by default.
	if r := a.callRaw("kanban.agent", map[string]any{"id": 1, "role": "dev"}); r["error"] == nil {
		t.Fatalf("no development agent set: %+v", r)
	}
	a.call("kanban.meta.set", map[string]any{"values": map[string]string{"devAgent": "claude"}})
	a.call("kanban.start", map[string]any{"id": 1})
	r := a.call("kanban.agent", map[string]any{"id": 1, "role": "dev", "text": "Develop it."})["result"].(map[string]any)
	child := projects.ChildID(id, 1)
	if r["project"] != child || r["session"] == "" {
		t.Fatalf("session: %+v", r)
	}
	agents := s.Store.Path("agents", child)
	thread := func() (acp.Info, []acp.Entry) {
		info, entries, _ := acp.Find(agents, 1)
		return info, entries
	}
	wait := func(what string, ok func(acp.Info, []acp.Entry) bool) {
		t.Helper()
		deadline := time.Now().Add(15 * time.Second)
		for {
			if info, e := thread(); ok(info, e) {
				return
			}
			if time.Now().After(deadline) {
				info, e := thread()
				t.Fatalf("timeout: %s: %+v %+v", what, info, e)
			}
			time.Sleep(50 * time.Millisecond)
		}
	}
	wait("briefing answered through the MCP server", func(info acp.Info, e []acp.Entry) bool {
		return len(e) >= 2 && e[0].Kind == "user" && strings.Contains(e[1].Text, "Briefing received (ticket #1). MCP web-ide: ") && strings.Contains(e[1].Text, "Greet") && info.State == acp.Ready
	})
	info, entries := thread()
	if info.Title != "#1 Greet" || info.Ticket != 1 || !strings.Contains(entries[0].Text, "You **develop** this ticket") || !strings.HasSuffix(entries[0].Text, "Develop it.") {
		t.Fatalf("briefing: %+v %q", info, entries[0].Text)
	}
	if f := a.call("kanban.agent.find", map[string]any{"id": 1})["result"].(map[string]any); f["session"] != info.ID || f["project"] != child {
		t.Fatalf("find: %+v", f)
	}
	// kanban_get points to it, kanban_conversation reads it.
	m := &mcpClient{t: t, url: ts.URL, token: "secret-token-0123456789abcdef0123"}
	if text := m.ok("kanban_get", map[string]any{"cwd": dir, "id": 1}); !strings.Contains(text, "chat="+info.ID) {
		t.Fatalf("kanban_get: %s", text)
	}
	if text := m.ok("kanban_conversation", map[string]any{"cwd": dir, "id": 1, "chat": info.ID}); !strings.Contains(text, "## Claude Code\nBriefing received") {
		t.Fatalf("kanban_conversation: %s", text)
	}

	// A correction goes to the same session.
	a.call("kanban.agent", map[string]any{"id": 1, "role": "correction", "text": "Fix it."})
	wait("correction prompted", func(info acp.Info, e []acp.Entry) bool {
		n := 0
		for _, x := range e {
			if x.Kind == "user" {
				n++
			}
		}
		return n == 2 && info.State == acp.Ready
	})

	// The worktree removed: the session ends.
	a.call("kanban.finish", map[string]any{"id": 1, "status": "abandoned", "comment": "test"})
	if _, err := os.Stat(agents); !os.IsNotExist(err) {
		t.Fatalf("the sessions of the worktree are removed: %v", err)
	}
	if r := a.call("kanban.agent.find", map[string]any{"id": 1})["result"].(map[string]any); len(r) != 0 {
		t.Fatalf("no session left: %+v", r)
	}
}
