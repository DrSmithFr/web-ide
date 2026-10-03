package server

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestKanbanRPC(t *testing.T) {
	_, ts := newServer(t)
	a, err := dial(t, ts, "secret-token-0123456789abcdef0123")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := dial(t, ts, "secret-token-0123456789abcdef0123")
	dir := t.TempDir()
	id := a.call("projects.create", map[string]any{"type": "local", "path": dir})["result"].(map[string]any)["id"].(string)
	a.call("project.open", map[string]any{"id": id})
	b.call("project.open", map[string]any{"id": id})

	tk := a.call("kanban.create", map[string]any{"title": "Export", "type": "bug", "addFiles": []string{"a.go"}})["result"].(map[string]any)
	if tk["id"] != float64(1) || tk["status"] != "new" || tk["type"] != "bug" {
		t.Fatalf("create: %+v", tk)
	}
	b.waitEvent("kanban.changed", func(d map[string]any) bool { return d["id"] == float64(1) && d["project"] == id })

	tk = a.call("kanban.plan", map[string]any{"id": 1, "by": "model", "plan": "# P", "goals": []string{"g1", "g2"}})["result"].(map[string]any)
	if tk["goals"] != float64(2) {
		t.Fatalf("plan: %+v", tk)
	}
	tk = a.call("kanban.move", map[string]any{"id": 1, "by": "model", "status": "ready"})["result"].(map[string]any)
	if tk["status"] != "ready" {
		t.Fatalf("move: %+v", tk)
	}
	tk = a.call("kanban.attachment.add", map[string]any{"id": 1, "name": "n.txt", "mime": "text/plain", "data": "aGVsbG8="})["result"].(map[string]any)
	aid := tk["attachments"].([]any)[0].(map[string]any)["id"]
	got := a.call("kanban.attachment.get", map[string]any{"id": 1, "aid": aid})["result"].(map[string]any)
	if got["data"] != "aGVsbG8=" {
		t.Fatalf("attachment: %+v", got)
	}
	list := a.call("kanban.list", nil)["result"].(map[string]any)
	if len(list["tickets"].([]any)) != 1 || list["project"] != id {
		t.Fatalf("list: %+v", list)
	}
	if _, err := os.Stat(filepath.Join(dir, ".ide", "kanban.db")); err != nil {
		t.Fatal(err)
	}
}

func gitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v %s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func TestKanbanWorktree(t *testing.T) {
	for k, v := range map[string]string{"GIT_AUTHOR_NAME": "t", "GIT_AUTHOR_EMAIL": "t@x", "GIT_COMMITTER_NAME": "t", "GIT_COMMITTER_EMAIL": "t@x"} {
		t.Setenv(k, v)
	}
	s, ts := newServer(t)
	a, _ := dial(t, ts, "secret-token-0123456789abcdef0123")
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("un\n"), 0o644)
	gitIn(t, dir, "init", "-q", "-b", "main")
	gitIn(t, dir, "add", "-A")
	gitIn(t, dir, "commit", "-q", "-m", "init")
	id := a.call("projects.create", map[string]any{"type": "local", "path": dir})["result"].(map[string]any)["id"].(string)
	a.call("project.open", map[string]any{"id": id})
	a.call("kanban.meta.set", map[string]any{"values": map[string]string{"setup": "echo prêt > setup.log"}})
	a.call("kanban.create", map[string]any{"title": "Été à l'export !"})
	a.call("kanban.plan", map[string]any{"id": 1, "plan": "p", "goals": []string{"g"}})
	a.call("kanban.move", map[string]any{"id": 1, "status": "ready"})

	r := a.call("kanban.start", map[string]any{"id": 1})["result"].(map[string]any)
	tk := r["ticket"].(map[string]any)
	wt := tk["worktree"].(string)
	if tk["branch"] != "ticket/1-ete-a-l-export" || tk["base"] != "main" || tk["status"] != "in_progress" || wt != filepath.Join(dir, ".ide", "worktrees", "1-ete-a-l-export") {
		t.Fatalf("start: %+v", tk)
	}
	child := r["project"].(string)
	if child != id+"-t1" {
		t.Fatalf("child: %s", child)
	}
	// The worktree project is hidden from the list and shares the kanban of its parent.
	for _, p := range a.call("projects.list", nil)["result"].([]any) {
		if p.(map[string]any)["id"] == child {
			t.Fatal("worktree project listed")
		}
	}
	if gitIn(t, dir, "status", "--porcelain") != "" {
		t.Fatalf("main folder not clean: %s", gitIn(t, dir, "status", "--porcelain"))
	}
	b, _ := dial(t, ts, "secret-token-0123456789abcdef0123")
	b.call("project.open", map[string]any{"id": child})
	if l := b.call("kanban.list", nil)["result"].(map[string]any); l["project"] != id {
		t.Fatalf("child kanban: %+v", l)
	}
	// Setup command run in the worktree.
	deadline := time.Now().Add(5 * time.Second)
	for {
		tk = a.call("kanban.get", map[string]any{"id": 1})["result"].(map[string]any)
		if tk["setup"] == "ok" || time.Now().After(deadline) {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if tk["setup"] != "ok" {
		t.Fatalf("setup: %+v", tk["setupLog"])
	}

	// Committed and uncommitted changes, untracked file.
	os.WriteFile(filepath.Join(wt, "a.txt"), []byte("un\ndeux\n"), 0o644)
	gitIn(t, wt, "commit", "-q", "-am", "#1 deux")
	os.WriteFile(filepath.Join(wt, "b.txt"), []byte("b\n"), 0o644)
	d := b.call("kanban.diff", map[string]any{"id": 1})["result"].(map[string]any)
	files := map[string]string{}
	for _, f := range d["files"].([]any) {
		f := f.(map[string]any)
		files[f["path"].(string)] = f["status"].(string)
	}
	if files["a.txt"] != "M" || files["b.txt"] != "?" || files["setup.log"] != "?" || d["ahead"] != float64(1) || d["dirty"] != true {
		t.Fatalf("diff: %+v", d)
	}
	patch := b.call("kanban.diff.file", map[string]any{"id": 1, "path": "a.txt", "from": d["from"]})["result"].(string)
	if !strings.Contains(patch, "+deux") {
		t.Fatalf("patch: %s", patch)
	}

	// Close: frozen change, worktree and its project removed, branch kept.
	a.call("kanban.move", map[string]any{"id": 1, "status": "review"})
	tk = a.call("kanban.finish", map[string]any{"id": 1, "status": "done"})["result"].(map[string]any)
	if tk["status"] != "done" || tk["worktree"] != nil || tk["snapshot"] == nil {
		t.Fatalf("finish: %+v", tk)
	}
	if p := tk["snapshot"].(map[string]any)["patch"].(string); !strings.Contains(p, "diff --git a/a.txt b/a.txt") || !strings.Contains(p, "+deux") {
		t.Fatal("snapshot without the patch")
	}
	if _, err := os.Stat(wt); !os.IsNotExist(err) {
		t.Fatal("worktree not removed")
	}
	if _, ok := s.Projects.Get(child); ok {
		t.Fatal("worktree project not removed")
	}
	if gitIn(t, dir, "branch", "--list", "ticket/1-ete-a-l-export") == "" {
		t.Fatal("branch deleted")
	}
	d = a.call("kanban.diff", map[string]any{"id": 1})["result"].(map[string]any)
	if d["source"] != "branch" || len(d["files"].([]any)) != 1 {
		t.Fatalf("diff after close: %+v", d)
	}
}
