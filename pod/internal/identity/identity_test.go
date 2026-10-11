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

	// Sync (milestone 4): the key of an epoch, its sub-keys, the X25519 pair of a device and
	// the wrap of the epoch key for it, checked by the browser with WebCrypto.
	EpochKey1        string `json:"epochKey1"`
	SettingsKey      string `json:"settingsKey"`
	VaultKey         string `json:"vaultKey"`
	PodXSeed         string `json:"podXSeed"`
	PodXPub          string `json:"podXPub"`
	AdminXSeed       string `json:"adminXSeed"`
	AdminXPub        string `json:"adminXPub"`
	Shared           string `json:"shared"`
	WrapKey          string `json:"wrapKey"`
	WrapAAD          string `json:"wrapAAD"`
	WrapNonce        string `json:"wrapNonce"`
	WrapData         string `json:"wrapData"`
	BlobAAD          string `json:"blobAAD"`
	BlobNonce        string `json:"blobNonce"`
	BlobData         string `json:"blobData"`
	SyncKeyCanonical string `json:"syncKeyCanonical"`
	SyncKeySig       string `json:"syncKeySig"`
	BlobCanonical    string `json:"blobCanonical"`
	BlobSig          string `json:"blobSig"`
	WrapCanonical    string `json:"wrapCanonical"`
	WrapSig          string `json:"wrapSig"`
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

// TestSyncVectors checks the sync primitives against the same file: the key of an epoch, its
// sub-keys, the X25519 pair of a device, the wrap of the epoch key for it and the ciphertext of
// a blob. The browser checks the same values with WebCrypto (scripts/identity-vectors.mjs), so a
// pod and a browser must encrypt and decrypt the same bytes.
func TestSyncVectors(t *testing.T) {
	v := loadVectors(t)
	seed, err := hex.DecodeString(v.Seed)
	if err != nil {
		t.Fatal(err)
	}
	syncSeed, err := hex.DecodeString(v.SyncSeed)
	if err != nil {
		t.Fatal(err)
	}
	deviceSeed := DeviceSeed(seed)
	if hex.EncodeToString(deviceSeed) != v.DeviceSeed {
		t.Errorf("device seed: got %s want %s", hex.EncodeToString(deviceSeed), v.DeviceSeed)
	}

	ekey := EpochKey(syncSeed, 1)
	if hex.EncodeToString(ekey) != v.EpochKey1 {
		t.Errorf("epoch key: got %s want %s", hex.EncodeToString(ekey), v.EpochKey1)
	}
	if hex.EncodeToString(KindKey(ekey, KindSettings)) != v.SettingsKey {
		t.Errorf("settings key: got %s want %s", hex.EncodeToString(KindKey(ekey, KindSettings)), v.SettingsKey)
	}
	if hex.EncodeToString(KindKey(ekey, KindVault)) != v.VaultKey {
		t.Errorf("vault key: got %s want %s", hex.EncodeToString(KindKey(ekey, KindVault)), v.VaultKey)
	}

	// The X25519 key of a device comes from its device seed, so a pod rebuilds it after a
	// restart without a new file on disk.
	xseed := Derive(deviceSeed, InfoX25519)
	if hex.EncodeToString(xseed) != v.PodXSeed {
		t.Errorf("pod x25519 seed: got %s want %s", hex.EncodeToString(xseed), v.PodXSeed)
	}
	pod, err := X25519FromSeed(xseed)
	if err != nil {
		t.Fatal(err)
	}
	if X25519ID(pod.PublicKey()) != v.PodXPub {
		t.Errorf("pod x25519 public: got %s want %s", X25519ID(pod.PublicKey()), v.PodXPub)
	}
	adminseed, err := hex.DecodeString(v.AdminXSeed)
	if err != nil {
		t.Fatal(err)
	}
	admin, err := X25519FromSeed(adminseed)
	if err != nil {
		t.Fatal(err)
	}
	if X25519ID(admin.PublicKey()) != v.AdminXPub {
		t.Errorf("admin x25519 public: got %s want %s", X25519ID(admin.PublicKey()), v.AdminXPub)
	}

	shared, err := Shared(admin, v.PodXPub)
	if err != nil {
		t.Fatal(err)
	}
	if hex.EncodeToString(shared) != v.Shared {
		t.Errorf("x25519 shared secret: got %s want %s", hex.EncodeToString(shared), v.Shared)
	}
	wk := WrapKey(shared)
	if hex.EncodeToString(wk) != v.WrapKey {
		t.Errorf("wrap key: got %s want %s", hex.EncodeToString(wk), v.WrapKey)
	}
	if WrapAAD(v.AccountID, v.DeviceID, 1) != v.WrapAAD {
		t.Errorf("wrap aad: got %s want %s", WrapAAD(v.AccountID, v.DeviceID, 1), v.WrapAAD)
	}
	if _, data, err := SealWith(wk, make([]byte, 12), v.WrapAAD, ekey); err != nil || data != v.WrapData {
		t.Errorf("the wrap must encrypt to the stored ciphertext: %v %s", err, data)
	}
	if plain, err := Unseal(wk, v.WrapAAD, v.WrapNonce, v.WrapData); err != nil || hex.EncodeToString(plain) != v.EpochKey1 {
		t.Errorf("the wrap must open to the epoch key: %v %s", err, plain)
	}

	bkey := KindKey(ekey, KindSettings)
	if BlobAAD(v.AccountID, KindSettings, 1, 1) != v.BlobAAD {
		t.Errorf("blob aad: got %s want %s", BlobAAD(v.AccountID, KindSettings, 1, 1), v.BlobAAD)
	}
	plain, err := Unseal(bkey, v.BlobAAD, v.BlobNonce, v.BlobData)
	if err != nil || string(plain) != `{"theme":"nuit"}` {
		t.Errorf("the blob must open to its value: %v %s", err, plain)
	}
	// A blob cannot be moved to another account, kind, epoch or seq: the associated data is
	// part of the ciphertext.
	for _, aad := range []string{BlobAAD("another-account", KindSettings, 1, 1), BlobAAD(v.AccountID, KindVault, 1, 1), BlobAAD(v.AccountID, KindSettings, 2, 1), BlobAAD(v.AccountID, KindSettings, 1, 2)} {
		if _, err := Unseal(bkey, aad, v.BlobNonce, v.BlobData); err == nil {
			t.Errorf("the blob must not open with the associated data %q", aad)
		}
	}

	// The canonical forms the device key signs.
	podkey := KeyFromSeed(deviceSeed)
	rec := &SyncKeyRecord{Account: v.AccountID, Device: v.DeviceID, Key: v.PodXPub}
	if rec.Canonical() != v.SyncKeyCanonical {
		t.Errorf("sync key canonical:\n got %s\nwant %s", rec.Canonical(), v.SyncKeyCanonical)
	}
	if Sign(podkey, []byte(rec.Canonical())) != v.SyncKeySig {
		t.Errorf("sync key signature: got %s want %s", Sign(podkey, []byte(rec.Canonical())), v.SyncKeySig)
	}
	blob := &BlobRecord{Account: v.AccountID, Kind: KindSettings, Epoch: 1, Seq: 1, Nonce: v.BlobNonce, Data: v.BlobData}
	if blob.Canonical() != v.BlobCanonical {
		t.Errorf("blob canonical:\n got %s\nwant %s", blob.Canonical(), v.BlobCanonical)
	}
	if Sign(podkey, []byte(blob.Canonical())) != v.BlobSig {
		t.Errorf("blob signature: got %s want %s", Sign(podkey, []byte(blob.Canonical())), v.BlobSig)
	}
	wrap := &WrapRecord{Account: v.AccountID, Device: v.DeviceID, Epoch: 1, Key: v.AdminXPub, Nonce: v.WrapNonce, Data: v.WrapData}
	if wrap.Canonical() != v.WrapCanonical {
		t.Errorf("wrap canonical:\n got %s\nwant %s", wrap.Canonical(), v.WrapCanonical)
	}
	if !Verify(v.AccountID, v.WrapSig, []byte(wrap.Canonical())) {
		t.Error("the wrap signature must verify against the account id")
	}
}
