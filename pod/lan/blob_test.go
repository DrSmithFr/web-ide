package lan

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"testing"
	"time"

	"github.com/DrSmithFr/web-ide/lib/identity"
)

// A pod with no link to the relay publishes through a peer of its cluster: the peer puts the record on
// its own link unchanged, and the relay checks the signature of the device that wrote it. The hop
// adds no trust, so what matters on the LAN side is that a record travels byte for byte.

// sealedBlob is a project list written by dev, the bytes a peer must forward unchanged.
func sealedBlob(t *testing.T, a *identity.Account, dev ed25519.PrivateKey, title string) *identity.BlobRecord {
	t.Helper()
	kind := identity.ProjectsKind(identity.KeyID(dev))
	key := identity.KindKey(identity.EpochKey(a.SyncSeed(), 0), kind)
	plain, err := json.Marshal(map[string]any{"v": 1, "projects": []map[string]string{{"id": "p1", "title": title}}})
	if err != nil {
		t.Fatal(err)
	}
	nonce, data, err := identity.Seal(key, identity.BlobAAD(a.ID, kind, 0, 1), plain)
	if err != nil {
		t.Fatal(err)
	}
	rec := &identity.BlobRecord{Account: a.ID, Kind: kind, Epoch: 0, Seq: 1,
		Device: identity.KeyID(dev), Op: identity.OpPut, Nonce: nonce, Data: data}
	rec.Sig = identity.Sign(dev, []byte(rec.Canonical()))
	return rec
}

// syncPod is a pod whose sync callbacks are collected.
type syncPod struct {
	*pod
	blobs chan *identity.BlobRecord
	wraps chan *identity.WrapRecord
	keys  chan *identity.SyncKeyRecord
}

func startSyncPod(t *testing.T, a *identity.Account, name string, peers ...string) *syncPod {
	t.Helper()
	id, dir := podIdentity(t, a, name)
	p := &syncPod{blobs: make(chan *identity.BlobRecord, 8), wraps: make(chan *identity.WrapRecord, 8), keys: make(chan *identity.SyncKeyRecord, 8)}
	m, err := New(Config{Dir: dir, Addr: "127.0.0.1:0", Identity: id, Name: name, Version: "test",
		Handler: podHandler(name), Peers: peers, Tick: 50 * time.Millisecond,
		OnBlob: func(b *identity.BlobRecord) { p.blobs <- b },
		OnWrap: func(w *identity.WrapRecord) { p.wraps <- w },
		OnSync: func(k *identity.SyncKeyRecord) { p.keys <- k }})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	m.Start(ctx)
	p.pod = &pod{m: m, id: id, dir: dir}
	return p
}

// await waits for a record on a callback channel.
func await[T any](t *testing.T, ch <-chan T, what string) T {
	t.Helper()
	select {
	case v := <-ch:
		return v
	case <-time.After(10 * time.Second):
		t.Fatalf("no %s reached the peer", what)
		return *new(T)
	}
}

// TestBlobTravelsUnchanged: the list of a pod reaches a peer of its cluster as the exact bytes its
// device signed — the peer can put them on its own link, where the relay checks that signature.
func TestBlobTravelsUnchanged(t *testing.T) {
	a := account(t, 3)
	c := startSyncPod(t, a, "charlie")
	b := startSyncPod(t, a, "bravo")
	al := startSyncPod(t, a, "alpha", b.addr(), c.addr())
	waitFor(t, al.pod, "two peers", func(s Status) bool { return len(s.Peers) == 2 })
	waitFor(t, b.pod, "seen by alpha", func(s Status) bool { return len(s.Peers) == 1 })

	rec := sealedBlob(t, a, b.id.Key(), "quiet reader")
	if err := b.m.PublishBlob(rec); err != nil {
		t.Fatal(err)
	}
	got := await(t, al.blobs, "blob")
	if got.Canonical() != rec.Canonical() || got.Sig != rec.Sig {
		t.Fatalf("the record must arrive unchanged:\n got %s\nwant %s", got.Canonical(), rec.Canonical())
	}
	// The peer that carried it is not the signer: the signature kept is the one of the writer.
	if got.Device != identity.KeyID(b.id.Key()) {
		t.Fatalf("the blob must still name the device that wrote it: %s", got.Device)
	}
}

// TestSyncKeyAndWrapGoThroughAPeer: the X25519 key of a pod with no link goes out to a peer, and the
// wrap an admin made for it comes back to it (a wrap for another device only passes on).
func TestSyncKeyAndWrapGoThroughAPeer(t *testing.T) {
	a := account(t, 4)
	c := startSyncPod(t, a, "charlie")
	b := startSyncPod(t, a, "bravo")
	al := startSyncPod(t, a, "alpha", b.addr(), c.addr())
	waitFor(t, al.pod, "two peers", func(s Status) bool { return len(s.Peers) == 2 })

	x, err := identity.X25519FromSeed(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	pub := identity.X25519ID(x.PublicKey())
	rec := &identity.SyncKeyRecord{Account: a.ID, Device: c.id.ID(), Key: pub}
	rec.Sig = identity.Sign(c.id.Key(), []byte(rec.Canonical()))
	if err := c.m.PublishSyncKey(rec); err != nil {
		t.Fatal(err)
	}
	got := await(t, al.keys, "sync key")
	if got.Device != c.id.ID() || got.Key != pub || got.Sig != rec.Sig {
		t.Fatalf("the key of the pod must arrive unchanged: %+v", got)
	}

	// The wrap an admin made for C: alpha pushes it to its peers, and only C can open it.
	w := &identity.WrapRecord{Account: a.ID, Device: c.id.ID(), Epoch: 0, Key: "x25519-sealed", Nonce: "n", Data: "d"}
	w.Sig = identity.Sign(al.id.Key(), []byte(w.Canonical()))
	if err := al.m.PassWrap(w); err != nil {
		t.Fatal(err)
	}
	back := await(t, c.wraps, "wrap")
	if back.Device != c.id.ID() || back.Sig != w.Sig {
		t.Fatalf("the wrap must reach the pod it is for: %+v", back)
	}
	select {
	case bad := <-b.wraps:
		t.Fatalf("a wrap for another device must not be taken as one's own: %+v", bad)
	case <-time.After(300 * time.Millisecond):
	}
}
