package identity

import (
	"encoding/hex"
	"encoding/json"
	"os"
	"testing"
)

// vectors.json holds the shared test vectors: the same phrase must give the same keys, the
// same canonical forms and the same signatures in Go and in the browser (the browser check
// reads the same file, see e2e/suites/identity.cjs).
type vectors struct {
	Phrase              string `json:"phrase"`
	Seed                string `json:"seed"`
	AccountSeed         string `json:"accountSeed"`
	SyncSeed            string `json:"syncSeed"`
	AccountID           string `json:"accountId"`
	AccountFingerprint  string `json:"accountFingerprint"`
	DeviceSeed          string `json:"deviceSeed"`
	DeviceID            string `json:"deviceId"`
	DeviceFingerprint   string `json:"deviceFingerprint"`
	Message             string `json:"message"`
	MessageSig          string `json:"messageSig"`
	DeviceMessageSig    string `json:"deviceMessageSig"`
	CertCanonical       string `json:"certCanonical"`
	CertSig             string `json:"certSig"`
	RevocationCanonical string `json:"revocationCanonical"`
	RevocationSig       string `json:"revocationSig"`
}

func loadVectors(t *testing.T) *vectors {
	t.Helper()
	data, err := os.ReadFile("vectors.json")
	if err != nil {
		t.Fatal(err)
	}
	var v vectors
	if err := json.Unmarshal(data, &v); err != nil {
		t.Fatal(err)
	}
	return &v
}

func TestSharedVectors(t *testing.T) {
	v := loadVectors(t)

	if err := Validate(v.Phrase); err != nil {
		t.Fatal(err)
	}
	a, err := FromPhrase(v.Phrase)
	if err != nil {
		t.Fatal(err)
	}
	if hex.EncodeToString(a.Seed) != v.Seed {
		t.Errorf("seed: got %s want %s", hex.EncodeToString(a.Seed), v.Seed)
	}
	if hex.EncodeToString(a.Priv.Seed()) != v.AccountSeed {
		t.Errorf("account seed: got %s want %s", hex.EncodeToString(a.Priv.Seed()), v.AccountSeed)
	}
	if a.ID != v.AccountID {
		t.Errorf("account id: got %s want %s", a.ID, v.AccountID)
	}
	if hex.EncodeToString(a.SyncSeed()) != v.SyncSeed {
		t.Errorf("sync seed: got %s want %s", hex.EncodeToString(a.SyncSeed()), v.SyncSeed)
	}
	if Fingerprint(a.ID) != v.AccountFingerprint {
		t.Errorf("account fingerprint: got %q want %q", Fingerprint(a.ID), v.AccountFingerprint)
	}

	dev := KeyFromSeed(DeviceSeed(a.Seed))
	if hex.EncodeToString(dev.Seed()) != v.DeviceSeed {
		t.Errorf("device seed: got %s want %s", hex.EncodeToString(dev.Seed()), v.DeviceSeed)
	}
	if KeyID(dev) != v.DeviceID {
		t.Errorf("device id: got %s want %s", KeyID(dev), v.DeviceID)
	}
	if Fingerprint(KeyID(dev)) != v.DeviceFingerprint {
		t.Errorf("device fingerprint: got %q want %q", Fingerprint(KeyID(dev)), v.DeviceFingerprint)
	}

	if Sign(a.Priv, []byte(v.Message)) != v.MessageSig {
		t.Errorf("message signature: got %s want %s", Sign(a.Priv, []byte(v.Message)), v.MessageSig)
	}
	if Sign(dev, []byte(v.Message)) != v.DeviceMessageSig {
		t.Errorf("device message signature: got %s want %s", Sign(dev, []byte(v.Message)), v.DeviceMessageSig)
	}
	if !Verify(v.AccountID, v.MessageSig, []byte(v.Message)) {
		t.Error("the message signature must verify against the account id")
	}

	// The canonical forms are the bytes the signature covers: they must match the browser's.
	c := &Cert{V: 1, Account: v.AccountID, Key: v.DeviceID, Name: "test pod", Kind: KindPod, Created: 1766721600, Scope: ScopeAll}
	if c.Canonical() != v.CertCanonical {
		t.Errorf("cert canonical:\n got %s\nwant %s", c.Canonical(), v.CertCanonical)
	}
	if !Verify(v.AccountID, v.CertSig, []byte(c.Canonical())) {
		t.Error("the certificate signature must verify")
	}
	r := &Revocations{Account: v.AccountID, Seq: 2, Epoch: 1, Revoked: []string{v.DeviceID}, Created: 1766721600}
	if r.Canonical() != v.RevocationCanonical {
		t.Errorf("revocation canonical:\n got %s\nwant %s", r.Canonical(), v.RevocationCanonical)
	}
	if !Verify(v.AccountID, v.RevocationSig, []byte(r.Canonical())) {
		t.Error("the revocation signature must verify")
	}
}
