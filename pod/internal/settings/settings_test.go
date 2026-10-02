package settings

import (
	"encoding/json"
	"testing"

	"webide/pod/internal/store"
)

func TestHistoryRollback(t *testing.T) {
	st, _ := store.Open(t.TempDir())
	s, err := Load(st)
	if err != nil {
		t.Fatal(err)
	}
	a, _ := s.Save(json.RawMessage(`{"theme":"a"}`), "A")
	s.Save(json.RawMessage(`{"theme":"b"}`), "B")
	if got := string(s.Current().Settings); got != `{"theme":"b"}` {
		t.Fatalf("current = %s", got)
	}
	r, err := s.Rollback(a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if string(r.Settings) != `{"theme":"a"}` || r.ID == a.ID {
		t.Fatalf("rollback must append a copy, got #%d %s", r.ID, r.Settings)
	}
	// The rollback itself can be undone.
	h := s.History()
	if len(h) != 4 || !h[0].Current {
		t.Fatalf("history = %+v", h)
	}
	if _, err := s.Rollback(h[1].ID); err != nil || string(s.Current().Settings) != `{"theme":"b"}` {
		t.Fatalf("undo rollback failed: %v %s", err, s.Current().Settings)
	}
	// Reload from disk.
	s2, _ := Load(st)
	if string(s2.Current().Settings) != `{"theme":"b"}` {
		t.Fatal("not persisted")
	}
}

func TestHistoryCap(t *testing.T) {
	st, _ := store.Open(t.TempDir())
	s, _ := Load(st)
	for i := 0; i < maxEntries+20; i++ {
		s.Save(json.RawMessage(`{"n":`+string(rune('0'+i%10))+`}`), "x")
	}
	if n := len(s.History()); n != maxEntries {
		t.Fatalf("entries = %d", n)
	}
}
