package clipboard

import (
	"strings"
	"testing"

	"github.com/DrSmithFr/web-ide/pod/internal/store"
)

func texts(l []Entry) string {
	var s []string
	for _, e := range l {
		s = append(s, e.Text)
	}
	return strings.Join(s, ",")
}

func TestHistory(t *testing.T) {
	st, _ := store.Open(t.TempDir())
	h := Load(st)
	h.Add("a", 3)
	h.Add("b", 3)
	h.Add("c", 3)
	if l, _ := h.Add("a", 3); texts(l) != "a,c,b" {
		t.Fatalf("an equal entry moves to the top: %s", texts(l))
	}
	if l, _ := h.Add("d", 3); texts(l) != "d,a,c" {
		t.Fatalf("max entries kept: %s", texts(l))
	}
	if _, changed := h.Add("d", 3); changed {
		t.Fatal("the text at the top again changes nothing")
	}
	if _, changed := h.Add(strings.Repeat("x", MaxText+1), 3); changed {
		t.Fatal("too large text kept")
	}
	if l := h.Remove("a"); texts(l) != "d,c" {
		t.Fatalf("remove: %s", texts(l))
	}
	// Reload from disk.
	if l := Load(st).List(); texts(l) != "d,c" {
		t.Fatalf("not persisted: %s", texts(l))
	}
	if l := h.Clear(); len(l) != 0 || len(Load(st).List()) != 0 {
		t.Fatal("clear")
	}
}
