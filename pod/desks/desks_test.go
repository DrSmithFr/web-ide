package desks

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/DrSmithFr/web-ide/pod/store"
)

func TestDesks(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	s := New(st)
	s.Put("p", json.RawMessage(`{"layout":{},"recent":["/a","/b"]}`))
	s.FlushAll()

	d := s.List("p")
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
	if d := s.PutList("p", DeskList{Desks: []Desk{{N: 2, Mode: "grid"}, {N: 3, Mode: "tiled"}, {N: 4, Mode: "scroll"}}}); d.Desks[1].Mode != "grid" || d.Desks[2].Mode != "full" || d.Desks[3].Mode != "scroll" {
		t.Fatalf("modes: %+v", d.Desks)
	}

	// Desk 1 cannot go, duplicates are dropped.
	d = s.PutList("p", DeskList{Desks: []Desk{{N: 2, Name: "Review"}, {N: 2}, {N: 3}}, Recent: d.Recent})
	if len(d.Desks) != 3 || d.Desks[0].N != 1 || d.Desks[1].Name != "Review" {
		t.Fatalf("put: %+v", d.Desks)
	}
	s.Put(Key("p", 3), json.RawMessage(`{"layout":{}}`))
	s.FlushAll()
	if !exists(st, "desks/p@3.json") {
		t.Fatal("desk 3 not written")
	}

	// Removing desk 3 removes its state.
	s.PutList("p", DeskList{Desks: []Desk{{N: 1}, {N: 2, Name: "Review"}}})
	if exists(st, "desks/p@3.json") || s.HasDesk("p", 3) || !s.HasDesk("p", 2) {
		t.Fatal("desk 3 not removed")
	}

	// A new Store reads the list back.
	if got := New(st).List("p"); len(got.Desks) != 2 || got.Desks[1].Name != "Review" {
		t.Fatalf("reload: %+v", got.Desks)
	}

	// Deleting the project removes every desk.
	s.Put(Key("p", 2), json.RawMessage(`{"layout":{}}`))
	s.FlushAll()
	s.Delete("p")
	if exists(st, "desks/p@2.json") || exists(st, "desks/p.desks.json") || exists(st, "desks/p.json") {
		t.Fatal("delete left files")
	}
}

func exists(st *store.Store, name string) bool {
	_, err := os.Stat(st.Path(name))
	return err == nil
}

// The files of sessions/ (the name of the state of a desk before desks/) are read and moved to
// desks/: desk 1, a desk @2 and the list of the desks; a file already in desks/ wins.
func TestMigrateSessions(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	write := func(name, body string) {
		if err := os.MkdirAll(st.Path("sessions"), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(st.Path(name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("sessions/p.json", `{"layout":{"old":1}}`)
	write("sessions/p@2.json", `{"layout":{"two":2}}`)
	write("sessions/p.desks.json", `{"desks":[{"n":1},{"n":2,"name":"Review"}],"recent":["/x"]}`)
	write("sessions/q.json", `{"layout":{"legacy":true}}`)
	if err := os.MkdirAll(st.Path("desks"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(st.Path("desks/q.json"), []byte(`{"layout":{"new":true}}`), 0o600); err != nil {
		t.Fatal(err)
	}

	s := New(st)
	if got := string(s.Get("p")); got != `{"layout":{"old":1}}` {
		t.Fatalf("desk 1: %s", got)
	}
	if got := string(s.Get(Key("p", 2))); got != `{"layout":{"two":2}}` {
		t.Fatalf("desk 2: %s", got)
	}
	d := s.List("p")
	if len(d.Desks) != 2 || d.Desks[1].Name != "Review" || len(d.Recent) != 1 || d.Recent[0] != "/x" {
		t.Fatalf("list: %+v", d)
	}
	for _, f := range []string{"p.json", "p@2.json", "p.desks.json"} {
		if exists(st, "sessions/"+f) || !exists(st, "desks/"+f) {
			t.Fatalf("%s not moved", f)
		}
	}
	// desks/ wins over a leftover of sessions/, which stays untouched.
	if got := string(s.Get("q")); got != `{"layout":{"new":true}}` {
		t.Fatalf("desks/ must win: %s", got)
	}
	if !exists(st, "sessions/q.json") {
		t.Fatal("the leftover was touched")
	}
	// Deleting the project also removes what was not migrated yet.
	s.Delete("q")
	if exists(st, "sessions/q.json") || exists(st, "desks/q.json") {
		t.Fatal("delete left files")
	}
}
