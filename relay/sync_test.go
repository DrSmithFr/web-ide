package relay

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/coder/websocket"

	"antennae.space/ide/lib/identity"
)

// readId reads the next message of a link, with a deadline: the relay walks its links in any
// order, so a test that waits for a pushed message on several links must not hang on the wrong
// one.
func (c *wsClient) readId(t *testing.T, wait time.Duration) (idMsg, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), wait)
	defer cancel()
	_, b, err := c.c.Read(ctx)
	if err != nil {
		return idMsg{}, err
	}
	var m idMsg
	if err := json.Unmarshal(b, &m); err != nil {
		return idMsg{}, err
	}
	return m, nil
}

// The sync store holds ciphertext only: the relay can check a signature against a device id it
// knows belongs to the account, it cannot read a value and cannot make a wrap.

func TestStoreSyncCAS(t *testing.T) {
	st, err := OpenStore(filepath.Join(t.TempDir(), "relay.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	a := testAccount(t)
	dev := identity.KeyFromSeed(identity.DeviceSeed(a.Seed))

	// The device must hold a certificate of the account for the relay to check its signature.
	if err := st.StoreDevice(identity.NewCert(a, dev, "laptop", identity.KindPod)); err != nil {
		t.Fatal(err)
	}

	blob := &identity.BlobRecord{Account: a.ID, Kind: identity.KindSettings, Epoch: 0, Seq: 1, Device: identity.KeyID(dev),
		Op: identity.OpPut, Nonce: "AAAAAAAAAAAAAAAA", Data: "sgADJqePjNes-0dpZmFfoSX6pHJ844UmbtEsukYZDU8"}
	blob.Sig = identity.Sign(dev, []byte(blob.Canonical()))

	if kept, err := st.PutBlob(blob); err != nil || !kept {
		t.Fatalf("the first write must be stored (kept=%v err=%v)", kept, err)
	}
	got, updated, err := st.Blob(a.ID, identity.KindSettings)
	if err != nil || got == nil || got.Seq != 1 || got.Data != blob.Data || updated == 0 {
		t.Fatalf("the stored blob must be the write: %+v %d %v", got, updated, err)
	}

	// Compare-and-swap: seq must be stored+1.
	replay := *blob
	replay.Sig = identity.Sign(dev, []byte(replay.Canonical()))
	if kept, err := st.PutBlob(&replay); err != nil || kept {
		t.Fatalf("a replayed seq must not be kept (kept=%v err=%v)", kept, err)
	}
	skip := *blob
	skip.Seq = 3
	skip.Data = "another-ciphertext"
	skip.Sig = identity.Sign(dev, []byte(skip.Canonical()))
	if kept, err := st.PutBlob(&skip); err != nil || kept {
		t.Fatalf("a skipped seq must not be kept (kept=%v err=%v)", kept, err)
	}
	next := *blob
	next.Seq = 2
	next.Data = "the-next-ciphertext"
	next.Sig = identity.Sign(dev, []byte(next.Canonical()))
	if kept, err := st.PutBlob(&next); err != nil || !kept {
		t.Fatalf("the next seq must be kept (kept=%v err=%v)", kept, err)
	}
	if got, _, err := st.Blob(a.ID, identity.KindSettings); err != nil || got.Seq != 2 || got.Data != next.Data {
		t.Fatalf("the blob must hold the newest write: %+v %v", got, err)
	}

	// A signature made by another key is refused: the relay checks it against the device id.
	forged := next
	forged.Seq = 3
	forged.Sig = identity.Sign(a.Priv, []byte(forged.Canonical())) // not signed by the device it names
	if kept, err := st.PutBlob(&forged); err == nil || kept {
		t.Fatalf("a blob signed by another key must be refused (kept=%v err=%v)", kept, err)
	}

	// A device with no certificate of the account cannot write.
	other := identity.KeyFromSeed(identity.Derive(a.Seed, "another device"))
	unknown := &identity.BlobRecord{Account: a.ID, Kind: identity.KindSettings, Epoch: 0, Seq: 3, Device: identity.KeyID(other),
		Op: identity.OpPut, Nonce: "AAAAAAAAAAAAAAAA", Data: "ciphertext"}
	unknown.Sig = identity.Sign(other, []byte(unknown.Canonical()))
	if kept, err := st.PutBlob(unknown); err == nil || kept {
		t.Fatalf("a device the relay cannot verify must not write (kept=%v err=%v)", kept, err)
	}

	// A blob of another account is refused.
	second, err := identity.FromPhrase("abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon about")
	if err != nil {
		t.Fatal(err)
	}
	foreign := &identity.BlobRecord{Account: second.ID, Kind: identity.KindSettings, Epoch: 0, Seq: 1, Device: identity.KeyID(dev),
		Op: identity.OpPut, Nonce: "AAAAAAAAAAAAAAAA", Data: "ciphertext"}
	foreign.Sig = identity.Sign(dev, []byte(foreign.Canonical()))
	if kept, err := st.PutBlob(foreign); err == nil || kept {
		t.Fatalf("a blob claiming another account must be refused (kept=%v err=%v)", kept, err)
	}

	// The size cap.
	big := next
	big.Seq = 3
	big.Data = string(make([]byte, maxBlobSize+1))
	big.Sig = identity.Sign(dev, []byte(big.Canonical()))
	if kept, err := st.PutBlob(&big); err == nil || kept {
		t.Fatalf("a blob over the cap must be refused (kept=%v err=%v)", kept, err)
	}

	// A deletion is a write too: it needs the next seq and the signature of the device.
	del := &identity.BlobRecord{Account: a.ID, Kind: identity.KindSettings, Epoch: 0, Seq: 3, Device: identity.KeyID(dev),
		Op: identity.OpDel, Nonce: "AAAAAAAAAAAAAAAA", Data: ""}
	del.Sig = identity.Sign(dev, []byte(del.Canonical()))
	if kept, err := st.PutBlob(del); err != nil || !kept {
		t.Fatalf("the deletion must be stored (kept=%v err=%v)", kept, err)
	}
	if got, _, err := st.Blob(a.ID, identity.KindSettings); err != nil || got != nil {
		t.Fatalf("the blob must be gone: %+v %v", got, err)
	}
	// A write cannot be replayed as a deletion: the op is part of the signed bytes.
	rewritten := *del
	rewritten.Op = identity.OpPut
	rewritten.Data = "ciphertext"
	rewritten.Sig = identity.Sign(dev, []byte(del.Canonical()))
	if kept, err := st.PutBlob(&rewritten); err == nil || kept {
		t.Fatalf("a signature of another op must be refused (kept=%v err=%v)", kept, err)
	}
}

func TestStoreSyncEpoch(t *testing.T) {
	st, err := OpenStore(filepath.Join(t.TempDir(), "relay.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	a := testAccount(t)
	dev := identity.KeyFromSeed(identity.DeviceSeed(a.Seed))
	if err := st.StoreDevice(identity.NewCert(a, dev, "laptop", identity.KindPod)); err != nil {
		t.Fatal(err)
	}

	// A revocation list moves the epoch; a blob of an older epoch is refused.
	r := &identity.Revocations{Account: a.ID, Seq: 1, Epoch: 1, Revoked: []string{identity.KeyID(dev)}, Created: time.Now().Unix()}
	if err := identity.SignRevocations(a.Priv, r); err != nil {
		t.Fatal(err)
	}
	if kept, err := st.SaveRevocations(r); err != nil || !kept {
		t.Fatalf("the list must be stored (kept=%v err=%v)", kept, err)
	}
	if epoch, err := st.SyncEpoch(a.ID); err != nil || epoch != 1 {
		t.Fatalf("the account must be at epoch 1: %d %v", epoch, err)
	}

	blob := &identity.BlobRecord{Account: a.ID, Kind: identity.KindSettings, Epoch: 0, Seq: 1, Device: identity.KeyID(dev),
		Op: identity.OpPut, Nonce: "AAAAAAAAAAAAAAAA", Data: "ciphertext"}
	blob.Sig = identity.Sign(dev, []byte(blob.Canonical()))
	if kept, err := st.PutBlob(blob); err == nil || kept {
		t.Fatalf("a blob of an older epoch must be refused (kept=%v err=%v)", kept, err)
	}

	// The device on the list cannot write, even for the current epoch.
	now := *blob
	now.Epoch = 1
	now.Sig = identity.Sign(dev, []byte(now.Canonical()))
	if kept, err := st.PutBlob(&now); err == nil || kept {
		t.Fatalf("a revoked device must not write (kept=%v err=%v)", kept, err)
	}

	// A device that is not revoked can write for the current epoch.
	keptDevice := identity.KeyFromSeed(identity.Derive(a.Seed, "the kept device"))
	if err := st.StoreDevice(identity.NewCert(a, keptDevice, "phone", identity.KindBrowser)); err != nil {
		t.Fatal(err)
	}
	ok := &identity.BlobRecord{Account: a.ID, Kind: identity.KindVault, Epoch: 1, Seq: 1, Device: identity.KeyID(keptDevice),
		Op: identity.OpPut, Nonce: "AAAAAAAAAAAAAAAA", Data: "ciphertext"}
	ok.Sig = identity.Sign(keptDevice, []byte(ok.Canonical()))
	if kept, err := st.PutBlob(ok); err != nil || !kept {
		t.Fatalf("a device of the account must write for the current epoch (kept=%v err=%v)", kept, err)
	}

	// A revoked device cannot publish its X25519 key either.
	xseed := identity.Derive(identity.DeviceSeed(a.Seed), identity.InfoX25519)
	x, err := identity.X25519FromSeed(xseed)
	if err != nil {
		t.Fatal(err)
	}
	rec := &identity.SyncKeyRecord{Account: a.ID, Device: identity.KeyID(dev), Key: identity.X25519ID(x.PublicKey())}
	rec.Sig = identity.Sign(dev, []byte(rec.Canonical()))
	if err := st.StoreSyncKey(rec); err == nil {
		t.Fatal("a revoked device must not publish a key")
	}
}

func TestStoreSyncKeysAndWraps(t *testing.T) {
	st, err := OpenStore(filepath.Join(t.TempDir(), "relay.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	a := testAccount(t)
	dev := identity.KeyFromSeed(identity.DeviceSeed(a.Seed))
	if err := st.StoreDevice(identity.NewCert(a, dev, "laptop", identity.KindPod)); err != nil {
		t.Fatal(err)
	}

	// The published X25519 key must be signed by the device it names.
	xseed := identity.Derive(identity.DeviceSeed(a.Seed), identity.InfoX25519)
	x, err := identity.X25519FromSeed(xseed)
	if err != nil {
		t.Fatal(err)
	}
	rec := &identity.SyncKeyRecord{Account: a.ID, Device: identity.KeyID(dev), Key: identity.X25519ID(x.PublicKey())}
	rec.Sig = identity.Sign(dev, []byte(rec.Canonical()))
	if err := st.StoreSyncKey(rec); err != nil {
		t.Fatalf("the published key must be stored: %v", err)
	}
	swapped := *rec
	swapped.Key = "c7LYt2qptTZgAyvI9di-46OuTjs6f9Sa3oH3NHo0qmg" // the relay swapped the key
	if err := st.StoreSyncKey(&swapped); err == nil {
		t.Fatal("a swapped key must not match its signature")
	}
	// A device the relay cannot verify cannot publish a key.
	other := identity.KeyFromSeed(identity.Derive(a.Seed, "another device"))
	unknown := &identity.SyncKeyRecord{Account: a.ID, Device: identity.KeyID(other), Key: identity.X25519ID(x.PublicKey())}
	unknown.Sig = identity.Sign(other, []byte(unknown.Canonical()))
	if err := st.StoreSyncKey(unknown); err == nil {
		t.Fatal("a device with no certificate must not publish a key")
	}
	list, err := st.SyncKeys(a.ID)
	if err != nil || len(list) != 1 || list[0].Key != rec.Key {
		t.Fatalf("the stored key must be the verified one: %+v %v", list, err)
	}

	// A wrap can only come from a device holding the account key: the relay checks the signature
	// against the account id, it cannot make one.
	wrap := &identity.WrapRecord{Account: a.ID, Device: identity.KeyID(dev), Epoch: 0, Key: "c7LYt2qptTZgAyvI9di-46OuTjs6f9Sa3oH3NHo0qmg",
		Nonce: "AAAAAAAAAAAAAAAA", Data: "7Gsmn0AwTAksqO2Wj7pIus-HK9tMpvZk0K5s_Jjl0WvolUEq-lCdjN4NogeTDPrn"}
	wrap.Sig = identity.Sign(a.Priv, []byte(wrap.Canonical()))
	if err := st.PutWrap(wrap); err != nil {
		t.Fatalf("a wrap signed by the account must be stored: %v", err)
	}
	tampered := *wrap
	tampered.Epoch = 1
	tampered.Sig = identity.Sign(a.Priv, []byte(wrap.Canonical())) // the signature of the original wrap
	if err := st.PutWrap(&tampered); err == nil {
		t.Fatal("a tampered wrap must be refused")
	}
	// A wrap signed by a device key is refused: only the account key signs one.
	devsigned := *wrap
	devsigned.Sig = identity.Sign(dev, []byte(wrap.Canonical()))
	if err := st.PutWrap(&devsigned); err == nil {
		t.Fatal("a wrap signed by a device key must be refused")
	}
	got, _, err := st.Wrap(a.ID, identity.KeyID(dev))
	if err != nil || got == nil || got.Data != wrap.Data {
		t.Fatalf("the stored wrap must be the verified one: %+v %v", got, err)
	}
	list2, err := st.Wraps(a.ID)
	if err != nil || len(list2) != 1 {
		t.Fatalf("the wraps of the account must be listed: %+v %v", list2, err)
	}
}

// TestSyncNoClearText: what the relay can see. The blob is stored as it came, the relay holds no
// sync key, so a value cannot be read out of the base.
func TestSyncNoClearText(t *testing.T) {
	st, err := OpenStore(filepath.Join(t.TempDir(), "relay.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	a := testAccount(t)
	dev := identity.KeyFromSeed(identity.DeviceSeed(a.Seed))
	if err := st.StoreDevice(identity.NewCert(a, dev, "laptop", identity.KindPod)); err != nil {
		t.Fatal(err)
	}

	// The ciphertext of the shared vectors, encrypted with the settings key of epoch 1.
	r := &identity.Revocations{Account: a.ID, Seq: 1, Epoch: 1, Revoked: []string{}, Created: time.Now().Unix()}
	if err := identity.SignRevocations(a.Priv, r); err != nil {
		t.Fatal(err)
	}
	if _, err := st.SaveRevocations(r); err != nil {
		t.Fatal(err)
	}
	ekey := identity.EpochKey(a.SyncSeed(), 1)
	bkey := identity.KindKey(ekey, identity.KindSettings)
	nonce, data, err := identity.Seal(bkey, identity.BlobAAD(a.ID, identity.KindSettings, 1, 1), []byte(`{"theme":"nuit"}`))
	if err != nil {
		t.Fatal(err)
	}
	blob := &identity.BlobRecord{Account: a.ID, Kind: identity.KindSettings, Epoch: 1, Seq: 1, Device: identity.KeyID(dev),
		Op: identity.OpPut, Nonce: nonce, Data: data}
	blob.Sig = identity.Sign(dev, []byte(blob.Canonical()))
	if kept, err := st.PutBlob(blob); err != nil || !kept {
		t.Fatalf("the blob must be stored (kept=%v err=%v)", kept, err)
	}

	stored, _, err := st.Blob(a.ID, identity.KindSettings)
	if err != nil || stored == nil {
		t.Fatalf("the blob must be readable: %+v %v", stored, err)
	}
	// Only a device holding the sync key can read it back.
	if plain, err := identity.Unseal(bkey, identity.BlobAAD(a.ID, identity.KindSettings, 1, 1), stored.Nonce, stored.Data); err != nil || string(plain) != `{"theme":"nuit"}` {
		t.Fatalf("the pod must open the stored blob: %v %s", err, plain)
	}
	if _, err := identity.Unseal(identity.KindKey(ekey, identity.KindVault), identity.BlobAAD(a.ID, identity.KindSettings, 1, 1), stored.Nonce, stored.Data); err == nil {
		t.Error("the vault key must not open the settings blob")
	}
	if _, err := identity.Unseal(bkey, identity.BlobAAD(a.ID, identity.KindSettings, 1, 2), stored.Nonce, stored.Data); err == nil {
		t.Error("a blob must not open with the associated data of another seq")
	}

	// The stored text holds no value: the row is read as text and searched.
	var raw string
	if err := st.db.QueryRow("SELECT data FROM blobs WHERE account=? AND kind=?", a.ID, identity.KindSettings).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if raw == `{"theme":"nuit"}` {
		t.Fatal("the stored blob must be the ciphertext, not the value")
	}
	for _, word := range []string{"nuit", "theme"} {
		if contains(raw, word) {
			t.Errorf("the stored blob must not hold %q in clear", word)
		}
	}
	// The relay never stored a sync key: nothing in the base is a key derived from the phrase.
	for _, key := range []string{hexKey(identity.KindKey(ekey, identity.KindSettings)), hexKey(ekey)} {
		if contains(storedText(t, st, "SELECT data FROM blobs WHERE account=? AND kind=?", a.ID, identity.KindSettings), key) {
			t.Errorf("the base must not hold the sync key %q", key)
		}
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func hexKey(b []byte) string {
	const hex = "0123456789abcdef"
	out := make([]byte, 0, len(b)*2)
	for _, x := range b {
		out = append(out, hex[x>>4], hex[x&15])
	}
	return string(out)
}

func storedText(t *testing.T, st *Store, query string, args ...any) string {
	t.Helper()
	var s string
	if err := st.db.QueryRow(query, args...).Scan(&s); err != nil {
		t.Fatal(err)
	}
	return s
}

// TestSyncMessages: the sync rendezvous over the admin link — a device posts its X25519 key, an
// admin wraps the epoch key for it, the wrap is pushed to the device, a pod reads the wrap made
// for it, writes a blob and the other link of the account gets the pushed write.
func TestSyncMessages(t *testing.T) {
	ts := newRelay(t)
	a := testAccount(t)
	dev := identity.KeyFromSeed(identity.DeviceSeed(a.Seed))
	podKey := identity.KeyFromSeed(identity.Derive(a.Seed, "the pod key"))

	joiner := connectIdentity(t, ts, a.ID, identity.NewCert(a, dev, "phone", identity.KindBrowser))
	defer joiner.close()
	if m := joiner.nextId(t); m.Type != "ready" {
		t.Fatalf("the joiner must be ready, got %q", m.Type)
	}

	// The device publishes its X25519 key, signed by its device key.
	xseed := identity.Derive(identity.DeviceSeed(a.Seed), identity.InfoX25519)
	x, err := identity.X25519FromSeed(xseed)
	if err != nil {
		t.Fatal(err)
	}
	rec := &identity.SyncKeyRecord{Account: a.ID, Device: identity.KeyID(dev), Key: identity.X25519ID(x.PublicKey())}
	rec.Sig = identity.Sign(dev, []byte(rec.Canonical()))
	joiner.sendId(t, idMsg{Type: "syncKey", SyncKey: rec})
	if m := joiner.nextId(t); m.Type != "syncKey" || m.Message != "stored" {
		t.Fatalf("the published key must be stored, got %q %q", m.Type, m.Message)
	}

	// The admin reads the published key and wraps the epoch key for the device.
	admin := connectIdentity(t, ts, a.ID, nil)
	defer admin.close()
	if m := admin.nextId(t); m.Type != "ready" {
		t.Fatalf("the admin must be ready, got %q", m.Type)
	}
	admin.sendId(t, idMsg{Type: "syncKeys"})
	m := admin.nextId(t)
	if m.Type != "syncKeys" || len(m.SyncKeys) != 1 || m.SyncKeys[0].Key != rec.Key {
		t.Fatalf("the admin must read the published key, got %q %+v", m.Type, m.SyncKeys)
	}

	aseed := make([]byte, 32)
	for i := range aseed {
		aseed[i] = 11
	}
	ak, err := identity.X25519FromSeed(aseed)
	if err != nil {
		t.Fatal(err)
	}
	shared, err := identity.Shared(ak, rec.Key)
	if err != nil {
		t.Fatal(err)
	}
	wk := identity.WrapKey(shared)
	ekey := identity.EpochKey(a.SyncSeed(), 0)
	nonce, data, err := identity.Seal(wk, identity.WrapAAD(a.ID, identity.KeyID(dev), 0), ekey)
	if err != nil {
		t.Fatal(err)
	}
	wrap := &identity.WrapRecord{Account: a.ID, Device: identity.KeyID(dev), Epoch: 0, Key: identity.X25519ID(ak.PublicKey()), Nonce: nonce, Data: data}
	wrap.Sig = identity.Sign(a.Priv, []byte(wrap.Canonical()))
	admin.sendId(t, idMsg{Type: "wrap", Wrap: wrap})
	if m := admin.nextId(t); m.Type != "wrap" || m.Message != "stored" {
		t.Fatalf("the wrap must be stored, got %q %q", m.Type, m.Message)
	}

	// The device gets the pushed wrap and opens it with its own key: the wrap is the only way a
	// device that does not know the phrase gets the epoch key.
	m = joiner.nextId(t)
	if m.Type != "wrap" || m.Wrap == nil {
		t.Fatalf("the joiner must get the pushed wrap, got %q %+v", m.Type, m.Wrap)
	}
	got, err := identity.Unseal(wk, identity.WrapAAD(a.ID, identity.KeyID(dev), 0), m.Wrap.Nonce, m.Wrap.Data)
	if err != nil || string(got) != string(ekey) {
		t.Fatalf("the wrap must open to the epoch key: %v %s", err, got)
	}

	// A pod reads the wrap made for it and writes a blob; the other link of the account gets the
	// pushed write.
	podLink := connectIdentity(t, ts, a.ID, identity.NewCert(a, podKey, "laptop", identity.KindPod))
	defer podLink.close()
	if m := podLink.nextId(t); m.Type != "ready" {
		t.Fatalf("the pod must be ready, got %q", m.Type)
	}
	podLink.sendId(t, idMsg{Type: "wrapGet", Device: identity.KeyID(podKey)})
	if m := podLink.nextId(t); m.Type != "wrap" || m.Wrap != nil {
		t.Fatalf("a pod with no wrap yet must get nothing, got %q %+v", m.Type, m.Wrap)
	}

	blob := &identity.BlobRecord{Account: a.ID, Kind: identity.KindSettings, Epoch: 0, Seq: 1, Device: identity.KeyID(podKey),
		Op: identity.OpPut, Nonce: "AAAAAAAAAAAAAAAA", Data: "ciphertext-of-the-settings"}
	blob.Sig = identity.Sign(podKey, []byte(blob.Canonical()))
	podLink.sendId(t, idMsg{Type: "blobPut", Blob: blob})
	if m := podLink.nextId(t); m.Type != "blob" || m.Message != "stored" {
		t.Fatalf("the write must be stored, got %q %q", m.Type, m.Message)
	}
	// Both other links of the account get the pushed write: the admin link too.
	for _, c := range []*wsClient{joiner, admin} {
		m, err := c.readId(t, 2*time.Second)
		if err != nil || m.Type != "blob" || m.Blob == nil || m.Blob.Data != blob.Data {
			t.Fatalf("the other links must get the pushed write, got %q (%v)", m.Type, err)
		}
	}

	// A CAS failure answers "older" without changing the stored blob.
	replay := *blob
	replay.Sig = identity.Sign(podKey, []byte(replay.Canonical()))
	podLink.sendId(t, idMsg{Type: "blobPut", Blob: &replay})
	if m := podLink.nextId(t); m.Type != "blob" || m.Message != "older" {
		t.Fatalf("a replayed seq must answer older, got %q %q", m.Type, m.Message)
	}

	// A revoked device cannot write.
	r := &identity.Revocations{Account: a.ID, Seq: 1, Epoch: 1, Revoked: []string{identity.KeyID(podKey)}, Created: time.Now().Unix()}
	if err := identity.SignRevocations(a.Priv, r); err != nil {
		t.Fatal(err)
	}
	admin.sendId(t, idMsg{Type: "revoke", List: r})
	if m := admin.nextId(t); m.Type != "revoked" {
		t.Fatalf("the admin must get the list back, got %q", m.Type)
	}
	// Both links of the account get the pushed list, in any order (the relay walks its links).
	for _, c := range []*wsClient{joiner, podLink} {
		m, err := c.readId(t, 2*time.Second)
		if err != nil || m.Type != "revocations" {
			t.Fatalf("the links must get the pushed list, got %q (%v)", m.Type, err)
		}
	}
	revokedBlob := &identity.BlobRecord{Account: a.ID, Kind: identity.KindSettings, Epoch: 1, Seq: 2, Device: identity.KeyID(podKey),
		Op: identity.OpPut, Nonce: "AAAAAAAAAAAAAAAA", Data: "ciphertext"}
	revokedBlob.Sig = identity.Sign(podKey, []byte(revokedBlob.Canonical()))
	podLink.sendId(t, idMsg{Type: "blobPut", Blob: revokedBlob})
	if m := podLink.nextId(t); m.Type != "error" {
		t.Fatalf("a revoked device must not write, got %q %q", m.Type, m.Message)
	}

	// The stored blob is still the one written before the revocation.
	admin.sendId(t, idMsg{Type: "blobGet", Kind: identity.KindSettings})
	m = admin.nextId(t)
	if m.Type != "blob" || m.Blob == nil || m.Blob.Seq != 1 {
		t.Fatalf("the stored blob must be unchanged, got %q %+v", m.Type, m.Blob)
	}
}

// TestSyncPodLink: the sync messages on the pod link. An unpaired pod has no sync key, so they are
// refused; a paired pod publishes its key, reads the wrap made for it and writes.
func TestSyncPodLink(t *testing.T) {
	ts := newRelay(t)
	a := testAccount(t)

	pod, _, err := websocket.Dial(context.Background(), wsURL(ts, "/_relay/pod"), nil)
	if err != nil {
		t.Fatal("dialing the pod link:", err)
	}
	defer pod.CloseNow()
	c := &wsClient{pod}

	// An unpaired pod: no account, so no sync.
	c.send(t, sigMsg{Type: "register", ID: "pod-a", Secret: "secret-1", Name: "unpaired"})
	if m := c.next(t); m.Type != "ready" {
		t.Fatalf("the pod must be ready, got %q", m.Type)
	}
	c.send(t, sigMsg{Type: "blobGet", Kind: identity.KindSettings})
	if m := c.next(t); m.Type != "error" {
		t.Fatalf("an unpaired pod must be refused, got %q %q", m.Type, m.Message)
	}

	// A paired pod registers with its certificate.
	podKey := identity.KeyFromSeed(identity.Derive(a.Seed, "the paired pod"))
	pod2, _, err := websocket.Dial(context.Background(), wsURL(ts, "/_relay/pod"), nil)
	if err != nil {
		t.Fatal("dialing the second pod link:", err)
	}
	defer pod2.CloseNow()
	p := &wsClient{pod2}
	p.send(t, sigMsg{Type: "register", ID: "pod-b", Secret: "secret-2", Name: "paired",
		Account: a.ID, Cert: identity.NewCert(a, podKey, "paired pod", identity.KindPod)})
	if m := p.next(t); m.Type != "ready" {
		t.Fatalf("the paired pod must be ready, got %q", m.Type)
	}

	xseed := identity.Derive(identity.DeviceSeed(a.Seed), identity.InfoX25519)
	x, err := identity.X25519FromSeed(xseed)
	if err != nil {
		t.Fatal(err)
	}
	rec := &identity.SyncKeyRecord{Account: a.ID, Device: identity.KeyID(podKey), Key: identity.X25519ID(x.PublicKey())}
	rec.Sig = identity.Sign(podKey, []byte(rec.Canonical()))
	p.send(t, sigMsg{Type: "syncKey", SyncKey: rec})
	if m := p.next(t); m.Type != "syncKey" || m.Message != "stored" {
		t.Fatalf("the key of the pod must be stored, got %q %q", m.Type, m.Message)
	}

	// No wrap yet: the pod must be told it has no sync key, not left with an empty answer.
	p.send(t, sigMsg{Type: "wrapGet", Device: identity.KeyID(podKey)})
	if m := p.next(t); m.Type != "wrap" || m.Wrap != nil {
		t.Fatalf("a pod with no wrap must get no wrap, got %q %+v", m.Type, m.Wrap)
	}

	// The admin wraps for the pod, and the pod gets the pushed wrap.
	admin := connectIdentity(t, ts, a.ID, nil)
	defer admin.close()
	if m := admin.nextId(t); m.Type != "ready" {
		t.Fatalf("the admin must be ready, got %q", m.Type)
	}
	aseed := make([]byte, 32)
	for i := range aseed {
		aseed[i] = 7
	}
	ak, err := identity.X25519FromSeed(aseed)
	if err != nil {
		t.Fatal(err)
	}
	shared, err := identity.Shared(ak, rec.Key)
	if err != nil {
		t.Fatal(err)
	}
	wk := identity.WrapKey(shared)
	ekey := identity.EpochKey(a.SyncSeed(), 0)
	nonce, data, err := identity.Seal(wk, identity.WrapAAD(a.ID, identity.KeyID(podKey), 0), ekey)
	if err != nil {
		t.Fatal(err)
	}
	wrap := &identity.WrapRecord{Account: a.ID, Device: identity.KeyID(podKey), Epoch: 0, Key: identity.X25519ID(ak.PublicKey()), Nonce: nonce, Data: data}
	wrap.Sig = identity.Sign(a.Priv, []byte(wrap.Canonical()))
	admin.sendId(t, idMsg{Type: "wrap", Wrap: wrap})
	if m := admin.nextId(t); m.Type != "wrap" || m.Message != "stored" {
		t.Fatalf("the wrap must be stored, got %q %q", m.Type, m.Message)
	}
	pm := p.next(t)
	if pm.Type != "wrap" || pm.Wrap == nil || pm.Device != identity.KeyID(podKey) {
		t.Fatalf("the pod must get the wrap made for it, got %q %+v", pm.Type, pm.Wrap)
	}
	if got, err := identity.Unseal(wk, identity.WrapAAD(a.ID, identity.KeyID(podKey), 0), pm.Wrap.Nonce, pm.Wrap.Data); err != nil || string(got) != string(ekey) {
		t.Fatalf("the pod must open the wrap with its own X25519 key: %v %s", err, got)
	}

	// The pod writes, and the admin link of the account gets the pushed write.
	blob := &identity.BlobRecord{Account: a.ID, Kind: identity.KindSettings, Epoch: 0, Seq: 1, Device: identity.KeyID(podKey),
		Op: identity.OpPut, Nonce: "AAAAAAAAAAAAAAAA", Data: "ciphertext-of-the-settings"}
	blob.Sig = identity.Sign(podKey, []byte(blob.Canonical()))
	p.send(t, sigMsg{Type: "blobPut", Blob: blob})
	if m := p.next(t); m.Type != "blob" || m.Message != "stored" {
		t.Fatalf("the write of the pod must be stored, got %q %q", m.Type, m.Message)
	}
	am := admin.nextId(t)
	if am.Type != "blob" || am.Blob == nil || am.Blob.Data != blob.Data {
		t.Fatalf("the admin must get the pushed write, got %q %+v", am.Type, am.Blob)
	}

	// The unpaired pod still cannot read the blob of an account it does not belong to.
	c.send(t, sigMsg{Type: "blobGet", Kind: identity.KindSettings})
	if m := c.next(t); m.Type != "error" {
		t.Fatalf("an unpaired pod must not read a blob, got %q %+v", m.Type, m.Blob)
	}
}

// TestSyncStoreMigration: a database created by version 2 (no epoch column, no sync tables) is
// brought to 3 without losing the identity tables.
func TestSyncStoreMigration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "relay.db")
	st, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	a := testAccount(t)
	if _, err := st.db.Exec("DROP TABLE blobs"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.db.Exec("ALTER TABLE accounts DROP COLUMN epoch"); err != nil {
		t.Fatal(err)
	}
	if err := migrate(st.db); err != nil {
		t.Fatalf("the sync migration must add the tables back: %v", err)
	}
	if epoch, err := st.SyncEpoch(a.ID); err != nil || epoch != 0 {
		t.Fatalf("the epoch column must be there: %d %v", epoch, err)
	}
	if b, _, err := st.Blob(a.ID, identity.KindSettings); err != nil || b != nil {
		t.Fatalf("the blobs table must be there: %+v %v", b, err)
	}
	st.Close()

	st, err = OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	var ver int
	if err := st.db.QueryRow("PRAGMA user_version").Scan(&ver); err != nil || ver != dbVersion {
		t.Fatalf("the database must be at version %d, got %d (%v)", dbVersion, ver, err)
	}
}

// A wrap opens only for the device it names, so the relay pushes it to that device. A pod must not
// be handed the wrap made for another one: it could not open it, and it would learn which devices
// the account has. A link that never published a sync key is an admin panel: it reads the wraps of
// the whole account, so it still gets the push.
func TestWrapPushedToItsDeviceOnly(t *testing.T) {
	ts := newRelay(t)
	a := testAccount(t)

	podA := connectIdentity(t, ts, a.ID, identity.NewCert(a, identity.KeyFromSeed(identity.Derive(a.Seed, "pod a")), "pod a", identity.KindPod))
	defer podA.close()
	podB := connectIdentity(t, ts, a.ID, identity.NewCert(a, identity.KeyFromSeed(identity.Derive(a.Seed, "pod b")), "pod b", identity.KindPod))
	defer podB.close()
	admin := connectIdentity(t, ts, a.ID, nil)
	defer admin.close()
	panel := connectIdentity(t, ts, a.ID, nil)
	defer panel.close()
	for _, c := range []*wsClient{podA, podB, admin, panel} {
		if m := c.nextId(t); m.Type != "ready" {
			t.Fatalf("the link must be ready, got %q", m.Type)
		}
	}

	xkey := func(seed []byte) string {
		x, err := identity.X25519FromSeed(seed)
		if err != nil {
			t.Fatal(err)
		}
		return identity.X25519ID(x.PublicKey())
	}
	pa := identity.KeyFromSeed(identity.Derive(a.Seed, "pod a"))
	pb := identity.KeyFromSeed(identity.Derive(a.Seed, "pod b"))
	devA, keyA := identity.KeyID(pa), xkey(identity.Derive(identity.DeviceSeed(a.Seed), "x25519 pod a"))
	keyB := xkey(identity.Derive(identity.DeviceSeed(a.Seed), "x25519 pod b"))
	post := func(c *wsClient, priv ed25519.PrivateKey, key string) {
		rec := &identity.SyncKeyRecord{Account: a.ID, Device: identity.KeyID(priv), Key: key}
		rec.Sig = identity.Sign(priv, []byte(rec.Canonical()))
		c.sendId(t, idMsg{Type: "syncKey", SyncKey: rec})
	}

	// The published key is answered to its author and pushed to the other links of the account.
	post(podA, pa, keyA)
	if m := podA.nextId(t); m.Type != "syncKey" || m.Message != "stored" {
		t.Fatalf("the published key must be stored, got %q %q", m.Type, m.Message)
	}
	post(podB, pb, keyB)
	// pod A reads the key pushed by pod B.
	if m := podA.nextId(t); m.Type != "syncKey" || m.Message == "stored" {
		t.Fatalf("pod A must read the key pushed by pod B, got %q", m.Type)
	}
	// pod B reads the key pushed by pod A, then the answer to its own.
	for i := 0; i < 2; i++ {
		m := podB.nextId(t)
		if m.Type != "syncKey" {
			t.Fatalf("pod B must read the published keys, got %q", m.Type)
		}
	}
	for _, c := range []*wsClient{admin, panel} {
		for i := 0; i < 2; i++ {
			if m := c.nextId(t); m.Type != "syncKey" {
				t.Fatalf("the panels must read the published keys, got %q", m.Type)
			}
		}
	}

	// The admin wraps the key of the epoch for pod A only.
	ak, err := identity.X25519FromSeed(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	shared, err := identity.Shared(ak, keyA)
	if err != nil {
		t.Fatal(err)
	}
	ekey := identity.EpochKey(a.SyncSeed(), 0)
	nonce, data, err := identity.Seal(identity.WrapKey(shared), identity.WrapAAD(a.ID, devA, 0), ekey)
	if err != nil {
		t.Fatal(err)
	}
	wrap := &identity.WrapRecord{Account: a.ID, Device: devA, Epoch: 0, Key: identity.X25519ID(ak.PublicKey()), Nonce: nonce, Data: data}
	wrap.Sig = identity.Sign(a.Priv, []byte(wrap.Canonical()))
	admin.sendId(t, idMsg{Type: "wrap", Wrap: wrap})
	if m := admin.nextId(t); m.Type != "wrap" || m.Message != "stored" {
		t.Fatalf("the wrap must be stored, got %q %q", m.Type, m.Message)
	}

	// The device the wrap is for gets it, and a panel that is not a device reads the wraps of the
	// whole account, so it gets the push too.
	for _, c := range []*wsClient{podA, panel} {
		if m, err := c.readId(t, 2*time.Second); err != nil || m.Type != "wrap" || m.Wrap == nil {
			t.Fatalf("the wrap must be pushed to the device it names, got %q (%v)", m.Type, err)
		}
	}
	// pod B cannot open a wrap made for another device: it must not be sent to it.
	if m, err := podB.readId(t, 500*time.Millisecond); err == nil {
		t.Fatalf("pod B must not get a wrap made for another device, got %q %+v", m.Type, m)
	}
}
