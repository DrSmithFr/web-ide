package server

import (
	"testing"
)

// Each desk is a session of its own: a change on desk 2 reaches the windows of desk 2 only,
// and the desk list reaches every window of the project.
func TestDesks(t *testing.T) {
	_, ts := newServer(t)
	a, err := dial(t, ts, "secret-token-0123456789abcdef0123")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := dial(t, ts, "secret-token-0123456789abcdef0123")
	c, _ := dial(t, ts, "secret-token-0123456789abcdef0123")
	p := a.call("projects.create", map[string]any{"type": "local", "path": t.TempDir()})["result"].(map[string]any)
	id := p["id"].(string)

	open := a.call("project.open", map[string]any{"id": id, "desk": 2})["result"].(map[string]any)
	if open["desk"] != float64(1) {
		t.Fatalf("a desk not created opens desk 1: %v", open["desk"])
	}
	a.call("session.update", map[string]any{"session": map[string]any{"layout": "one"}})

	b.call("project.open", map[string]any{"id": id})
	list := map[string]any{"desks": []any{map[string]any{"n": 1}, map[string]any{"n": 2, "name": "Review"}}, "recent": []string{"/x"}}
	b.call("desks.update", map[string]any{"desks": list})
	ev := a.waitEvent("desks.changed", nil)
	if ds := ev["desks"].([]any); len(ds) != 2 || ds[1].(map[string]any)["name"] != "Review" {
		t.Fatalf("desks.changed = %v", ev)
	}

	r := b.call("desk.switch", map[string]any{"desk": 2})["result"].(map[string]any)
	if r["desk"] != float64(2) || r["session"] != nil {
		t.Fatalf("desk 2 starts empty: %v", r)
	}
	c.call("project.open", map[string]any{"id": id, "desk": 2})
	b.call("session.update", map[string]any{"session": map[string]any{"layout": "two"}})
	ev = c.waitEvent("session.changed", nil)
	if ev["desk"] != float64(2) || ev["session"].(map[string]any)["layout"] != "two" {
		t.Fatalf("session.changed on desk 2 = %v", ev)
	}

	// Desk 1 kept its session; a reads desk 2 and writes it (a tab sent there).
	if s := a.call("project.open", map[string]any{"id": id, "desk": 1})["result"].(map[string]any)["session"].(map[string]any); s["layout"] != "one" {
		t.Fatalf("desk 1 = %v", s)
	}
	if s := a.call("session.get", map[string]any{"desk": 2})["result"].(map[string]any); s["layout"] != "two" {
		t.Fatalf("session.get desk 2 = %v", s)
	}
	a.call("session.put", map[string]any{"desk": 2, "session": map[string]any{"layout": "sent"}})
	if ev := b.waitEvent("session.changed", nil); ev["session"].(map[string]any)["layout"] != "sent" {
		t.Fatalf("session.put = %v", ev)
	}

	// Removing desk 2: its session goes, its windows are not written back.
	a.call("desks.update", map[string]any{"desks": map[string]any{"desks": []any{map[string]any{"n": 1}}}})
	b.call("session.update", map[string]any{"session": map[string]any{"layout": "late"}})
	if r := a.callRawIn("", "session.get", map[string]any{"desk": 2}); r["error"] == nil {
		t.Fatalf("removed desk still readable: %v", r)
	}
}
