package runtime

import (
	"slices"
	"testing"
)

type fakePart struct {
	name string
	log  *[]string
}

func (p fakePart) Stop(release bool) {
	if release {
		*p.log = append(*p.log, p.name+" released")
	} else {
		*p.log = append(*p.log, p.name+" stopped")
	}
}

func (p fakePart) Idle() { *p.log = append(*p.log, p.name+" idle") }

func TestParts(t *testing.T) {
	r := &Runtime{parts: map[string]any{}}
	var log []string
	made := 0
	mk := func(name string) func() any {
		return func() any { made++; return fakePart{name, &log} }
	}
	a := r.Part("a", mk("a"))
	if again := r.Part("a", mk("a")); again != a || made != 1 {
		t.Fatal("a part is made once", made)
	}
	r.Part("b", mk("b"))
	r.Part("plain", func() any { return 42 }) // neither Stopper nor Idler
	r.idleParts()
	r.stopParts(false)
	r.stopParts(true)
	want := []string{"a idle", "b idle", "b stopped", "a stopped", "b released", "a released"}
	if !slices.Equal(log, want) {
		t.Fatal(log)
	}
}
