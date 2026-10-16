package sessions

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/DrSmithFr/web-ide/pod/internal/store"
)

func TestDesks(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	s := New(st)
	s.Put("p", json.RawMessage(`{"layout":{},"recent":["/a","/b"]}`))
	s.FlushAll()

	d := s.Desks("p")
	if len(d.Desks) != 1 || d.Desks[0].N != 1 || d.Desks[0].Mode != "full" {
		t.Fatalf("default desks: %+v", d.Desks)
	}
	if len(d.Recent) != 2 || d.Recent[0] != "/a" {
		t.Fatalf("recent taken from desk 1: %v", d.Recent)
	}
	if Key("p", 1) != "p" || Key("p", 3) != "p@3" {
		t.Fatal("keys")
	}

	// A known mode is kept, another one is full.
	if d := s.PutDesks("p", DeskList{Desks: []Desk{{N: 2, Mode: "grid"}, {N: 3, Mode: "tiled"}, {N: 4, Mode: "scroll"}}}); d.Desks[1].Mode != "grid" || d.Desks[2].Mode != "full" || d.Desks[3].Mode != "scroll" {
		t.Fatalf("modes: %+v", d.Desks)
	}

	// Desk 1 cannot go, duplicates are dropped.
	d = s.PutDesks("p", DeskList{Desks: []Desk{{N: 2, Name: "Review"}, {N: 2}, {N: 3}}, Recent: d.Recent})
	if len(d.Desks) != 3 || d.Desks[0].N != 1 || d.Desks[1].Name != "Review" {
		t.Fatalf("put: %+v", d.Desks)
	}
	s.Put(Key("p", 3), json.RawMessage(`{"layout":{}}`))
	s.FlushAll()
	if !exists(st, "sessions/p@3.json") {
		t.Fatal("desk 3 not written")
	}

	// Removing desk 3 removes its session.
	s.PutDesks("p", DeskList{Desks: []Desk{{N: 1}, {N: 2, Name: "Review"}}})
	if exists(st, "sessions/p@3.json") || s.HasDesk("p", 3) || !s.HasDesk("p", 2) {
		t.Fatal("desk 3 not removed")
	}

	// A new Sessions reads the list back.
	if got := New(st).Desks("p"); len(got.Desks) != 2 || got.Desks[1].Name != "Review" {
		t.Fatalf("reload: %+v", got.Desks)
	}

	// Deleting the project removes every desk.
	s.Put(Key("p", 2), json.RawMessage(`{"layout":{}}`))
	s.FlushAll()
	s.Delete("p")
	if exists(st, "sessions/p@2.json") || exists(st, "sessions/p.desks.json") || exists(st, "sessions/p.json") {
		t.Fatal("delete left files")
	}
}

func exists(st *store.Store, name string) bool {
	_, err := os.Stat(st.Path(name))
	return err == nil
}
