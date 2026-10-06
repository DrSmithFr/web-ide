package agent

import (
	"reflect"
	"strings"
	"testing"
)

func TestLineHunks(t *testing.T) {
	split := func(s string) []string { return strings.Split(s, "") }
	for _, c := range []struct {
		a, b string
		want []Hunk
	}{
		{"abc", "abc", []Hunk{}},
		{"abc", "abxc", []Hunk{{2, 0, 2, 1}}},
		{"abxc", "abc", []Hunk{{2, 1, 2, 0}}},
		{"abcdef", "abXdeY", []Hunk{{2, 1, 2, 1}, {5, 1, 5, 1}}},
		{"", "ab", []Hunk{{0, 0, 0, 2}}},
		{"axbycz", "abc", []Hunk{{1, 1, 1, 0}, {3, 1, 2, 0}, {5, 1, 3, 0}}},
	} {
		got, ok := LineHunks(split(c.a), split(c.b))
		if !ok || !reflect.DeepEqual(got, c.want) {
			t.Errorf("%q → %q: %v, want %v", c.a, c.b, got, c.want)
		}
	}
	// Applying the hunks rebuilds the new text.
	a := strings.Split("one\ntwo\nthree\nfour\nfive\nsix", "\n")
	b := strings.Split("zero\none\n2\nthree\nfive\nsix\nseven", "\n")
	hunks, _ := LineHunks(a, b)
	var out []string
	i := 0
	for _, h := range hunks {
		out = append(out, a[i:h.A]...)
		out = append(out, b[h.B:h.B+h.BL]...)
		i = h.A + h.AL
	}
	out = append(out, a[i:]...)
	if !reflect.DeepEqual(out, b) {
		t.Fatalf("rebuilt %v", out)
	}
}

func TestDiffLines(t *testing.T) {
	old := "a\nb\nc\nd\ne\nf\ng"
	got := DiffLines(old, "a\nb\nc\nd\nE\nf\ng")
	want := []DiffLine{{T: "…", Text: "line {n}", N: 3}, {T: " ", Text: "c"}, {T: " ", Text: "d"}, {T: "-", Text: "e"}, {T: "+", Text: "E"}, {T: " ", Text: "f"}, {T: " ", Text: "g"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("diff: %+v", got)
	}
}
