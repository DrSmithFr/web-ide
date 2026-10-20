package pair

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DrSmithFr/web-ide/lib/identity"
)

// join --phrase pairs a pod under its own key, with no relay: two pods joined so are two
// devices, and the phrase is stored nowhere.
func TestJoinWithPhrase(t *testing.T) {
	a, _ := identity.FromPhrase(phrase)
	var keys []string
	for i := 0; i < 2; i++ {
		e, _ := env(t)
		e.In = strings.NewReader(phrase + "\n")
		e.Name = "pod" + string(rune('a'+i))
		if err := Run(e, []string{"join", "--phrase"}); err != nil {
			t.Fatalf("join --phrase: %v", err)
		}
		p, err := identity.Open(e.Dir)
		if err != nil {
			t.Fatal(err)
		}
		if !p.Paired() || p.Account != a.ID || p.Cert.Key != p.ID() {
			t.Fatalf("not paired under its own key: %+v", p.Cert)
		}
		keys = append(keys, p.ID())
		for _, name := range []string{"device.key", "identity.json"} {
			data, _ := os.ReadFile(filepath.Join(e.Dir, name))
			if strings.Contains(string(data), "lecture") {
				t.Fatalf("%s holds the phrase", name)
			}
		}
	}
	if keys[0] == keys[1] {
		t.Fatal("two pods joined with the phrase share a key")
	}

	// A pod restored from the phrase holds the admin key: restore is the command for it.
	e, _ := env(t)
	e.In = strings.NewReader(phrase + "\n")
	if err := Restore(e); err != nil {
		t.Fatal(err)
	}
	e.In = strings.NewReader(phrase + "\n")
	if err := JoinWithPhrase(e); err == nil {
		t.Fatal("join --phrase on the admin key was accepted")
	}
}

// revoke signs a list with the phrase typed, after the one the pod holds, offline.
func TestRevokeOffline(t *testing.T) {
	e, out := env(t)
	e.In = strings.NewReader(phrase + "\n")
	if err := Run(e, []string{"join", "--phrase"}); err != nil {
		t.Fatal(err)
	}
	other := identity.KeyID(identity.KeyFromSeed(make([]byte, 32)))
	third := identity.KeyID(identity.KeyFromSeed([]byte(strings.Repeat("x", 32))))
	for i, key := range []string{other, third, third} {
		e.In = strings.NewReader(phrase + "\n")
		if err := Run(e, []string{"revoke", key}); err != nil {
			t.Fatalf("revoke %d: %v", i, err)
		}
	}
	p, _ := identity.Open(e.Dir)
	if p.Revocations == nil || p.Revocations.Seq != 2 || !p.Revocations.Has(other) || !p.Revocations.Has(third) {
		t.Fatalf("list: %+v", p.Revocations)
	}
	if !strings.Contains(out.String(), "already revoked") {
		t.Fatalf("output: %s", out.String())
	}

	// Another phrase is not the authority of this account; a bad key is refused.
	e.In = strings.NewReader("abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon art\n")
	if err := Run(e, []string{"revoke", other}); err == nil {
		t.Fatal("a foreign phrase signed a revocation")
	}
	if err := Run(e, []string{"revoke", "nope"}); err == nil {
		t.Fatal("a bad key was accepted")
	}
}
