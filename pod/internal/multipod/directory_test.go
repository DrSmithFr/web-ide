package multipod

import (
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/DrSmithFr/web-ide/pod/internal/identity"
	"github.com/DrSmithFr/web-ide/pod/internal/lan"
	"github.com/DrSmithFr/web-ide/pod/internal/relayclient"
	"github.com/DrSmithFr/web-ide/pod/internal/settings"
	"github.com/DrSmithFr/web-ide/pod/internal/settingsync"
	"github.com/DrSmithFr/web-ide/pod/internal/store"
)

// The directory of the pods: what the home page shows. The relay reports who is online and in which
// site; the lists come as sealed blobs; the LAN peers come first, since their page is reachable even
// with the relay down.

const testPhrase = "lecture vapor excuse note pledge rough bundle start regular burden reveal theme banana pencil owner cube social job emotion member joy sting dash whisper"

// fakeLAN is a stand for the LAN link of the pod.
type fakeLAN struct{ st lan.Status }

func (f fakeLAN) Status() lan.Status { return f.st }

// hubOf makes a paired pod with its hub, and the account it is paired with.
func hubOf(t *testing.T) (*settingsync.Hub, *identity.PodIdentity, *identity.Account) {
	t.Helper()
	a, err := identity.FromPhrase(testPhrase)
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "pod")
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
	if err := p.Pin(a.ID); err != nil {
		t.Fatal(err)
	}
	if err := p.SetCert(identity.NewCert(a, p.Key(), "this pod", identity.KindPod)); err != nil {
		t.Fatal(err)
	}
	return settingsync.NewHub(st, nil, p, settingsync.NewFlags(sets)), p, a
}

// wrapFor is the wrap an admin made for this device: it gives the hub the key of the epoch, the one
// with which the other pods sealed their lists.
func wrapFor(t *testing.T, a *identity.Account, p *identity.PodIdentity, epoch int64) *identity.WrapRecord {
	t.Helper()
	x, err := identity.X25519FromSeed(identity.Derive(p.Seed, identity.InfoX25519))
	if err != nil {
		t.Fatal(err)
	}
	admin, err := identity.X25519FromSeed(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	shared, err := identity.Shared(admin, identity.X25519ID(x.PublicKey()))
	if err != nil {
		t.Fatal(err)
	}
	nonce, data, err := identity.Seal(identity.WrapKey(shared), identity.WrapAAD(a.ID, p.ID(), epoch), identity.EpochKey(a.SyncSeed(), epoch))
	if err != nil {
		t.Fatal(err)
	}
	w := &identity.WrapRecord{Account: a.ID, Device: p.ID(), Epoch: epoch, Key: identity.X25519ID(admin.PublicKey()), Nonce: nonce, Data: data}
	w.Sig = identity.Sign(a.Priv, []byte(w.Canonical()))
	return w
}

// blobOf is the list a pod published under its own kind, sealed with the key of the epoch.
func blobOf(t *testing.T, a *identity.Account, device string, epoch int64, seq int64, name string, titles ...string) *identity.BlobRecord {
	t.Helper()
	return blobOfProto(t, a, device, epoch, seq, name, 0, titles...)
}

// blobOfProto seals the list of a pod that tells the protocol proto (0: it tells none).
func blobOfProto(t *testing.T, a *identity.Account, device string, epoch int64, seq int64, name string, proto int, titles ...string) *identity.BlobRecord {
	t.Helper()
	return blobOfList(t, a, device, epoch, seq, List{V: 1, Name: name, Version: "1.0", Protocol: proto, Projects: projects(titles...)})
}

// blobOfList seals a whole list.
func blobOfList(t *testing.T, a *identity.Account, device string, epoch int64, seq int64, l List) *identity.BlobRecord {
	t.Helper()
	kind := identity.ProjectsKind(device)
	content, err := json.Marshal(l)
	if err != nil {
		t.Fatal(err)
	}
	// What travels is the state envelope of #28, the way the hub of the publishing pod writes it.
	st := &settingsync.State{Device: device, Fields: map[string]settingsync.Field{}}
	st.Apply(content, nil)
	plain, err := json.Marshal(st)
	if err != nil {
		t.Fatal(err)
	}
	nonce, data, err := identity.Seal(identity.KindKey(identity.EpochKey(a.SyncSeed(), epoch), kind),
		identity.BlobAAD(a.ID, kind, epoch, seq), plain)
	if err != nil {
		t.Fatal(err)
	}
	return &identity.BlobRecord{Account: a.ID, Kind: kind, Epoch: epoch, Seq: seq, Device: device,
		Op: identity.OpPut, Nonce: nonce, Data: data}
}

func projects(titles ...string) []Project {
	out := []Project{}
	for i, s := range titles {
		out = append(out, Project{ID: string(rune('a' + i)), Title: s})
	}
	return out
}

// TestDirectoryProtocolMismatch: a pod that does not speak this multi-pod protocol is listed with
// its version and what it means — outdated (Update this pod) or newer (this pod is the one to
// update) — and it is never routed to. A pod that tells none is the older one.
func TestDirectoryProtocolMismatch(t *testing.T) {
	hub, p, a := hubOf(t)
	d := New(hub, fakeLAN{st: lan.Status{Peers: []lan.PeerView{{ID: "devOld", Name: "older", Online: true,
		Path: "/_lan/pod/devOld", Protocol: 0}}}})
	if err := hub.SetWrap(wrapFor(t, a, p, 0)); err != nil {
		t.Fatal(err)
	}
	hub.SetOther(func(m settingsync.Msg) { d.HandleBlob(m) })
	d.HandleBlob(settingsync.Msg{Type: "blob", Blob: blobOfProto(t, a, "devList", 0, 1, "listed", Protocol+1, "quiet reader")})
	d.RelayPods([]relayclient.PodInfo{
		{Key: "devNew", Name: "newer", Site: "devOld", Protocol: Protocol + 1},
		{Key: "devSame", Name: "same", Site: "devOld", Protocol: Protocol},
		{Key: p.ID(), Name: "this pod", Site: "devOld", Gateway: true},
	})

	by := map[string]PodView{}
	for _, v := range d.List() {
		by[v.Key] = v
	}
	if len(by) != 4 {
		t.Fatalf("four pods expected: %+v", by)
	}
	if by["devOld"].Mismatch != MismatchOutdated || by["devOld"].Protocol != 0 {
		t.Fatalf("a LAN peer that tells no protocol is outdated: %+v", by["devOld"])
	}
	if by["devNew"].Mismatch != MismatchNewer || by["devNew"].Protocol != Protocol+1 {
		t.Fatalf("a pod the relay reports with a newer protocol: %+v", by["devNew"])
	}
	if by["devSame"].Mismatch != "" {
		t.Fatalf("a pod of this protocol carries no mismatch: %+v", by["devSame"])
	}
	if by["devList"].Mismatch != MismatchNewer || by["devList"].Online {
		t.Fatalf("a pod only known by its list: the list carries its protocol: %+v", by["devList"])
	}
	for _, key := range []string{"devOld", "devNew", "devList"} {
		if d.Routeable(key) {
			t.Fatalf("%s does not speak this protocol, it is not routed to", key)
		}
	}
	if !d.Routeable("devSame") {
		t.Fatal("a pod of this protocol is routed to")
	}
	// A pod nothing says about: routed to, like any pod of this build.
	if !d.Routeable("devUnknown") {
		t.Fatal("a pod no source names is not held against a protocol")
	}
}

// TestDirectoryNoKeyYet: before the wrap arrived, a pod online is listed without its list — the page
// says "waiting for the sync key", it is not an error.
func TestDirectoryNoKeyYet(t *testing.T) {
	hub, p, a := hubOf(t)
	d := New(hub, fakeLAN{})
	d.HandleBlob(settingsync.Msg{Type: "blob", Blob: blobOf(t, a, "devB", 0, 1, "bravo", "quiet reader")})
	d.RelayPods([]relayclient.PodInfo{{Key: "devB", Name: "bravo", Site: "devA"}, {Key: p.ID(), Name: "this pod", Site: "devA", Gateway: true}})

	out := d.List()
	if len(out) != 1 {
		t.Fatalf("this pod is not listed among the others: %+v", out)
	}
	if !out[0].NoKey || out[0].Name != "bravo" || !out[0].Online {
		t.Fatalf("a pod online without a readable list must be greyed as waiting for the key: %+v", out[0])
	}
	if !out[0].Gateway && out[0].Site == "" {
		t.Fatalf("the site and the gateway come from the relay: %+v", out[0])
	}
}

// TestDirectoryMergesSources: the LAN peers first (their page is reachable through this pod), then
// the pods the relay reports online, then the pods whose only trace is a cached list — offline, with
// the time they published it.
func TestDirectoryMergesSources(t *testing.T) {
	hub, p, a := hubOf(t)
	d := New(hub, fakeLAN{st: lan.Status{Peers: []lan.PeerView{{ID: "devL", Name: "lantern", Fingerprint: "a b c d", Online: true,
		Path: "/_lan/pod/devL", Projects: json.RawMessage(`[{"name":"seen live"}]`)}}}})
	if err := hub.SetWrap(wrapFor(t, a, p, 0)); err != nil {
		t.Fatal(err)
	}
	hub.SetOther(func(m settingsync.Msg) { d.HandleBlob(m) })

	d.HandleBlob(settingsync.Msg{Type: "blob", Blob: blobOf(t, a, "devB", 0, 1, "bravo", "quiet reader", "other"), Updated: 111})
	d.HandleBlob(settingsync.Msg{Type: "blob", Blob: blobOf(t, a, "devO", 0, 2, "offline pod", "kept"), Updated: 222})
	d.RelayPods([]relayclient.PodInfo{{Key: "devB", Name: "bravo", Site: "devL"}, {Key: p.ID(), Name: "this pod", Site: "devL", Gateway: true}})

	out := d.List()
	by := map[string]PodView{}
	for _, v := range out {
		by[v.Key] = v
	}
	if len(out) != 3 {
		t.Fatalf("three pods expected: %+v", out)
	}
	if out[0].Key != "devL" || out[0].Path != "/_lan/pod/devL" {
		t.Fatalf("the LAN peer comes first, with its path: %+v", out[0])
	}
	b := by["devB"]
	if !b.Online || b.Site != "devL" || b.Seen != 111 {
		t.Fatalf("the pod the relay reports online: %+v", b)
	}
	var got []map[string]string
	_ = json.Unmarshal(b.Projects, &got)
	if len(got) != 2 || got[0]["name"] != "quiet reader" {
		t.Fatalf("the cached list must be shown: %s", b.Projects)
	}
	o := by["devO"]
	if o.Online || o.Seen != 222 || o.Name != "offline pod" || o.NoKey {
		t.Fatalf("a pod only in a cached list is greyed with its list: %+v", o)
	}
	if by[p.ID()].Key != "" {
		t.Fatal("this pod is not among the others")
	}
	if d.Gateway() == "" {
		t.Fatal("the gateway of the site of this pod must be known")
	}

	// A list deleted by its pod: the block keeps its name, without projects.
	d.HandleBlob(settingsync.Msg{Type: "blob", Blob: &identity.BlobRecord{Account: a.ID, Kind: identity.ProjectsKind("devO"),
		Device: "devO", Epoch: 0, Seq: 3, Op: identity.OpDel}})
	after := d.List()
	for _, v := range after {
		if v.Key == "devO" && !v.NoKey {
			t.Fatalf("a deleted list must leave the pod without projects: %+v", v)
		}
	}
}

// TestDirectoryOneWay: a LAN peer that dialed this pod but cannot be dialed back is shown by the
// relay when the relay sees it online (with its cached list), and keeps the one-way mark; with no
// relay it stays a LAN peer, offline, still marked one-way.
func TestDirectoryOneWay(t *testing.T) {
	hub, p, a := hubOf(t)
	d := New(hub, fakeLAN{st: lan.Status{Peers: []lan.PeerView{{ID: "devW", Name: "wsl", OneWay: true}}}})
	if err := hub.SetWrap(wrapFor(t, a, p, 0)); err != nil {
		t.Fatal(err)
	}
	hub.SetOther(func(m settingsync.Msg) { d.HandleBlob(m) })
	d.HandleBlob(settingsync.Msg{Type: "blob", Blob: blobOf(t, a, "devW", 0, 1, "wsl", "behind nat"), Updated: 333})

	out := d.List()
	if len(out) != 1 || out[0].Online || !out[0].OneWay {
		t.Fatalf("without the relay, a one-way peer is offline and marked: %+v", out)
	}
	d.RelayPods([]relayclient.PodInfo{{Key: "devW", Name: "wsl", Site: "devW"}})
	out = d.List()
	if len(out) != 1 || !out[0].Online || !out[0].OneWay || out[0].Seen != 333 {
		t.Fatalf("the relay sees the one-way peer online, with its cached list: %+v", out)
	}
}

// TestDirectoryIconsAndOpened: the unified home page shows the icon of every project and sorts them
// by their last opening — from the cached list, and for a LAN peer, whose live projects.list carries
// no icon, from the list it published.
func TestDirectoryIconsAndOpened(t *testing.T) {
	hub, p, a := hubOf(t)
	d := New(hub, fakeLAN{st: lan.Status{Peers: []lan.PeerView{{ID: "devL", Name: "lantern", Online: true,
		Path: "/_lan/pod/devL", Projects: json.RawMessage(`[{"id":"l1","name":"live","openedAt":"2026-10-01T10:00:00Z"}]`)}}}})
	if err := hub.SetWrap(wrapFor(t, a, p, 0)); err != nil {
		t.Fatal(err)
	}
	hub.SetOther(func(m settingsync.Msg) { d.HandleBlob(m) })
	d.HandleBlob(settingsync.Msg{Type: "blob", Blob: blobOfList(t, a, "devL", 0, 1, List{V: 1, Name: "lantern",
		Projects: []Project{{ID: "l1", Title: "live", Icon: "<svg>L</svg>"}}})})
	d.HandleBlob(settingsync.Msg{Type: "blob", Blob: blobOfList(t, a, "devO", 0, 2, List{V: 1, Name: "far",
		Projects: []Project{{ID: "o1", Title: "cached", Icon: "<svg>O</svg>", Opened: 1790000000}}})})

	by := map[string][]map[string]string{}
	for _, v := range d.List() {
		var got []map[string]string
		_ = json.Unmarshal(v.Projects, &got)
		by[v.Key] = got
	}
	if l := by["devL"]; len(l) != 1 || l[0]["icon"] != "<svg>L</svg>" || l[0]["openedAt"] != "2026-10-01T10:00:00Z" {
		t.Fatalf("the live list of a LAN peer keeps its fields and gets the published icons: %v", l)
	}
	if o := by["devO"]; len(o) != 1 || o[0]["icon"] != "<svg>O</svg>" || o[0]["openedAt"] != time.Unix(1790000000, 0).UTC().Format(time.RFC3339) {
		t.Fatalf("a cached list carries the icon and the last opening: %v", o)
	}
}

// TestDirectoryLANDownRelayUp: a LAN peer whose link is down while the relay sees it online is
// listed online, from the relay: its projects stay openable through the relay.
func TestDirectoryLANDownRelayUp(t *testing.T) {
	hub, p, a := hubOf(t)
	d := New(hub, fakeLAN{st: lan.Status{Peers: []lan.PeerView{{ID: "devD", Name: "down"}}}})
	if err := hub.SetWrap(wrapFor(t, a, p, 0)); err != nil {
		t.Fatal(err)
	}
	hub.SetOther(func(m settingsync.Msg) { d.HandleBlob(m) })
	if out := d.List(); len(out) != 1 || out[0].Online {
		t.Fatalf("with no relay, a LAN peer whose link is down is offline: %+v", out)
	}
	d.RelayPods([]relayclient.PodInfo{{Key: "devD", Name: "down", Site: "devD"}})
	out := d.List()
	if len(out) != 1 || !out[0].Online || out[0].Site != "devD" {
		t.Fatalf("the relay sees it: it is online, from the relay: %+v", out)
	}
}
