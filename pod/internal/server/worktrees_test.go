package server

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/DrSmithFr/web-ide/pod/internal/agent"
)

func TestWorktrees(t *testing.T) {
	for k, v := range map[string]string{"GIT_AUTHOR_NAME": "t", "GIT_AUTHOR_EMAIL": "t@x", "GIT_COMMITTER_NAME": "t", "GIT_COMMITTER_EMAIL": "t@x"} {
		t.Setenv(k, v)
	}
	srv, ts := newServer(t)
	a, _ := dial(t, ts, "secret-token-0123456789abcdef0123")
	dir := t.TempDir()
	id := a.call("projects.create", map[string]any{"type": "local", "path": dir})["result"].(map[string]any)["id"].(string)
	a.call("project.open", map[string]any{"id": id})
	gitIn(t, dir, "branch", "feature/x")
	// Uncommitted changes of the main folder do not prevent opening a branch.
	os.WriteFile(filepath.Join(dir, "wip.txt"), []byte("wip"), 0o644)

	child := a.call("worktrees.add", map[string]any{"branch": "feature/x"})["result"].(map[string]any)["project"].(string)
	wt := filepath.Join(dir, ".ide", "worktrees", "b-feature-x")
	if _, err := os.Stat(filepath.Join(wt, ".git")); err != nil {
		t.Fatal("worktree not created")
	}
	created := a.call("worktrees.add", map[string]any{"branch": "spike", "create": true})["result"].(map[string]any)["project"].(string)
	if created == child || gitIn(t, filepath.Join(dir, ".ide", "worktrees", "b-spike"), "branch", "--show-current") != "spike" {
		t.Fatal("new branch")
	}

	// From the worktree window: the same list, with the projects opened on them.
	b, _ := dial(t, ts, "secret-token-0123456789abcdef0123")
	b.call("project.open", map[string]any{"id": child})
	r := b.call("worktrees.list", nil)["result"].(map[string]any)
	items := r["items"].([]any)
	if r["root"] != id || len(items) != 3 {
		t.Fatalf("list = %+v", r)
	}
	main, first := items[0].(map[string]any), items[1].(map[string]any)
	if main["main"] != true || main["branch"] != "main" || main["project"] != id || first["branch"] != "feature/x" || first["project"] != child {
		t.Fatalf("items = %+v", items)
	}
	// The main window attaches the worktree: requests naming it run there, its events
	// reach the window tagged with it; another project cannot be attached.
	if r := a.callRawIn(child, "console.list", nil); r["error"] == nil {
		t.Fatal("request in a worktree not attached")
	}
	att := a.call("project.attach", map[string]any{"id": child})["result"].(map[string]any)
	if att["root"] != first["path"] {
		t.Fatalf("attach = %+v", att)
	}
	other := a.call("projects.create", map[string]any{"type": "local", "path": t.TempDir()})["result"].(map[string]any)["id"].(string)
	if r := a.callRaw("project.attach", map[string]any{"id": other}); r["error"] == nil {
		t.Fatal("another project attached")
	}
	a.callIn(child, "fs.write", map[string]any{"path": filepath.Join(first["path"].(string), "a.txt"), "content": "in the worktree"})
	if got, _ := os.ReadFile(filepath.Join(wt, "a.txt")); string(got) != "in the worktree" {
		t.Fatal("write in the worktree")
	}
	b.call("console.create", map[string]any{"kind": "terminal"})
	for deadline := time.After(5 * time.Second); ; {
		select {
		case e := <-a.events:
			if e["event"] != "console.created" {
				continue
			}
			if e["project"] != child {
				t.Fatalf("event = %+v", e)
			}
		case <-deadline:
			t.Fatal("event of the attached worktree not received")
		}
		break
	}

	// A ticket conversation works in its worktree whatever the window shows.
	if got := srv.runProject(&agent.Chat{Ticket: &agent.TicketLink{ID: 1, Project: created}}, id, id); got != created {
		t.Fatalf("run project = %s", got)
	}
	if got := srv.runProject(&agent.Chat{Ticket: &agent.TicketLink{ID: 1, Project: other}}, id, child); got != child {
		t.Fatalf("run project of another repository = %s", got)
	}

	// Opening it again gives the same project; it is not listed on the home page.
	if again := b.call("worktrees.open", map[string]any{"path": first["path"]})["result"].(map[string]any)["project"]; again != child {
		t.Fatalf("open = %v", again)
	}
	for _, p := range a.call("projects.list", nil)["result"].([]any) {
		if p.(map[string]any)["id"] == child {
			t.Fatal("worktree project listed")
		}
	}
	if got, _ := os.ReadFile(filepath.Join(dir, "wip.txt")); string(got) != "wip" {
		t.Fatal("changes of the main folder")
	}

	// Removal: refused with uncommitted changes unless forced; the branch stays.
	os.WriteFile(filepath.Join(wt, "draft.txt"), []byte("x"), 0o644)
	if r := b.callRaw("worktrees.remove", map[string]any{"path": first["path"]}); r["error"] == nil {
		t.Fatal("dirty worktree removed")
	}
	b.call("worktrees.remove", map[string]any{"path": first["path"], "force": true})
	if _, err := os.Stat(wt); !os.IsNotExist(err) {
		t.Fatal("worktree still there")
	}
	if _, ok := srv.Projects.Get(child); ok {
		t.Fatal("project of the worktree kept")
	}
	if gitIn(t, dir, "branch", "--list", "feature/x") == "" {
		t.Fatal("branch deleted")
	}
	// The setup command of the kanban comes with a new worktree.
	a.call("kanban.meta.set", map[string]any{"values": map[string]string{"setup": "npm install"}})
	if r := a.call("worktrees.add", map[string]any{"branch": "feature/x"})["result"].(map[string]any); r["setup"] != "npm install" {
		t.Fatalf("add = %+v", r)
	}
}
