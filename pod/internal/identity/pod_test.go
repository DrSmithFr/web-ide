package identity

import (
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPodIdentityFiles(t *testing.T) {
	dir := t.TempDir()
	p, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if p.Paired() {
		t.Fatal("a fresh pod is unpaired")
	}
	if len(p.Seed) != 32 {
		t.Fatalf("device seed: %d bytes", len(p.Seed))
	}

	// device.key must be 0600 and hold the seed.
	st, err := os.Stat(filepath.Join(dir, "device.key"))
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0o600 {
		t.Errorf("device.key mode is %o, want 0600", st.Mode().Perm())
	}

	// The same folder gives the same device id on the next run.
	p2, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if p2.ID() != p.ID() {
		t.Errorf("the device key must survive a restart: %s then %s", p.ID(), p2.ID())
	}

	a, err := FromPhrase(loadVectors(t).Phrase)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Pin(a.ID); err != nil {
		t.Fatal(err)
	}
	if err := p.SetCert(NewCert(a, p.Key(), "test pod", KindPod)); err != nil {
		t.Fatal(err)
	}
	if !p.Paired() {
		t.Fatal("the pod must be paired after pin + cert")
	}

	// A certificate signed by another account cannot be stored.
	other, err := FromPhrase("abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon about")
	if err != nil {
		t.Fatal(err)
	}
	if err := p.SetCert(NewCert(other, p.Key(), "evil", KindPod)); err == nil {
		t.Error("a certificate of another account must be refused")
	}

	// Revocations: only a signed list of the pinned account, with a higher seq.
	r := &Revocations{Account: a.ID, Seq: 2, Epoch: 1, Revoked: []string{p.ID()}, Created: 1766721600}
	if err := SignRevocations(a.Priv, r); err != nil {
		t.Fatal(err)
	}
	if err := p.ApplyRevocations(r); err != nil {
		t.Fatal(err)
	}
	if err := p.PeerError(p.Cert); err == nil {
		t.Error("the pod must refuse its own revoked device")
	}
	// An older seq is ignored: a relay that replays an old list changes nothing.
	older := &Revocations{Account: a.ID, Seq: 1, Epoch: 1, Revoked: []string{p.ID()}, Created: 1766721600}
	if err := SignRevocations(a.Priv, older); err != nil {
		t.Fatal(err)
	}
	if err := p.ApplyRevocations(older); err != nil {
		t.Fatal(err)
	}
	if p.Revocations.Seq != 2 {
		t.Errorf("the stored list must keep the highest seq, got %d", p.Revocations.Seq)
	}

	// identity.json holds the certificate, the pinned account and the list — never a private
	// key, never the phrase.
	data, err := os.ReadFile(filepath.Join(dir, "identity.json"))
	if err != nil {
		t.Fatal(err)
	}
	secrets := []string{hex.EncodeToString(a.Priv.Seed()), hex.EncodeToString(a.Seed), hex.EncodeToString(a.Priv), loadVectors(t).Phrase}
	for _, secret := range secrets {
		if strings.Contains(string(data), secret) {
			t.Errorf("identity.json must not hold the phrase or the account key in clear")
		}
	}

	// A restart reads the same identity back.
	p3, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if p3.Account != a.ID || p3.Cert == nil || p3.Revocations == nil || p3.Revocations.Seq != 2 {
		t.Errorf("identity.json must survive a restart: %+v", p3)
	}
}
