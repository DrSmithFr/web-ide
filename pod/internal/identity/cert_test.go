package identity

import (
	"testing"
)

func TestCertVerify(t *testing.T) {
	a, err := FromPhrase(loadVectors(t).Phrase)
	if err != nil {
		t.Fatal(err)
	}
	dev := KeyFromSeed(DeviceSeed(a.Seed))
	devID := KeyID(dev)
	c := NewCert(a, dev, "test pod", KindPod)
	if err := VerifyCert(c, a.ID, nil); err != nil {
		t.Fatalf("a certificate signed by the account must verify: %v", err)
	}

	// Another account: the pinned key is the guard, the relay cannot say who is who.
	other, err := FromPhrase("abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon about")
	if err != nil {
		t.Fatal(err)
	}
	foreign := NewCert(other, dev, "other pod", KindPod)
	if err := VerifyCert(foreign, a.ID, nil); err == nil {
		t.Error("a certificate of another account must be refused")
	}

	// A tampered certificate: the canonical form no longer matches the signature.
	tampered := *c
	tampered.Name = "evil pod"
	if err := VerifyCert(&tampered, a.ID, nil); err == nil {
		t.Error("a tampered certificate must be refused")
	}

	// A wrong scope.
	wide := *c
	wide.Scope = "read"
	if err := VerifyCert(&wide, a.ID, nil); err == nil {
		t.Error("a scope other than all must be refused")
	}

	// A revoked device.
	r := &Revocations{Account: a.ID, Seq: 1, Epoch: 1, Revoked: []string{devID}, Created: 1766721600}
	if err := SignRevocations(a.Priv, r); err != nil {
		t.Fatal(err)
	}
	if err := VerifyCert(c, a.ID, r); err == nil {
		t.Error("a revoked device must be refused")
	}
	if err := VerifyCert(c, a.ID, &Revocations{Account: other.ID, Seq: 1, Epoch: 1, Revoked: []string{devID}, Created: 1766721600, Sig: r.Sig}); err == nil {
		t.Error("a revocation list of another account must be refused")
	}
	// A list whose signature does not match its content.
	bad := *r
	bad.Revoked = []string{"any"}
	if err := VerifyRevocations(&bad, a.ID); err == nil {
		t.Error("a tampered revocation list must be refused")
	}
}

// The canonical form must be byte-identical to what the browser builds: fixed order, no
// spaces, only the JSON escapes of the browser.
func TestCanonicalForm(t *testing.T) {
	c := &Cert{V: 1, Account: "acc", Key: "key", Name: `a "quoted" name`, Kind: KindBrowser, Created: 1700000000, Scope: ScopeAll}
	want := `{"v":1,"account":"acc","key":"key","name":"a \"quoted\" name","kind":"browser","created":1700000000,"scope":"all"}`
	if c.Canonical() != want {
		t.Errorf("cert canonical:\n got %s\nwant %s", c.Canonical(), want)
	}
	r := &Revocations{Account: "acc", Seq: 3, Epoch: 2, Revoked: []string{"b", "a"}, Created: 1700000000}
	// The revoked keys are sorted, so both sides build the same bytes.
	if r.Canonical() != `{"account":"acc","seq":3,"epoch":2,"revoked":["a","b"],"created":1700000000}` {
		t.Errorf("revocation canonical: %s", r.Canonical())
	}
}
