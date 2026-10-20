package acp

import (
	"encoding/json"
	"testing"
)

// opencode sends the paths of its tool calls relative to the working folder.
func TestToolPathsMadeAbsolute(t *testing.T) {
	var th thread
	th.index()
	var w wireToolCall
	_ = json.Unmarshal([]byte(`{"toolCallId":"t1","kind":"edit","locations":[{"path":"notes.txt"},{"path":"/abs/a.go"}],
		"content":[{"type":"diff","path":"sub/b.txt","oldText":"a","newText":"b"}]}`), &w)
	e := th.Entries[th.applyTool(w, "/work")]
	if len(e.Locations) != 2 || e.Locations[0] != "/work/notes.txt" || e.Locations[1] != "/abs/a.go" {
		t.Fatalf("locations: %v", e.Locations)
	}
	if e.Content[0].Path != "/work/sub/b.txt" {
		t.Fatalf("diff path: %s", e.Content[0].Path)
	}
}
