package server

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWorktrees(t *testing.T) {
	for k, v := range map[string]string{"GIT_AUTHOR_NAME": "t", "GIT_AUTHOR_EMAIL": "t@x", "GIT_COMMITTER_NAME": "t", "GIT_COMMITTER_EMAIL": "t@x"} {
		t.Setenv(k, v)
	}
	_, ts := newServer(t)
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
}
