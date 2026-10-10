package idedir

import (
	"os"
	"path/filepath"
	"testing"
)

func TestEnsure(t *testing.T) {
	dir := filepath.Join(t.TempDir(), ".ide")
	if err := Local(dir); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(dir, ".gitignore"))
	if string(data) != Gitignore {
		t.Fatalf("gitignore = %q", data)
	}
	// An older one listing the bases only is replaced.
	_ = os.WriteFile(filepath.Join(dir, ".gitignore"), []byte("chats.db\nkanban.db\nworktrees/\n"), 0o644)
	if err := Local(dir); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(filepath.Join(dir, ".gitignore")); string(data) != Gitignore {
		t.Fatalf("not replaced: %q", data)
	}
	// One ignoring everything is kept as written.
	_ = os.WriteFile(filepath.Join(dir, ".gitignore"), []byte("# mine\n*\n"), 0o644)
	_ = Local(dir)
	if data, _ := os.ReadFile(filepath.Join(dir, ".gitignore")); string(data) != "# mine\n*\n" {
		t.Fatalf("rewritten: %q", data)
	}
}
