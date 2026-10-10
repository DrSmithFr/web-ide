package commands

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DrSmithFr/web-ide/pod/internal/fsx"
)

func TestSaveLoad(t *testing.T) {
	dir := filepath.Join(t.TempDir(), ".ide")
	fs := fsx.Local{}
	if list, err := Load(fs, dir); err != nil || len(list) != 0 {
		t.Fatalf("missing file: %v %v", list, err)
	}
	list, err := Save(fs, dir, []Command{
		{Name: " Build ", Command: "make build", Flag: "build"},
		{Name: "Dev", Command: "cd web\nnpm run dev", Flag: "serve", Cwd: "/web/", Port: 5173},
	})
	if err != nil {
		t.Fatal(err)
	}
	if list[0].ID == "" || list[0].ID == list[1].ID || list[0].Name != "Build" || list[1].Cwd != "web" {
		t.Fatalf("saved: %+v", list)
	}
	got, err := Load(fs, dir)
	if err != nil || len(got) != 2 || got[1].Command != "cd web\nnpm run dev" || got[1].Port != 5173 {
		t.Fatalf("loaded: %+v %v", got, err)
	}
	if gi, _ := os.ReadFile(filepath.Join(dir, ".gitignore")); !strings.Contains(string(gi), "*") {
		t.Fatal("no .gitignore")
	}
	// Ids are kept, a duplicated one is renewed.
	again, _ := Save(fs, dir, []Command{got[0], got[0]})
	if again[0].ID != got[0].ID || again[1].ID == got[0].ID {
		t.Fatalf("ids: %+v", again)
	}

	for _, bad := range []Command{
		{Name: "", Command: "x", Flag: "build"},
		{Name: "a", Command: " ", Flag: "build"},
		{Name: "a", Command: "x", Flag: "deploy"},
		{Name: "a", Command: "x", Flag: "build", Cwd: "../up"},
		{Name: "a", Command: "x", Flag: "build", Cwd: "a/../../b"},
		{Name: "a", Command: "x", Flag: "serve", Port: 70000},
	} {
		if _, err := Save(fs, dir, []Command{bad}); err == nil {
			t.Fatalf("accepted: %+v", bad)
		}
	}

	// An invalid file is an error, and stays as it is.
	os.WriteFile(filepath.Join(dir, "commands.json"), []byte("{oops"), 0o644)
	if _, err := Load(fs, dir); err == nil {
		t.Fatal("invalid file accepted")
	}
	if data, _ := os.ReadFile(filepath.Join(dir, "commands.json")); string(data) != "{oops" {
		t.Fatal("invalid file rewritten")
	}
}
