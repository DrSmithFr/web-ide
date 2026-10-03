package git

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/DrSmithFr/web-ide/pod/internal/execx"
)

func run(t *testing.T, dir string, args ...string) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@x", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@x")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v %s", args, err, out)
	}
}

func TestRepo(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git absent")
	}
	ctx := context.Background()
	top := t.TempDir()
	proj := filepath.Join(top, "app") // project in a subfolder of the repository
	os.MkdirAll(proj, 0o755)
	g := New(execx.Local{}, proj)
	if st, _ := g.Status(ctx); st.Repo {
		t.Fatal("no repository expected")
	}
	run(t, top, "init", "-q", "-b", "main")
	t.Setenv("GIT_AUTHOR_NAME", "t")
	t.Setenv("GIT_AUTHOR_EMAIL", "t@x")
	t.Setenv("GIT_COMMITTER_NAME", "t")
	t.Setenv("GIT_COMMITTER_EMAIL", "t@x")
	a := filepath.Join(proj, "a.txt")
	os.WriteFile(a, []byte("one\n"), 0o644)
	os.WriteFile(filepath.Join(proj, "é b.txt"), []byte("x\n"), 0o644)

	st, err := g.Status(ctx)
	if err != nil || !st.Repo || len(st.Files) != 2 || !st.Files[0].Untracked {
		t.Fatalf("status = %+v %v", st, err)
	}
	if err := g.Stage(ctx, []string{a}); err != nil {
		t.Fatal(err)
	}
	if _, err := g.Commit(ctx, "first", false); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(a, []byte("one\ntwo\n"), 0o644)
	st, _ = g.Status(ctx)
	var fa *File
	for i := range st.Files {
		if st.Files[i].Path == a {
			fa = &st.Files[i]
		}
	}
	if fa == nil || fa.Work != "M" || fa.Index != "." || st.Branch != "main" {
		t.Fatalf("modified file = %+v (branch %s)", fa, st.Branch)
	}
	head, ok, _ := g.Show(ctx, a, "HEAD")
	if !ok || head != "one\n" {
		t.Fatalf("show HEAD = %q %v", head, ok)
	}
	if _, ok, _ := g.Show(ctx, filepath.Join(proj, "é b.txt"), "HEAD"); ok {
		t.Fatal("untracked file found in HEAD")
	}
	g.Stage(ctx, []string{a})
	if idx, _, _ := g.Show(ctx, a, ""); idx != "one\ntwo\n" {
		t.Fatalf("index = %q", idx)
	}
	g.Unstage(ctx, []string{a})
	g.Discard(ctx, []string{a})
	if data, _ := os.ReadFile(a); string(data) != "one\n" {
		t.Fatalf("discard: %q", data)
	}
	log, _ := g.Log(ctx, 10)
	if len(log) != 1 || log[0].Subject != "first" {
		t.Fatalf("log = %+v", log)
	}
	if err := g.Switch(ctx, "feature", true); err != nil {
		t.Fatal(err)
	}
	br, _ := g.Branches(ctx)
	if len(br) != 2 || !(br[0].Name == "feature" && br[0].Current) {
		t.Fatalf("branches = %+v", br)
	}
	if _, err := g.Commit(ctx, "", false); err == nil {
		t.Fatal("empty message accepted")
	}
}
