package server

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestKanbanLineage(t *testing.T) {
	for k, v := range map[string]string{"GIT_AUTHOR_NAME": "t", "GIT_AUTHOR_EMAIL": "t@x", "GIT_COMMITTER_NAME": "t", "GIT_COMMITTER_EMAIL": "t@x", "GIT_CONFIG_GLOBAL": os.DevNull} {
		t.Setenv(k, v)
	}
	_, ts := newServer(t)
	a, _ := dial(t, ts, "secret-token-0123456789abcdef0123")
	// A remote: tickets start from origin/main, merges go to the local main (never pushed).
	remote := t.TempDir()
	gitIn(t, remote, "init", "-q", "--bare", "-b", "main")
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("a\n"), 0o644)
	gitIn(t, dir, "init", "-q", "-b", "main")
	gitIn(t, dir, "add", "-A")
	gitIn(t, dir, "commit", "-q", "-m", "init")
	gitIn(t, dir, "remote", "add", "origin", remote)
	gitIn(t, dir, "push", "-q", "-u", "origin", "main")
	id := a.call("projects.create", map[string]any{"type": "local", "path": dir})["result"].(map[string]any)["id"].(string)
	a.call("project.open", map[string]any{"id": id})

	create := func(title string, extra map[string]any) {
		args := map[string]any{"title": title}
		for k, v := range extra {
			args[k] = v
		}
		n := a.call("kanban.create", args)["result"].(map[string]any)["id"]
		a.call("kanban.plan", map[string]any{"id": n, "plan": "p", "goals": []string{"g"}})
	}
	create("Root", nil)
	create("Step 1", map[string]any{"parent": 1})
	create("Step 2", map[string]any{"parent": 1})
	create("Other", map[string]any{"dependsOn": []int{2}})
	errCode := func(method string, args map[string]any) (string, string) {
		t.Helper()
		r := a.callRaw(method, args)
		e, _ := r["error"].(map[string]any)
		if e == nil {
			t.Fatalf("%s %v accepted: %+v", method, args, r)
		}
		code, _ := e["code"].(string)
		return code, e["message"].(string)
	}
	get := func(n int) map[string]any {
		return a.call("kanban.get", map[string]any{"id": n})["result"].(map[string]any)
	}
	blockers := func(n int) string {
		var out []string
		list, _ := get(n)["blockers"].([]any)
		for _, b := range list {
			m := b.(map[string]any)
			out = append(out, "#"+itoa(int64(m["id"].(float64)))+" "+m["kind"].(string))
		}
		return strings.Join(out, ", ")
	}
	if got := blockers(2); got != "#1 parent" {
		t.Fatalf("step 1 before the root starts: %s", got)
	}
	if code, _ := errCode("kanban.start", map[string]any{"id": 2}); code != "blocked" {
		t.Fatalf("start before the root: %s", code)
	}
	if code, _ := errCode("kanban.delete", map[string]any{"id": 1}); code == "blocked" {
		t.Fatal("delete a parent")
	}

	r := a.call("kanban.start", map[string]any{"id": 1})["result"].(map[string]any)
	wt := r["ticket"].(map[string]any)["worktree"].(string)
	rootProject := r["project"].(string)
	os.WriteFile(filepath.Join(wt, "root.txt"), []byte("root\n"), 0o644)
	gitIn(t, wt, "add", "-A")
	gitIn(t, wt, "commit", "-q", "-m", "#1 root")
	if got := blockers(2); got != "#1 previous" {
		t.Fatalf("step 1 before the validation: %s", got)
	}
	if _, msg := errCode("kanban.step", map[string]any{"id": 1}); !strings.Contains(msg, "To test") {
		t.Fatalf("validate in progress: %s", msg)
	}
	a.call("kanban.move", map[string]any{"id": 1, "status": "review"})
	a.call("kanban.step", map[string]any{"id": 1})

	// Step 1 works in the worktree of the root, from its last commit.
	head := gitIn(t, wt, "rev-parse", "HEAD")
	r = a.call("kanban.start", map[string]any{"id": 2})["result"].(map[string]any)
	tk := r["ticket"].(map[string]any)
	if r["project"] != rootProject || tk["worktree"] != wt || tk["branch"] != get(1)["branch"] || tk["base"] != head || tk["status"] != "in_progress" {
		t.Fatalf("start of step 1: %+v (project %v)", tk, r["project"])
	}
	if _, msg := errCode("kanban.move", map[string]any{"id": 1, "status": "in_progress"}); msg == "" {
		t.Fatal("root back to in progress")
	}
	os.WriteFile(filepath.Join(wt, "s1.txt"), []byte("s1\n"), 0o644)
	gitIn(t, wt, "add", "-A")
	gitIn(t, wt, "commit", "-q", "-m", "#2 s1")
	if code, _ := errCode("kanban.merge", map[string]any{"id": 2}); code == "blocked" {
		t.Fatal("merge of a child")
	}
	a.call("kanban.move", map[string]any{"id": 2, "status": "review"})
	a.call("kanban.finish", map[string]any{"id": 2, "status": "done"})
	if _, err := os.Stat(wt); err != nil {
		t.Fatal("the worktree went with the child")
	}
	d := a.call("kanban.diff", map[string]any{"id": 2})["result"].(map[string]any)
	if files := d["files"].([]any); len(files) != 1 || files[0].(map[string]any)["path"] != "s1.txt" || d["source"] != "snapshot" {
		t.Fatalf("change of step 1: %+v", d)
	}

	// The root waits for step 2; Other waits for the lineage of step 1 to be merged.
	if _, msg := errCode("kanban.merge", map[string]any{"id": 1}); !strings.Contains(msg, "#3") {
		t.Fatalf("merge before step 2: %s", msg)
	}
	if _, msg := errCode("kanban.finish", map[string]any{"id": 1, "status": "done"}); !strings.Contains(msg, "#3") {
		t.Fatalf("close before step 2: %s", msg)
	}
	if got := blockers(4); got != "#2 depends" {
		t.Fatalf("other: %s", got)
	}
	if code, _ := errCode("kanban.start", map[string]any{"id": 4}); code != "blocked" {
		t.Fatal("start of other")
	}
	a.call("kanban.start", map[string]any{"id": 3})
	a.call("kanban.move", map[string]any{"id": 3, "status": "review"})
	a.call("kanban.finish", map[string]any{"id": 3, "status": "done"})
	if st := a.call("kanban.merge", map[string]any{"id": 1})["result"].(map[string]any); st["merged"] != true {
		t.Fatalf("merge of the lineage: %+v", st)
	}

	// Merged into the local main only: Other starts from it, and says so.
	if got := blockers(4); got != "" {
		t.Fatalf("other after the merge: %s", got)
	}
	tk = a.call("kanban.start", map[string]any{"id": 4})["result"].(map[string]any)["ticket"].(map[string]any)
	if tk["base"] != "main" {
		t.Fatalf("base of other: %v", tk["base"])
	}
	if _, err := os.Stat(filepath.Join(tk["worktree"].(string), "s1.txt")); err != nil {
		t.Fatal("other does not hold the lineage it depends on")
	}

	// Forcing is the user's: a new dependent ticket of an unfinished lineage.
	create("Next", nil)
	create("Late", map[string]any{"dependsOn": []int{5}})
	if code, _ := errCode("kanban.start", map[string]any{"id": 6}); code != "blocked" {
		t.Fatal("start of late")
	}
	tk = a.call("kanban.start", map[string]any{"id": 6, "force": true})["result"].(map[string]any)["ticket"].(map[string]any)
	forced := false
	for _, n := range get(6)["notes"].([]any) {
		forced = forced || strings.Contains(n.(map[string]any)["text"].(string), `"Started despite: {blockers}","params":{"blockers":"#5 (dependency not merged)"}`)
	}
	if tk["status"] != "in_progress" || !forced {
		t.Fatalf("forced start: %+v", get(6)["notes"])
	}
}
