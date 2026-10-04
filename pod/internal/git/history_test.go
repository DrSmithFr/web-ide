package git

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DrSmithFr/web-ide/pod/internal/execx"
)

func TestHistory(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git absent")
	}
	ctx := context.Background()
	dir := t.TempDir()
	g := New(execx.Local{}, dir)
	run(t, dir, "init", "-q", "-b", "main")
	t.Setenv("GIT_AUTHOR_NAME", "t")
	t.Setenv("GIT_AUTHOR_EMAIL", "t@x")
	t.Setenv("GIT_COMMITTER_NAME", "t")
	t.Setenv("GIT_COMMITTER_EMAIL", "t@x")
	write := func(name, text string) { os.WriteFile(filepath.Join(dir, name), []byte(text), 0o644) }
	write("a.txt", "one\n")
	run(t, dir, "add", "-A")
	run(t, dir, "commit", "-q", "-m", "first")
	run(t, dir, "mv", "a.txt", "b.txt")
	write("c.txt", "c\n")
	run(t, dir, "add", "-A")
	run(t, dir, "commit", "-q", "-m", "second\n\nlonger body")
	run(t, dir, "switch", "-q", "-c", "side")
	write("c.txt", "side\n")
	run(t, dir, "commit", "-q", "-am", "on side")
	run(t, dir, "switch", "-q", "main")
	write("d.txt", "d\n")
	run(t, dir, "add", "-A")
	run(t, dir, "commit", "-q", "-m", "on main")
	run(t, dir, "merge", "-q", "--no-ff", "-m", "merge side", "side")

	log, err := g.Log(ctx, 0, 50, "")
	if err != nil || len(log) != 5 || log[0].Subject != "merge side" || len(log[0].Parents) != 2 || log[4].Subject != "first" || len(log[4].Parents) != 0 {
		t.Fatalf("log = %+v %v", log, err)
	}
	if page, _ := g.Log(ctx, 3, 50, ""); len(page) != 2 {
		t.Fatalf("second page = %+v", page)
	}
	if found, _ := g.Log(ctx, 0, 50, "LONGER"); len(found) != 1 || found[0].Subject != "second" {
		t.Fatalf("search in the body = %+v", found)
	}
	if found, _ := g.Log(ctx, 0, 50, "T@X"); len(found) != 5 {
		t.Fatalf("search by author = %d", len(found))
	}

	second := log[len(log)-2]
	info, err := g.CommitInfo(ctx, second.Hash)
	if err != nil || info.Message != "second\n\nlonger body" || info.Committer != "t" || len(info.Files) != 2 {
		t.Fatalf("info = %+v %v", info, err)
	}
	if f := info.Files[0]; f.Status != "R" || f.OrigPath != filepath.Join(dir, "a.txt") || f.Path != filepath.Join(dir, "b.txt") {
		t.Fatalf("rename = %+v", f)
	}
	if root, _ := g.CommitInfo(ctx, log[4].Hash); len(root.Files) != 1 || root.Files[0].Status != "A" {
		t.Fatalf("root commit = %+v", root)
	}
	if _, err := g.CommitInfo(ctx, "--all"); err == nil {
		t.Fatal("option accepted as a revision")
	}

	if _, err := g.Revert(ctx, log[0].Hash); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(filepath.Join(dir, "c.txt")); string(data) != "c\n" {
		t.Fatalf("merge reverted: %q", data)
	}
	if err := g.Reset(ctx, log[0].Hash, "soft"); err != nil {
		t.Fatal(err)
	}
	if st, _ := g.Status(ctx); len(st.Files) != 1 || st.Files[0].Index != "M" {
		t.Fatalf("soft reset keeps the index: %+v", st.Files)
	}
	if err := g.Reset(ctx, log[0].Hash, "keep"); err == nil || !strings.Contains(err.Error(), "keep") {
		t.Fatalf("unknown mode: %v", err)
	}
	if err := g.Reset(ctx, log[0].Hash, "hard"); err != nil {
		t.Fatal(err)
	}
	if err := g.Switch(ctx, "from-first", true, log[4].Hash); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "a.txt")); err != nil {
		t.Fatal("branch created at the first commit")
	}
}

func TestHistoryWithoutCommit(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git absent")
	}
	// git speaks the language of the user: its message must not be needed.
	t.Setenv("LANGUAGE", "fr")
	dir := t.TempDir()
	run(t, dir, "init", "-q", "-b", "main")
	list, err := New(execx.Local{}, dir).Log(context.Background(), 0, 50, "")
	if err != nil || list == nil || len(list) != 0 {
		t.Fatalf("Log of a repository without commit: %v %v", list, err)
	}
}
