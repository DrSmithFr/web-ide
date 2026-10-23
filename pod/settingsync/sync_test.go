package settingsync

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/DrSmithFr/web-ide/lib/identity"
	"github.com/DrSmithFr/web-ide/pod/settings"
	"github.com/DrSmithFr/web-ide/pod/store"
	"github.com/DrSmithFr/web-ide/relay"
)

// The loop tested against the real relay store: two pods of one account, and an admin that wraps
// the key of the epoch for each. The relay here is the same code a pod talks to — it only stores
// ciphertext and checks signatures.

// fakeRelay is the relay as the pods see it: the real store, and a delivery function per pod.
type fakeRelay struct {
	st    *relay.Store
	pods  map[string]*Hub
	cut   map[string]bool // a pod whose link is down: nothing reaches it
	admin *identity.Account
}

// send delivers to a pod. A peer that cannot apply a message does not break the write: the relay
// only logs it, so the delivery errors are dropped here.
func (f *fakeRelay) send(device string, m Msg) {
	if f.cut[device] {
		return
	}
	h := f.pods[device]
	if h == nil {
		return
	}
	_ = h.Handle(m)
}

// run is one message from a pod to the relay, answered like the relay does: only the store can
// refuse a message. A pod whose link is down reaches no one, the relay included.
func (f *fakeRelay) run(from string, m Msg) error {
	if f.cut[from] {
		return nil
	}
	switch m.Type {
	case "syncKey":
		if err := f.st.StoreSyncKey(m.SyncKey); err != nil {
			return err
		}
		if err := f.wrapFor(m.Device); err != nil {
			return err
		}
		for d := range f.pods {
			if d != from {
				f.send(d, Msg{Type: "syncKey", SyncKey: m.SyncKey})
			}
		}
		return nil
	case "wrapGet":
		w, updated, err := f.st.Wrap(m.Account, m.Device)
		if err != nil {
			return err
		}
		f.send(from, Msg{Type: "wrap", Device: m.Device, Wrap: w, Updated: updated})
		return nil
	case "blobGet":
		b, updated, err := f.st.Blob(m.Account, m.Kind)
		if err != nil {
			return err
		}
		f.send(from, Msg{Type: "blob", Kind: m.Kind, Blob: b, Updated: updated})
		return nil
	case "blobPut":
		kept, err := f.st.PutBlob(m.Blob)
		if err != nil {
			return err
		}
		msg := "older"
		if kept {
			msg = "stored"
		}
		f.send(from, Msg{Type: "blob", Kind: m.Kind, Blob: m.Blob, Message: msg})
		if kept {
			for d := range f.pods {
				if d != from {
					f.send(d, Msg{Type: "blob", Kind: m.Kind, Blob: m.Blob})
				}
			}
		}
		return nil
	}
	return nil
}

// wrapFor is the admin browser of the Members panel: it reads the published key of a device,
// derives the key of the current epoch from the phrase, and posts the wrap.
func (f *fakeRelay) wrapFor(device string) error {
	rec, err := f.st.SyncKey(f.admin.ID, device)
	if err != nil || rec == nil {
		return err
	}
	epoch, err := f.st.SyncEpoch(f.admin.ID)
	if err != nil {
		return err
	}
	aseed := make([]byte, 32)
	for i := range aseed {
		aseed[i] = 5
	}
	ak, err := identity.X25519FromSeed(aseed)
	if err != nil {
		return err
	}
	shared, err := identity.Shared(ak, rec.Key)
	if err != nil {
		return err
	}
	wk := identity.WrapKey(shared)
	ekey := identity.EpochKey(f.admin.SyncSeed(), epoch)
	nonce, data, err := identity.Seal(wk, identity.WrapAAD(f.admin.ID, device, epoch), ekey)
	if err != nil {
		return err
	}
	w := &identity.WrapRecord{Account: f.admin.ID, Device: device, Epoch: epoch, Key: identity.X25519ID(ak.PublicKey()), Nonce: nonce, Data: data}
	w.Sig = identity.Sign(f.admin.Priv, []byte(w.Canonical()))
	if err := f.st.PutWrap(w); err != nil {
		return err
	}
	f.send(device, Msg{Type: "wrap", Device: device, Wrap: w})
	return nil
}

// pod is one pod of the test: its identity, its settings, its sync loop.
type pod struct {
	id    string
	hub   *Hub
	sets  *settings.Settings
	store *store.Store
}

func newPod(t *testing.T, f *fakeRelay, name string) *pod {
	t.Helper()
	// One folder per pod: two pods of the same test must not share their device key.
	dir := filepath.Join(t.TempDir(), name)
	p, err := identity.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	sets, err := settings.Load(st)
	if err != nil {
		t.Fatal(err)
	}
	// The pod is paired: its certificate is the one the admin signed for it.
	cert := identity.NewCert(f.admin, p.Key(), name, identity.KindPod)
	if err := p.Pin(f.admin.ID); err != nil {
		t.Fatal(err)
	}
	if err := p.SetCert(cert); err != nil {
		t.Fatal(err)
	}
	if err := f.st.StoreDevice(cert); err != nil {
		t.Fatal(err)
	}
	flags := NewFlags(sets)
	h := NewHub(st, &testTransport{f, p.ID()}, p, flags)
	if _, err := h.AddSource(NewSettingsSource(sets, func() bool { return flags.On(identity.KindSettings) }, nil)); err != nil {
		t.Fatal(err)
	}
	f.pods[p.ID()] = h
	return &pod{id: p.ID(), hub: h, sets: sets, store: st}
}

type testTransport struct {
	f      *fakeRelay
	device string
}

func (tr *testTransport) Send(m Msg) error { return tr.f.run(tr.device, m) }

func phrase() (*identity.Account, error) {
	return identity.FromPhrase("lecture vapor excuse note pledge rough bundle start regular burden reveal theme banana pencil owner cube social job emotion member joy sting dash whisper")
}

func content(t *testing.T, s *settings.Settings) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal(s.Current().Settings, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestSyncTwoPods(t *testing.T) {
	a, err := phrase()
	if err != nil {
		t.Fatal(err)
	}
	st, err := relay.OpenStore(filepath.Join(t.TempDir(), "relay.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	f := &fakeRelay{st: st, pods: map[string]*Hub{}, cut: map[string]bool{}, admin: a}

	aPod := newPod(t, f, "laptop")
	bPod := newPod(t, f, "phone")

	// Both pods publish their key and get the wrap; A writes first.
	if err := aPod.hub.Publish(); err != nil {
		t.Fatal(err)
	}
	if err := bPod.hub.Publish(); err != nil {
		t.Fatal(err)
	}
	if !aPod.hub.hasKey() || !bPod.hub.hasKey() {
		t.Fatal("both pods must have the key of the epoch")
	}

	// A theme change on A reaches B.
	if _, err := aPod.sets.Save(json.RawMessage(`{"theme":"nuit","language":"en"}`), "Initial settings"); err != nil {
		t.Fatal(err)
	}
	if err := aPod.hub.Source(identity.KindSettings).Push(); err != nil {
		t.Fatal(err)
	}
	if got := content(t, bPod.sets); got["theme"] != "nuit" || got["language"] != "en" {
		t.Fatalf("the theme change must reach B: %v", got)
	}
	if bPod.hub.Source(identity.KindSettings).Status().LastSync == 0 {
		t.Fatal("B must record the time of the sync")
	}

	// A change on B comes back to A.
	if _, err := bPod.sets.Save(json.RawMessage(`{"theme":"nuit","language":"fr"}`), "Language"); err != nil {
		t.Fatal(err)
	}
	if err := bPod.hub.Source(identity.KindSettings).Push(); err != nil {
		t.Fatal(err)
	}
	if got := content(t, aPod.sets); got["language"] != "fr" || got["theme"] != "nuit" {
		t.Fatalf("the language change must come back to A: %v", got)
	}

	// Different fields changed on both sides merge silently.
	if _, err := aPod.sets.Save(json.RawMessage(`{"theme":"nuit","language":"fr","editor":{"tabSize":2}}`), "Tab size"); err != nil {
		t.Fatal(err)
	}
	if err := aPod.hub.Source(identity.KindSettings).Push(); err != nil {
		t.Fatal(err)
	}
	if _, err := bPod.sets.Save(json.RawMessage(`{"theme":"nuit","language":"fr","editor":{"tabSize":2},"font":{"size":16}}`), "Font size"); err != nil {
		t.Fatal(err)
	}
	if err := bPod.hub.Source(identity.KindSettings).Push(); err != nil {
		t.Fatal(err)
	}
	for _, p := range []*pod{aPod, bPod} {
		got := content(t, p.sets)
		editor, ok := got["editor"].(map[string]any)
		if !ok || editor["tabSize"] != float64(2) {
			t.Fatalf("the tab size must be there on %s: %v", p.id, got)
		}
		font, ok := got["font"].(map[string]any)
		if !ok || font["size"] != float64(16) {
			t.Fatalf("the font size must be there on %s: %v", p.id, got)
		}
		if len(p.hub.Source(identity.KindSettings).Conflicts()) != 0 {
			t.Fatalf("different fields must not ask: %+v", p.hub.Source(identity.KindSettings).Conflicts())
		}
	}

	// The same field changed on both sides while B's link is cut: the conflict is recorded when B
	// catches up.
	f.cut[bPod.id] = true
	if _, err := aPod.sets.Save(json.RawMessage(`{"theme":"dark","language":"fr","editor":{"tabSize":2},"font":{"size":16}}`), "Theme on A"); err != nil {
		t.Fatal(err)
	}
	if err := aPod.hub.Source(identity.KindSettings).Push(); err != nil {
		t.Fatal(err)
	}
	if _, err := bPod.sets.Save(json.RawMessage(`{"theme":"jour","language":"fr","editor":{"tabSize":2},"font":{"size":16}}`), "Theme on B"); err != nil {
		t.Fatal(err)
	}
	if err := bPod.hub.Source(identity.KindSettings).Push(); err != nil {
		t.Fatal(err) // nothing left while the link is down
	}
	f.cut[bPod.id] = false
	if err := bPod.hub.Source(identity.KindSettings).Push(); err != nil {
		t.Fatal(err)
	}
	c := bPod.hub.Source(identity.KindSettings).Conflicts()
	if len(c) != 1 || c[0].Path != "theme" {
		t.Fatalf("the same field changed on both sides must ask: %+v", c)
	}
	if string(c[0].Local) != `"jour"` || string(c[0].Remote) != `"dark"` || c[0].Dev != aPod.id {
		t.Fatalf("the conflict must show both values and the other device: %+v", c)
	}

	// The choice is a normal write: it reaches the other pod.
	if err := bPod.hub.Source(identity.KindSettings).Resolve("theme", json.RawMessage(`"jour"`)); err != nil {
		t.Fatal(err)
	}
	if len(bPod.hub.Source(identity.KindSettings).Conflicts()) != 0 {
		t.Fatalf("the conflict must be resolved: %+v", bPod.hub.Source(identity.KindSettings).Conflicts())
	}
	if content(t, aPod.sets)["theme"] != "jour" {
		t.Fatalf("the choice must reach A: %v", content(t, aPod.sets))
	}

	// A pod that has no wrap yet does not write: it cannot read the blob either.
	cPod := newPod(t, f, "unwrapped")
	if err := cPod.hub.Source(identity.KindSettings).Push(); err == nil {
		t.Fatal("a pod with no sync key must not write")
	}
	if err := cPod.hub.Source(identity.KindSettings).Pull(); err == nil {
		t.Fatal("a pod with no sync key must not read")
	}
}

// A revocation moves the epoch: the pods re-encrypt for the new key, and a blob of the old epoch is
// refused.
func TestSyncRotation(t *testing.T) {
	a, err := phrase()
	if err != nil {
		t.Fatal(err)
	}
	st, err := relay.OpenStore(filepath.Join(t.TempDir(), "relay.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	f := &fakeRelay{st: st, pods: map[string]*Hub{}, cut: map[string]bool{}, admin: a}

	aPod := newPod(t, f, "laptop")
	bPod := newPod(t, f, "phone")
	if err := aPod.hub.Publish(); err != nil {
		t.Fatal(err)
	}
	if err := bPod.hub.Publish(); err != nil {
		t.Fatal(err)
	}
	if _, err := aPod.sets.Save(json.RawMessage(`{"theme":"nuit"}`), "Initial settings"); err != nil {
		t.Fatal(err)
	}
	if err := aPod.hub.Source(identity.KindSettings).Push(); err != nil {
		t.Fatal(err)
	}
	if content(t, bPod.sets)["theme"] != "nuit" {
		t.Fatalf("B must have the settings of A: %v", content(t, bPod.sets))
	}

	// The admin revokes a device and moves to epoch 1, then wraps the new key for the survivors.
	r := &identity.Revocations{Account: a.ID, Seq: 1, Epoch: 1, Revoked: []string{aPod.id}, Created: 1}
	if err := identity.SignRevocations(a.Priv, r); err != nil {
		t.Fatal(err)
	}
	if kept, err := f.st.SaveRevocations(r); err != nil || !kept {
		t.Fatalf("the list must be stored (kept=%v err=%v)", kept, err)
	}
	if err := f.wrapFor(bPod.id); err != nil {
		t.Fatal(err)
	}
	if bPod.hub.Source(identity.KindSettings).Status().Epoch != 1 {
		t.Fatalf("B must be at the new epoch: %+v", bPod.hub.Source(identity.KindSettings).Status())
	}

	// B writes for the new epoch; A, still at the old one, refuses the blob it is shown.
	if _, err := bPod.sets.Save(json.RawMessage(`{"theme":"jour"}`), "Theme"); err != nil {
		t.Fatal(err)
	}
	if err := bPod.hub.Source(identity.KindSettings).Push(); err != nil {
		t.Fatal(err)
	}
	if content(t, aPod.sets)["theme"] != "nuit" {
		t.Fatalf("a blob of another epoch must not be applied: %v", content(t, aPod.sets))
	}

	// A revoked device cannot write: it has nothing to send, so it must change something first.
	if _, err := aPod.sets.Save(json.RawMessage(`{"theme":"dark"}`), "Theme on A"); err != nil {
		t.Fatal(err)
	}
	if err := aPod.hub.Source(identity.KindSettings).Push(); err == nil {
		t.Fatal("a revoked device must not write")
	}
}

// The vault syncs only when it is switched on, and switching it off deletes the blob.
func TestSyncVault(t *testing.T) {
	a, err := phrase()
	if err != nil {
		t.Fatal(err)
	}
	st, err := relay.OpenStore(filepath.Join(t.TempDir(), "relay.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	f := &fakeRelay{st: st, pods: map[string]*Hub{}, cut: map[string]bool{}, admin: a}

	podA := newPod(t, f, "laptop")
	podB := newPod(t, f, "phone")
	if err := podA.hub.Publish(); err != nil {
		t.Fatal(err)
	}
	if err := podB.hub.Publish(); err != nil {
		t.Fatal(err)
	}

	// Both pods know the same model server: a key written on one side is applied on the other.
	keysA, keysB := map[string]string{"s1": "sk-a"}, map[string]string{}
	mapOf := func(keys map[string]string) []VaultMap {
		return []VaultMap{{Name: "llm", Read: func() map[string]string { return keys },
			Apply: func(e map[string]string) error {
				for k, v := range e {
					keys[k] = v
				}
				return nil
			}}}
	}

	on := false
	vaultA := NewVaultSource(podA.store, mapOf(keysA), func() bool { return on }, nil)
	if _, err := podA.hub.AddSource(vaultA); err != nil {
		t.Fatal(err)
	}

	// Off: the pod does not write, and the relay stores nothing.
	if err := podA.hub.Source(identity.KindVault).Push(); err != nil {
		t.Fatalf("a source that is off must not try to write: %v", err)
	}
	if blob, _, err := f.st.Blob(a.ID, identity.KindVault); err != nil || blob != nil {
		t.Fatalf("a source that is off must store nothing: %+v %v", blob, err)
	}

	on = true
	if err := podA.hub.Source(identity.KindVault).Push(); err != nil {
		t.Fatal(err)
	}
	blob, _, err := f.st.Blob(a.ID, identity.KindVault)
	if err != nil || blob == nil {
		t.Fatalf("the vault must be stored (blob=%+v err=%v)", blob, err)
	}
	// The relay holds ciphertext: no key in the stored text.
	if contains(blob.Data, "sk-a") {
		t.Fatal("the stored vault must not hold an API key in clear")
	}

	// The other pod reads it and applies the key to its own server.
	vaultB := NewVaultSource(podB.store, mapOf(keysB), func() bool { return true }, nil)
	if _, err := podB.hub.AddSource(vaultB); err != nil {
		t.Fatal(err)
	}
	if err := podB.hub.Source(identity.KindVault).Pull(); err != nil {
		t.Fatal(err)
	}
	got, err := json.Marshal(vaultB.Read())
	if err != nil {
		t.Fatal(err)
	}
	if !contains(string(got), "sk-a") || keysB["s1"] != "sk-a" {
		t.Fatalf("the vault key must reach the other pod: %s", got)
	}

	// Switching off deletes the blob.
	on = false
	if err := podA.hub.Source(identity.KindVault).Delete(); err != nil {
		t.Fatal(err)
	}
	if blob, _, err := f.st.Blob(a.ID, identity.KindVault); err != nil || blob != nil {
		t.Fatalf("the vault blob must be gone: %+v %v", blob, err)
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
