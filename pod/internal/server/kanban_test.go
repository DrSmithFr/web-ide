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

	tk := a.call("kanban.create", map[string]any{"title": "Export", "priority": "high", "addFiles": []string{"a.go"}})["result"].(map[string]any)
	if tk["id"] != float64(1) || tk["status"] != "new" || tk["priority"] != "high" {
		t.Fatalf("create: %+v", tk)
	}
	b.waitEvent("kanban.changed", func(d map[string]any) bool { return d["id"] == float64(1) && d["project"] == id })

	// Goals as titles, or titles with a description; the plan moves the ticket to To do.
	tk = a.call("kanban.plan", map[string]any{"id": 1, "by": "model", "plan": "# P", "goals": []any{"g1", map[string]string{"title": "g2", "description": "d2"}}})["result"].(map[string]any)
	if tk["goals"] != float64(2) || tk["status"] != "todo" || tk["goalList"].([]any)[1].(map[string]any)["description"] != "d2" {
		t.Fatalf("plan: %+v", tk)
	}
	tk = a.call("kanban.feedback", map[string]any{"id": 1, "feedback": map[string]any{"op": "add", "kind": "info", "text": "f"}})["result"].(map[string]any)
	if tk["feedbackOpen"] != float64(1) {
		t.Fatalf("feedback: %+v", tk)
	}
	// Errors are translated for the language of the window; history lines stay neutral.
	if r := a.callRaw("kanban.move", map[string]any{"id": 1, "by": "model", "status": "done"}); r["error"].(map[string]any)["message"] != "the model cannot move the ticket from “To do” to “Done”" {
		t.Fatalf("english error: %+v", r)
	}
	a.call("client.lang", map[string]any{"lang": "fr"})
	if r := a.callRaw("kanban.move", map[string]any{"id": 1, "by": "model", "status": "done"}); r["error"].(map[string]any)["message"] != "le modèle ne peut pas passer le ticket de « Todo » à « Terminé »" {
		t.Fatalf("french error: %+v", r)
	}
	notes := tk["notes"].([]any)
	if last := notes[len(notes)-1].(map[string]any)["text"]; last != `{"key":"{from} → {to}","params":{"from":"new","to":"todo"}}` {
		t.Fatalf("event: %v", last)
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
	a.call("kanban.meta.set", map[string]any{"values": map[string]string{"setup": "echo ready > setup.log"}})
	a.call("kanban.create", map[string]any{"title": "Café à l'export !"})
	a.call("kanban.plan", map[string]any{"id": 1, "plan": "p", "goals": []string{"g"}})

	r := a.call("kanban.start", map[string]any{"id": 1})["result"].(map[string]any)
	tk := r["ticket"].(map[string]any)
	wt := tk["worktree"].(string)
	if tk["branch"] != "ticket/1-cafe-a-l-export" || tk["base"] != "main" || tk["status"] != "in_progress" || wt != filepath.Join(dir, ".ide", "worktrees", "1-cafe-a-l-export") {
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
	if gitIn(t, dir, "branch", "--list", "ticket/1-cafe-a-l-export") == "" {
		t.Fatal("branch deleted")
	}
	d = a.call("kanban.diff", map[string]any{"id": 1})["result"].(map[string]any)
	if d["source"] != "snapshot" || len(d["files"].([]any)) != 3 {
		t.Fatalf("diff after close: %+v", d)
	}
}

func TestKanbanMergeRebase(t *testing.T) {
	for k, v := range map[string]string{"GIT_AUTHOR_NAME": "t", "GIT_AUTHOR_EMAIL": "t@x", "GIT_COMMITTER_NAME": "t", "GIT_COMMITTER_EMAIL": "t@x"} {
		t.Setenv(k, v)
	}
	_, ts := newServer(t)
	a, _ := dial(t, ts, "secret-token-0123456789abcdef0123")
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("un\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "b.txt"), []byte("b\n"), 0o644)
	gitIn(t, dir, "init", "-q", "-b", "main")
	gitIn(t, dir, "add", "-A")
	gitIn(t, dir, "commit", "-q", "-m", "init")
	id := a.call("projects.create", map[string]any{"type": "local", "path": dir})["result"].(map[string]any)["id"].(string)
	a.call("project.open", map[string]any{"id": id})
	start := func(n int, title string) string {
		a.call("kanban.create", map[string]any{"title": title})
		a.call("kanban.plan", map[string]any{"id": n, "plan": "p", "goals": []string{"g"}})
		return a.call("kanban.start", map[string]any{"id": n})["result"].(map[string]any)["ticket"].(map[string]any)["worktree"].(string)
	}
	wt := start(1, "A")
	os.WriteFile(filepath.Join(wt, "a.txt"), []byte("deux\n"), 0o644)
	gitIn(t, wt, "commit", "-q", "-am", "#1 deux")
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("trois\n"), 0o644)
	gitIn(t, dir, "commit", "-q", "-am", "trois")

	// Rebase: conflict left in progress.
	st := a.call("kanban.rebase", map[string]any{"id": 1})["result"].(map[string]any)
	w := st["worktree"].(map[string]any)
	if w["rebase"] != true || len(w["conflicts"].([]any)) != 1 || st["into"] != "main" {
		t.Fatalf("rebase: %+v", st)
	}
	os.WriteFile(filepath.Join(wt, "a.txt"), []byte("trois\ndeux\n"), 0o644)
	gitIn(t, wt, "add", "a.txt")
	st = a.call("kanban.continue", map[string]any{"id": 1, "where": "worktree"})["result"].(map[string]any)
	if w := st["worktree"].(map[string]any); w["rebase"] != false || len(w["conflicts"].([]any)) != 0 {
		t.Fatalf("continue: %+v", st)
	}
	// Merge into main.
	st = a.call("kanban.merge", map[string]any{"id": 1})["result"].(map[string]any)
	if st["merged"] != true {
		t.Fatalf("merge: %+v", st)
	}
	if got, _ := os.ReadFile(filepath.Join(dir, "a.txt")); string(got) != "trois\ndeux\n" {
		t.Fatalf("merged content: %q", got)
	}
	if !strings.HasPrefix(gitIn(t, dir, "log", "-1", "--format=%s"), "Merge #1 A") {
		t.Fatal("merge commit message")
	}

	// Squash with a conflict, then abort.
	wt2 := start(2, "B")
	os.WriteFile(filepath.Join(wt2, "b.txt"), []byte("b2\n"), 0o644)
	gitIn(t, wt2, "commit", "-q", "-am", "#2 b2")
	os.WriteFile(filepath.Join(dir, "b.txt"), []byte("b3\n"), 0o644)
	gitIn(t, dir, "commit", "-q", "-am", "b3")
	// Uncommitted changes of the main folder are put aside during the merge.
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("sale\n"), 0o644)
	st = a.call("kanban.merge", map[string]any{"id": 2, "squash": true})["result"].(map[string]any)
	if m := st["main"].(map[string]any); m["squash"] != true || len(m["conflicts"].([]any)) != 1 {
		t.Fatalf("squash conflict: %+v", st)
	}
	st = a.call("kanban.abort", map[string]any{"id": 2, "where": "main"})["result"].(map[string]any)
	if m := st["main"].(map[string]any); m["squash"] != false || len(m["conflicts"].([]any)) != 0 || gitIn(t, dir, "status", "--porcelain", "--untracked-files=no") != "M a.txt" {
		t.Fatalf("abort: %+v / %q", st, gitIn(t, dir, "status", "--porcelain", "--untracked-files=no"))
	}
	if got, _ := os.ReadFile(filepath.Join(dir, "a.txt")); string(got) != "sale\n" {
		t.Fatalf("changes of the main folder after the abort: %q", got)
	}
	// Rebase of a worktree with uncommitted changes, then merge into the dirty main folder.
	os.WriteFile(filepath.Join(wt2, "a.txt"), []byte("wip\n"), 0o644)
	a.call("kanban.rebase", map[string]any{"id": 2})
	os.WriteFile(filepath.Join(wt2, "b.txt"), []byte("b3\nb2\n"), 0o644)
	gitIn(t, wt2, "add", "b.txt")
	a.call("kanban.continue", map[string]any{"id": 2, "where": "worktree"})
	if got, _ := os.ReadFile(filepath.Join(wt2, "a.txt")); string(got) != "wip\n" {
		t.Fatalf("changes of the worktree after the rebase: %q", got)
	}
	st = a.call("kanban.merge", map[string]any{"id": 2})["result"].(map[string]any)
	if st["merged"] != true {
		t.Fatalf("merge with a dirty main folder: %+v", st)
	}
	got, _ := os.ReadFile(filepath.Join(dir, "a.txt"))
	got2, _ := os.ReadFile(filepath.Join(dir, "b.txt"))
	if string(got) != "sale\n" || string(got2) != "b3\nb2\n" {
		t.Fatalf("after the merge: a=%q b=%q", got, got2)
	}
}

// The pull request of a ticket: branch pushed to origin, gh called with its title and base.
func TestKanbanPR(t *testing.T) {
	for k, v := range map[string]string{"GIT_AUTHOR_NAME": "t", "GIT_AUTHOR_EMAIL": "t@x", "GIT_COMMITTER_NAME": "t", "GIT_COMMITTER_EMAIL": "t@x"} {
		t.Setenv(k, v)
	}
	// A fake gh: records its arguments and answers the address of the pull request.
	bin := t.TempDir()
	os.WriteFile(filepath.Join(bin, "gh"), []byte("#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$(git rev-parse --git-common-dir)/gh-args\"\necho https://github.com/o/r/pull/7\n"), 0o755)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	_, ts := newServer(t)
	a, _ := dial(t, ts, "secret-token-0123456789abcdef0123")
	dir, origin := t.TempDir(), t.TempDir()
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("un\n"), 0o644)
	gitIn(t, dir, "init", "-q", "-b", "main")
	gitIn(t, dir, "add", "-A")
	gitIn(t, dir, "commit", "-q", "-m", "init")
	id := a.call("projects.create", map[string]any{"type": "local", "path": dir})["result"].(map[string]any)["id"].(string)
	a.call("project.open", map[string]any{"id": id})
	a.call("kanban.create", map[string]any{"title": "Export", "description": "Need"})
	a.call("kanban.plan", map[string]any{"id": 1, "plan": "p", "goals": []string{"g"}})
	a.call("kanban.start", map[string]any{"id": 1})
	if gi := a.call("kanban.gitstate", map[string]any{"id": 1})["result"].(map[string]any); gi["canPR"] != false {
		t.Fatalf("pull request without origin: %+v", gi)
	}
	gitIn(t, origin, "init", "-q", "--bare")
	gitIn(t, dir, "remote", "add", "origin", origin)
	if gi := a.call("kanban.gitstate", map[string]any{"id": 1})["result"].(map[string]any); gi["canPR"] != true {
		t.Fatalf("pull request with origin and gh: %+v", gi)
	}
	tk := a.call("kanban.pr", map[string]any{"id": 1})["result"].(map[string]any)
	if tk["pr"] != "https://github.com/o/r/pull/7" {
		t.Fatalf("pr: %+v", tk)
	}
	if gitIn(t, origin, "branch", "--list", "ticket/1-export") == "" {
		t.Fatal("branch not pushed")
	}
	args, _ := os.ReadFile(filepath.Join(dir, ".git", "gh-args"))
	if s := string(args); !strings.Contains(s, "--base\nmain\n") || !strings.Contains(s, "--title\n#1 Export\n") || !strings.Contains(s, "Need\n\n## Goals\n- [ ] g") {
		t.Fatalf("gh args: %s", s)
	}
}
