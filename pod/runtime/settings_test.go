package runtime

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/DrSmithFr/web-ide/pod/projects"
	"github.com/DrSmithFr/web-ide/pod/sshx"
	"github.com/DrSmithFr/web-ide/pod/store"
)

// A worktree reads and writes the settings in the .ide of its main project.
func TestWorktreeSettings(t *testing.T) {
	st, _ := store.Open(t.TempDir())
	main := t.TempDir()
	wt := filepath.Join(main, ".ide", "worktrees", "1-x")
	os.MkdirAll(filepath.Join(wt, "src"), 0o755)
	os.WriteFile(filepath.Join(main, ".ide", "folders.json"), []byte(`{"folders":{"gen":"excluded"}}`), 0o644)
	os.WriteFile(filepath.Join(main, ".ide", "project.json"), []byte(`{"tests":{".go":"{name}_test.go"}}`), 0o644)

	p := projects.Project{ID: "p-t1", Type: "local", Path: wt, Parent: "p"}
	rt, err := Open(p, sshx.Creds{}, Deps{Store: st, SettingsRoot: main}, func(string, any, string) {})
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()
	if rt.IdeDir != filepath.Join(main, ".ide") || rt.Config.Tests[".go"] == "" {
		t.Fatalf("settings of the worktree: %s %+v", rt.IdeDir, rt.Config)
	}
	if ex := rt.Excluded(); len(ex) != 1 || ex[0] != filepath.Join(wt, "gen") {
		t.Fatalf("excluded = %v", ex)
	}
	if _, err := rt.MarkFolder(filepath.Join(wt, "src"), MarkSource); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(wt, ".ide")); err == nil {
		t.Fatal(".ide written in the worktree")
	}
	data, _ := os.ReadFile(filepath.Join(main, ".ide", ".gitignore"))
	if string(data) == "" {
		t.Fatal("no .gitignore in the .ide of the main project")
	}
}
