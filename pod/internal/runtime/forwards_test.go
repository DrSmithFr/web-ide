package runtime

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DrSmithFr/web-ide/pod/internal/projects"
	"github.com/DrSmithFr/web-ide/pod/internal/sshx"
	"github.com/DrSmithFr/web-ide/pod/internal/store"
)

// A project saved with the old .ide/tunnels.json keeps its port forwards: they are read, then
// moved to forwards.json.
func TestForwardsMigrateTunnelsJSON(t *testing.T) {
	st, _ := store.Open(t.TempDir())
	dir := t.TempDir()
	ide := filepath.Join(dir, ".ide")
	os.MkdirAll(ide, 0o755)
	old := `{"tunnels":[{"id":"a","remoteHost":"10.0.0.2","remotePort":5432,"localPort":15432,"lan":false,"enabled":true}]}`
	os.WriteFile(filepath.Join(ide, "tunnels.json"), []byte(old), 0o644)

	rt, err := Open(projects.Project{ID: "p-f1", Type: "local", Path: dir}, sshx.Creds{}, Deps{Store: st}, func(string, any, string) {})
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()
	list := rt.ForwardSpecs()
	if len(list) != 1 || list[0].RemoteHost != "10.0.0.2" || list[0].RemotePort != 5432 || list[0].LocalPort != 15432 || !list[0].Enabled {
		t.Fatalf("forwards read from tunnels.json = %+v", list)
	}
	if _, err := os.Stat(filepath.Join(ide, "tunnels.json")); err == nil {
		t.Fatal("tunnels.json still there after the move")
	}
	data, err := os.ReadFile(filepath.Join(ide, "forwards.json"))
	if err != nil || !strings.Contains(string(data), `"forwards"`) || !strings.Contains(string(data), "15432") {
		t.Fatalf("forwards.json = %s (%v)", data, err)
	}
	// Read again: forwards.json is now the source.
	if again := rt.ForwardSpecs(); len(again) != 1 || again[0].ID != "a" {
		t.Fatalf("second read = %+v", again)
	}
	// With both files, forwards.json wins and tunnels.json is left alone.
	os.WriteFile(filepath.Join(ide, "tunnels.json"), []byte(`{"tunnels":[]}`), 0o644)
	if again := rt.ForwardSpecs(); len(again) != 1 {
		t.Fatalf("forwards.json must win: %+v", again)
	}
}
