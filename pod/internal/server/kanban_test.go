package server

import (
	"os"
	"path/filepath"
	"testing"
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
