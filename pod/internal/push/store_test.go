package push

import "testing"

func TestStore(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Subscribe("dev1", Device{Sub: "s1", P256dh: "k", Auth: "a"}); err != nil {
		t.Fatal(err)
	}
	if len(s.For(Finished)) != 1 || len(s.For(Feedback)) != 1 {
		t.Fatal("a new device gets every kind")
	}
	_ = s.SetKind("dev1", Tests, false)
	// Subscribing again (a new endpoint) keeps the choices.
	_ = s.Subscribe("dev1", Device{Sub: "s2", P256dh: "k", Auth: "a"})
	again, _ := Open(dir)
	if d, ok := again.Get("dev1"); !ok || d.Sub != "s2" || d.Kinds[Tests] || !d.Kinds[Asks] {
		t.Fatalf("read back: %+v", d)
	}
	again.Gone("s2")
	if _, ok := again.Get("dev1"); ok {
		t.Fatal("a subscription gone at the relay is forgotten")
	}
}
