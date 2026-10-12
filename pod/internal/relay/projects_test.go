package relay

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/DrSmithFr/web-ide/pod/internal/identity"
)

// The project lists of the pods (milestone 3): each pod writes its list under its own kind
// "projects:<device key>", and a pod with no link of its own has a peer of its site put it for it.
// The relay keeps its rule unchanged: it checks the signature against the device the blob names,
// so a forwarded write is trusted exactly as far as the device that signed it.

// sealProjects seals a project list under a kind and signs it with the device key of the pod that
// wrote it — the bytes a peer forwards unchanged on its own link.
func sealProjects(t *testing.T, a *identity.Account, dev ed25519.PrivateKey, kind, title string, seq, epoch int64) *identity.BlobRecord {
	t.Helper()
	key := identity.KindKey(identity.EpochKey(a.SyncSeed(), epoch), kind)
	plain, err := json.Marshal(map[string]any{
		"v":    1,
		"name": "a pod",
		"projects": []map[string]string{
			{"id": "p1", "title": title, "path": "/home/me/" + title},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	nonce, data, err := identity.Seal(key, identity.BlobAAD(a.ID, kind, epoch, seq), plain)
	if err != nil {
		t.Fatal(err)
	}
	rec := &identity.BlobRecord{Account: a.ID, Kind: kind, Epoch: epoch, Seq: seq,
		Device: identity.KeyID(dev), Op: identity.OpPut, Nonce: nonce, Data: data}
	rec.Sig = identity.Sign(dev, []byte(rec.Canonical()))
	return rec
}

// TestProjectBlobForwardedByAPeer: a peer puts the list of another pod on its own link. The record
// is stored under the device it names, and pushed to the account. A record altered on the way, or
// signed by another device, is refused: forwarding adds no trust.
func TestProjectBlobForwardedByAPeer(t *testing.T) {
	ts := newRelay(t)
	a := testAccount(t)
	pA, pB := podDevice(t, a, "A"), podDevice(t, a, "B")
	kB := identity.KeyID(pB.key)
	kind := identity.ProjectsKind(kB)

	aPod := registerPod(t, ts, "A", "s1", a.ID, pA.cert, true, pA.key, false)
	defer aPod.close()
	ready(t, aPod)
	bPod := registerPod(t, ts, "B", "s2", a.ID, pB.cert, true, pB.key, false)
	defer bPod.close()
	ready(t, bPod)

	rec := sealProjects(t, a, pB.key, kind, "quiet reader", 1, 0)
	aPod.send(t, sigMsg{Type: "blobPut", Account: a.ID, Device: kB, Kind: kind, Blob: rec})
	m := nextUntil(t, aPod, "blob")
	if m.Message != "stored" || m.Kind != kind {
		t.Fatalf("the list of B, signed by B, must be stored: %+v", m)
	}
	// B gets the pushed write, unchanged: it reads its own list back with its key.
	got := nextUntil(t, bPod, "blob")
	if got.Blob == nil || got.Blob.Device != kB || got.Blob.Data != rec.Data || got.Blob.Sig != rec.Sig {
		t.Fatalf("the pushed write must be the one B signed: %+v", got)
	}
	if _, err := identity.Unseal(identity.KindKey(identity.EpochKey(a.SyncSeed(), 0), kind),
		identity.BlobAAD(a.ID, kind, 0, 1), got.Blob.Nonce, got.Blob.Data); err != nil {
		t.Fatalf("the list must open with the kind key of B: %v", err)
	}

	// A record altered on the way no longer matches the signature of its device.
	tampered := *rec
	tampered.Seq, tampered.Data = 2, rec.Data[:len(rec.Data)-4]+"AAAA"
	aPod.send(t, sigMsg{Type: "blobPut", Account: a.ID, Device: kB, Kind: kind, Blob: &tampered})
	if m := nextUntil(t, aPod, "error"); m.Message == "" {
		t.Fatal("an altered blob must be refused")
	}

	// A list for B signed by another device: the signature is checked against the device the blob
	// names, not against the link that carried it.
	fake := sealProjects(t, a, pA.key, kind, "lied by A", 2, 0)
	fake.Device = kB // the blob claims B's list, its signature is A's
	aPod.send(t, sigMsg{Type: "blobPut", Account: a.ID, Device: kB, Kind: kind, Blob: fake})
	if m := nextUntil(t, aPod, "error"); m.Message == "" {
		t.Fatal("a list signed by another device must be refused")
	}

	// A device with no certificate of the account: the relay can check nothing against it.
	ghost := podDevice(t, a, "G")
	ghostCert := *pA.cert
	ghostCert.Key = identity.KeyID(ghost.key)
	ghostCert.Sig = ""
	aPod.send(t, sigMsg{Type: "blobPut", Account: a.ID, Device: ghostCert.Key, Kind: identity.ProjectsKind(ghostCert.Key),
		Blob: sealProjects(t, a, ghost.key, identity.ProjectsKind(ghostCert.Key), "unpaired", 1, 0)})
	if m := nextUntil(t, aPod, "error"); m.Message == "" {
		t.Fatal("a blob of a device the relay does not know must be refused")
	}
}

// linkReader reads a pod link in the background into a channel. A test that must pick one message
// among the pushes of the relay reads from the channel: a Read with a deadline closes the link, and
// the relay walks its links in any order.
type linkReader struct{ msgs chan sigMsg }

func readLink(pc *wsClient) *linkReader {
	r := &linkReader{msgs: make(chan sigMsg, 128)}
	go func() {
		for {
			_, b, err := pc.c.Read(context.Background())
			if err != nil {
				return
			}
			var m sigMsg
			if json.Unmarshal(b, &m) == nil {
				r.msgs <- m
			}
		}
	}()
	return r
}

// await reads until a message answers match.
func (r *linkReader) await(t *testing.T, match func(sigMsg) bool) sigMsg {
	t.Helper()
	deadline := time.After(3 * time.Second)
	for {
		select {
		case m := <-r.msgs:
			if match(m) {
				return m
			}
		case <-deadline:
			t.Fatal("the relay never sent what the test waited for")
		}
	}
}

// quiet returns the messages that arrive during d: what the link has left to say.
func (r *linkReader) quiet(d time.Duration) []sigMsg {
	out := []sigMsg{}
	deadline := time.After(d)
	for {
		select {
		case m := <-r.msgs:
			out = append(out, m)
		case <-deadline:
			return out
		}
	}
}

func has(kinds []string, kind string) bool {
	for _, k := range kinds {
		if k == kind {
			return true
		}
	}
	return false
}

// TestProjectBlobsPrefix: a pod reads the whole family of project lists with the prefix
// "projects:", one blob message per pod that published one. The prefix is matched literally: the
// wildcards a kind could hold are escaped.
func TestProjectBlobsPrefix(t *testing.T) {
	ts := newRelay(t)
	a := testAccount(t)
	p := podDevice(t, a, "A")
	k := identity.KeyID(p.key)

	aPod := registerPod(t, ts, "A", "s1", a.ID, p.cert, true, p.key, false)
	defer aPod.close()
	ready(t, aPod)
	r := readLink(aPod)

	for _, kind := range []string{identity.ProjectsKind(k), identity.ProjectsKind("another-device"), identity.KindSettings, "projects_x", "projectsax"} {
		aPod.send(t, sigMsg{Type: "blobPut", Account: a.ID, Device: k, Kind: kind,
			Blob: sealProjects(t, a, p.key, kind, "reader", 1, 0)})
		m := r.await(t, func(m sigMsg) bool { return m.Type == "blob" && m.Kind == kind && m.Message == "stored" })
		if m.Message != "stored" {
			t.Fatalf("the write under %q must be stored: %+v", kind, m)
		}
	}
	// The relay pushes a stored write back to its writer too: read what the link has left to say
	// before asking, so the answer to the read is not mixed with those copies.
	r.quiet(300 * time.Millisecond)

	aPod.send(t, sigMsg{Type: "blobGet", Account: a.ID, Device: k, Kind: identity.ProjectsPrefix})
	kinds := []string{}
	for _, m := range r.quiet(400 * time.Millisecond) {
		if m.Type == "blob" {
			kinds = append(kinds, m.Kind)
		}
	}
	if !has(kinds, identity.ProjectsKind(k)) || !has(kinds, identity.ProjectsKind("another-device")) {
		t.Fatalf("the prefix must answer the project lists: %+v", kinds)
	}
	// Nothing else comes: the settings blob and the kinds that only look like the prefix stay
	// silent. A pod that asked for one kind still gets one blob.
	for _, kind := range []string{identity.KindSettings, "projects_x", "projectsax"} {
		if has(kinds, kind) {
			t.Errorf("the prefix must not answer %q: %+v", kind, kinds)
		}
	}
	aPod.send(t, sigMsg{Type: "blobGet", Account: a.ID, Device: k, Kind: identity.KindSettings})
	m := r.await(t, func(m sigMsg) bool { return m.Type == "blob" && m.Kind == identity.KindSettings })
	if m.Kind != identity.KindSettings {
		t.Fatalf("a plain kind must answer its own blob: %+v", m)
	}
}

// TestStoreBlobsPrefix: the store side of the prefix read, including the escaped wildcards.
func TestStoreBlobsPrefix(t *testing.T) {
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
	k := identity.KeyID(dev)
	for _, kind := range []string{identity.ProjectsKind(k), "projects:other", identity.KindSettings, "projects_x", "projectsax"} {
		if kept, err := st.PutBlob(sealProjects(t, a, dev, kind, "reader", 1, 0)); err != nil || !kept {
			t.Fatalf("the write under %q must be stored (kept=%v err=%v)", kind, kept, err)
		}
	}

	recs, updated, err := st.Blobs(a.ID, identity.ProjectsPrefix)
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 2 || recs[0].Kind != "projects:"+k || recs[1].Kind != "projects:other" {
		t.Fatalf("the prefix must answer the project kinds only: %+v", recs)
	}
	for _, r := range recs {
		if updated[r.Kind] == 0 {
			t.Fatalf("each stored write carries the time it was written: %+v", r)
		}
	}

	// An underscore of a prefix is a character, not a wildcard.
	recs, _, err = st.Blobs(a.ID, "projects_")
	if err != nil || len(recs) != 1 || recs[0].Kind != "projects_x" {
		t.Fatalf("the wildcards of a prefix must be escaped: %+v %v", recs, err)
	}
	recs, _, err = st.Blobs(a.ID, "projects%")
	if err != nil || len(recs) != 0 {
		t.Fatalf("a percent must not match anything: %+v %v", recs, err)
	}
}

// TestProjectBlobNoClearText: the relay caches a project list and never sees a project name.
func TestProjectBlobNoClearText(t *testing.T) {
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
	kind := identity.ProjectsKind(identity.KeyID(dev))
	title := "the lighthouse project"
	if kept, err := st.PutBlob(sealProjects(t, a, dev, kind, title, 1, 0)); err != nil || !kept {
		t.Fatalf("the list must be stored (kept=%v err=%v)", kept, err)
	}

	raw := storedText(t, st, "SELECT data FROM blobs WHERE account=? AND kind=?", a.ID, kind)
	if contains(raw, title) || contains(raw, "lighthouse") || contains(raw, "/home/me/") {
		t.Fatalf("the stored blob must hold no project name in clear: %s", raw)
	}
	// The whole base: no table holds a project name either.
	for _, q := range []string{"SELECT COALESCE(group_concat(kind), '') FROM blobs", "SELECT COALESCE(group_concat(cert), '') FROM devices", "SELECT COALESCE(group_concat(name), '') FROM pods"} {
		if s := storedText(t, st, q); contains(s, title) || contains(s, "lighthouse") {
			t.Errorf("%s must not hold a project name: %s", q, s)
		}
	}
}
