package server

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSavedCommands(t *testing.T) {
	_, ts := newServer(t)
	a, err := dial(t, ts, "secret-token-0123456789abcdef0123")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "sub"), 0o755)
	id := a.call("projects.create", map[string]any{"type": "local", "path": dir})["result"].(map[string]any)["id"].(string)
	a.call("project.open", map[string]any{"id": id})

	if l := a.call("commands.list", nil)["result"].(map[string]any); len(l["commands"].([]any)) != 0 {
		t.Fatalf("list = %+v", l)
	}
	saved := a.call("commands.save", map[string]any{"commands": []map[string]any{
		{"name": "Where", "command": "pwd > where.txt\nsleep 30", "flag": "other", "cwd": "sub"},
	}})["result"].([]any)
	a.waitEvent("commands.changed", nil)
	cid := saved[0].(map[string]any)["id"].(string)
	if _, err := os.Stat(filepath.Join(dir, ".ide", "commands.json")); err != nil {
		t.Fatal("commands.json not written")
	}

	// Run: a task in the folder of the command, tagged with its id.
	r := a.call("commands.run", map[string]any{"id": cid})["result"].(map[string]any)
	c1 := r["console"].(map[string]any)
	if c1["commandId"] != cid || c1["title"] != "Where" {
		t.Fatalf("console = %+v", c1)
	}
	for i := 0; i < 50; i++ {
		if _, err := os.Stat(filepath.Join(dir, "sub", "where.txt")); err == nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if data, _ := os.ReadFile(filepath.Join(dir, "sub", "where.txt")); string(data) != filepath.Join(dir, "sub")+"\n" {
		t.Fatalf("cwd = %q", data)
	}
	// Running: answered as such, nothing started.
	r = a.call("commands.run", map[string]any{"id": cid})["result"].(map[string]any)
	if r["running"].(map[string]any)["id"] != c1["id"] || r["console"] != nil {
		t.Fatalf("second run = %+v", r)
	}
	// Restart: the console is replaced.
	r = a.call("commands.run", map[string]any{"id": cid, "restart": true})["result"].(map[string]any)
	if r["replaced"] != c1["id"] || r["console"].(map[string]any)["id"] == c1["id"] {
		t.Fatalf("restart = %+v", r)
	}
	if n := len(a.call("console.list", nil)["result"].([]any)); n != 1 {
		t.Fatalf("%d consoles", n)
	}
	if e := a.callRaw("commands.run", map[string]any{"id": "nope"}); e["error"] == nil {
		t.Fatal("unknown command run")
	}
	// An invalid file is reported, not overwritten.
	os.WriteFile(filepath.Join(dir, ".ide", "commands.json"), []byte("{"), 0o644)
	if l := a.call("commands.list", nil)["result"].(map[string]any); l["error"] == nil {
		t.Fatalf("invalid file: %+v", l)
	}
}
