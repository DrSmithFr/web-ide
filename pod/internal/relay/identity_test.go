package relay

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/DrSmithFr/web-ide/pod/internal/identity"
)

// testAccount is the account of the shared vectors, with a device key.
func testAccount(t *testing.T) *identity.Account {
	t.Helper()
	a, err := identity.FromPhrase("lecture vapor excuse note pledge rough bundle start regular burden reveal theme banana pencil owner cube social job emotion member joy sting dash whisper")
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func TestStoreDevices(t *testing.T) {
	st, err := OpenStore(filepath.Join(t.TempDir(), "relay.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	a := testAccount(t)

	dev := identity.KeyFromSeed(identity.DeviceSeed(a.Seed))
	cert := identity.NewCert(a, dev, "phone", identity.KindBrowser)
	if err := st.StoreDevice(cert); err != nil {
		t.Fatalf("a certificate signed by the account must be stored: %v", err)
	}

	// The relay has no account key: it can only check a signature against the account the
	// certificate claims. A certificate claiming this account but signed by another key is
	// refused — only a device holding the account key can sign for it.
	other, err := identity.FromPhrase("abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon about")
	if err != nil {
		t.Fatal(err)
	}
	forged := identity.NewCert(other, dev, "evil", identity.KindBrowser)
	forged.Account = a.ID // claims the account, but the signature is the other one's
	if err := st.StoreDevice(forged); err == nil {
		t.Fatal("a certificate signed by another account must be refused")
	}

	// A tampered certificate no longer matches its signature.
	tampered := *cert
	tampered.Name = "renamed"
	if err := st.StoreDevice(&tampered); err == nil {
		t.Fatal("a tampered certificate must be refused")
	}

	devs, err := st.Devices(a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(devs) != 1 || devs[0].Key != identity.KeyID(dev) || devs[0].Cert.Name != "phone" {
		t.Fatalf("the device list must hold the stored certificate: %+v", devs)
	}
}

func TestStoreRevocations(t *testing.T) {
	st, err := OpenStore(filepath.Join(t.TempDir(), "relay.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	a := testAccount(t)
	dev := identity.KeyFromSeed(identity.DeviceSeed(a.Seed))

	r := &identity.Revocations{Account: a.ID, Seq: 1, Epoch: 1, Revoked: []string{identity.KeyID(dev)}, Created: time.Now().Unix()}
	if err := identity.SignRevocations(a.Priv, r); err != nil {
		t.Fatal(err)
	}
	if kept, err := st.SaveRevocations(r); err != nil || !kept {
		t.Fatalf("a signed list must be stored (kept=%v err=%v)", kept, err)
	}

	// A list whose signature does not match its content is refused.
	bad := *r
	bad.Seq = 3
	if kept, err := st.SaveRevocations(&bad); err == nil {
		t.Fatal("a tampered revocation list must be refused")
	} else if kept {
		t.Fatal("a tampered list must not be kept")
	}

	// A lower seq is ignored: a relay that replays an old list changes nothing.
	older := &identity.Revocations{Account: a.ID, Seq: 1, Epoch: 1, Revoked: []string{}, Created: time.Now().Unix()}
	if err := identity.SignRevocations(a.Priv, older); err != nil {
		t.Fatal(err)
	}
	if kept, err := st.SaveRevocations(older); err != nil || kept {
		t.Fatalf("an older seq must not be kept (kept=%v err=%v)", kept, err)
	}

	// A higher seq wins.
	newer := &identity.Revocations{Account: a.ID, Seq: 2, Epoch: 2, Revoked: []string{identity.KeyID(dev)}, Created: time.Now().Unix()}
	if err := identity.SignRevocations(a.Priv, newer); err != nil {
		t.Fatal(err)
	}
	if kept, err := st.SaveRevocations(newer); err != nil || !kept {
		t.Fatalf("a higher seq must be kept (kept=%v err=%v)", kept, err)
	}
	got, err := st.Revocations(a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || got.Seq != 2 || got.Epoch != 2 || !got.Has(identity.KeyID(dev)) {
		t.Fatalf("the stored list must be the newest: %+v", got)
	}

	// A list of another account is refused.
	foreign := &identity.Revocations{Account: "another-account", Seq: 9, Epoch: 1, Revoked: []string{identity.KeyID(dev)}, Created: time.Now().Unix()}
	if _, err := st.SaveRevocations(foreign); err == nil {
		t.Fatal("a revocation list of another account must be refused")
	}
}

func TestStorePairing(t *testing.T) {
	st, err := OpenStore(filepath.Join(t.TempDir(), "relay.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	a := testAccount(t)
	dev := identity.KeyFromSeed(identity.DeviceSeed(a.Seed))

	p, err := st.CreatePairing(identity.KeyID(dev), "phone", identity.KindBrowser)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Code) != 8 || p.Account != "" || p.Used {
		t.Fatalf("a fresh pairing: %+v", p)
	}

	// The code is claimed by the first admin that reads it.
	if _, err := st.ClaimPairing(p.Code, a.ID); err != nil {
		t.Fatal(err)
	}
	other, err := identity.FromPhrase("abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon about")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.ClaimPairing(p.Code, other.ID); err == nil {
		t.Fatal("a code claimed by an account cannot be read by another")
	}

	// The certificate must name the key the device posted: a relay that swapped the key
	// would not be able to sign a matching certificate.
	wrong := identity.NewCert(a, identity.KeyFromSeed(identity.Derive(a.Seed, "other device")), "phone", identity.KindBrowser)
	if _, err := st.ApprovePairing(p.Code, a.ID, wrong); err == nil {
		t.Fatal("a certificate for another key must be refused")
	}

	cert := identity.NewCert(a, dev, "phone", identity.KindBrowser)
	got, err := st.ApprovePairing(p.Code, a.ID, cert)
	if err != nil {
		t.Fatalf("the admin must be able to sign the certificate: %v", err)
	}
	if !got.Used || got.Cert == nil || got.Cert.Key != identity.KeyID(dev) {
		t.Fatalf("the approved pairing must carry the certificate: %+v", got)
	}
	// Single use.
	if _, err := st.ApprovePairing(p.Code, a.ID, cert); err == nil {
		t.Fatal("a code already used must be refused")
	}

	// An expired code is refused.
	expired, err := st.CreatePairing(identity.KeyID(dev), "phone", identity.KindBrowser)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.db.Exec("UPDATE pairings SET expires=? WHERE code=?", time.Now().Add(-time.Minute).Unix(), expired.Code); err != nil {
		t.Fatal(err)
	}
	if _, err := st.ApprovePairing(expired.Code, a.ID, cert); err == nil {
		t.Fatal("an expired pairing must be refused")
	}

	// An unknown code is not an error but no pairing.
	if p, err := st.Pairing("XXXXXXXX"); err != nil || p != nil {
		t.Fatalf("an unknown code must give no pairing: %v %v", p, err)
	}

	list, err := st.Pairings(a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].Code != p.Code || list[0].Cert == nil {
		t.Fatalf("the admin must see what it signed: %+v", list)
	}
}

// TestIdentityPairing: the pairing rendezvous over the identity link — a device posts its
// key and gets a code, an admin reads it, signs the certificate and posts it back; the
// joining device gets the certificate.
func TestIdentityPairing(t *testing.T) {
	ts := newRelay(t)
	a := testAccount(t)
	dev := identity.KeyFromSeed(identity.DeviceSeed(a.Seed))

	joiner := connectIdentity(t, ts, a.ID, identity.NewCert(a, dev, "phone", identity.KindBrowser))
	defer joiner.close()

	m := joiner.nextId(t)
	if m.Type != "ready" || m.Account != a.ID {
		t.Fatalf("the joiner must be ready, got %q", m.Type)
	}

	joiner.sendId(t, idMsg{Type: "pair", Key: identity.KeyID(dev), Name: "phone", Kind: identity.KindBrowser})
	m = joiner.nextId(t)
	if m.Type != "pair" || m.Pairing == nil || len(m.Pairing.Code) != 8 {
		t.Fatalf("the joiner must get a code, got %q %+v", m.Type, m.Pairing)
	}
	code := m.Pairing.Code

	admin := connectIdentity(t, ts, a.ID, nil)
	defer admin.close()
	if m := admin.nextId(t); m.Type != "ready" {
		t.Fatalf("the admin must be ready, got %q", m.Type)
	}
	admin.sendId(t, idMsg{Type: "pairGet", Code: code})
	m = admin.nextId(t)
	if m.Type != "pair" || m.Pairing == nil || m.Pairing.Key != identity.KeyID(dev) {
		t.Fatalf("the admin must read the posted key, got %q %+v", m.Type, m.Pairing)
	}

	// The admin signs the certificate for the key it read.
	cert := identity.NewCert(a, dev, "phone", identity.KindBrowser)
	admin.sendId(t, idMsg{Type: "pairApprove", Code: code, Cert: cert})
	m = admin.nextId(t)
	if m.Type != "pair" || !m.Pairing.Used {
		t.Fatalf("the approval must come back, got %q %q %+v", m.Type, m.Message, m.Pairing)
	}
	m = joiner.nextId(t)
	if m.Type != "pair" || m.Pairing == nil || m.Pairing.Cert == nil || m.Pairing.Cert.Sig != cert.Sig {
		t.Fatalf("the joiner must get the signed certificate, got %q %+v", m.Type, m.Pairing)
	}

	// A device of another account cannot read the code.
	other, err := identity.FromPhrase("abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon about")
	if err != nil {
		t.Fatal(err)
	}
	foreign := connectIdentity(t, ts, other.ID, nil)
	defer foreign.close()
	if m := foreign.nextId(t); m.Type != "ready" {
		t.Fatalf("the foreign link must be ready, got %q", m.Type)
	}
	foreign.sendId(t, idMsg{Type: "pairGet", Code: code})
	if m := foreign.nextId(t); m.Type != "error" {
		t.Fatalf("another account must not read the code, got %q %q", m.Type, m.Message)
	}
}

// TestIdentityRevocationPush: an admin posts a signed list; the relay stores it and pushes
// it to the online pods of the account. A list that does not verify is refused.
func TestIdentityRevocationPush(t *testing.T) {
	ts := newRelay(t)
	a := testAccount(t)
	dev := identity.KeyFromSeed(identity.DeviceSeed(a.Seed))

	// A paired pod registers with its account and its certificate: the relay can check the
	// signature but never make one.
	podKey := identity.KeyFromSeed(identity.Derive(a.Seed, "the pod key"))
	podLink := connectIdentity(t, ts, a.ID, identity.NewCert(a, podKey, "laptop", identity.KindPod))
	defer podLink.close()
	if m := podLink.nextId(t); m.Type != "ready" {
		t.Fatalf("the pod must be ready, got %q", m.Type)
	}

	admin := connectIdentity(t, ts, a.ID, nil)
	defer admin.close()
	if m := admin.nextId(t); m.Type != "ready" {
		t.Fatalf("the admin must be ready, got %q", m.Type)
	}

	r := &identity.Revocations{Account: a.ID, Seq: 1, Epoch: 1, Revoked: []string{identity.KeyID(dev)}, Created: time.Now().Unix()}
	if err := identity.SignRevocations(a.Priv, r); err != nil {
		t.Fatal(err)
	}
	admin.sendId(t, idMsg{Type: "revoke", List: r})
	m := admin.nextId(t)
	if m.Type != "revoked" || m.Message != "stored" {
		t.Fatalf("the admin must get the list back as stored, got %q %q", m.Type, m.Message)
	}
	// The online pod of the account gets the pushed list.
	pod := podLink.nextId(t)
	if pod.Type != "revocations" || pod.List == nil || !pod.List.Has(identity.KeyID(dev)) {
		t.Fatalf("the pod must get the pushed list, got %q %+v", pod.Type, pod.List)
	}

	// A tampered list is refused.
	bad := *r
	bad.Seq = 2
	admin.sendId(t, idMsg{Type: "revoke", List: &bad})
	if m := admin.nextId(t); m.Type != "error" {
		t.Fatalf("a tampered list must be refused, got %q %q", m.Type, m.Message)
	}
}

// TestIdentityPodRegisterCert: a pod registers with its certificate; a certificate signed by
// another account is refused, and the ready message carries the stored list.
func TestIdentityPodRegisterCert(t *testing.T) {
	ts := newRelay(t)
	a := testAccount(t)
	podKey := identity.KeyFromSeed(identity.DeviceSeed(a.Seed))
	cert := identity.NewCert(a, podKey, "laptop", identity.KindPod)

	c, _, err := websocket.Dial(context.Background(), wsURL(ts, "/_relay/identity"), nil)
	if err != nil {
		t.Fatal("dialing the identity link:", err)
	}
	defer c.CloseNow()
	idc := &wsClient{c}
	idc.sendId(t, idMsg{Type: "register", Account: a.ID, Cert: cert})
	m := idc.nextId(t)
	if m.Type != "ready" || m.Account != a.ID {
		t.Fatalf("the pod must be ready, got %q", m.Type)
	}

	// The certificate is stored and can be listed.
	idc.sendId(t, idMsg{Type: "devices", Account: a.ID})
	m = idc.nextId(t)
	if m.Type != "devices" || len(m.Devices) != 1 || m.Devices[0].Key != identity.KeyID(podKey) {
		t.Fatalf("the device list must hold the pod certificate, got %q %+v", m.Type, m.Devices)
	}

	// A pod claiming the account with a certificate signed by another key is refused.
	fake := identity.NewCert(a, identity.KeyFromSeed(identity.Derive(a.Seed, "not the pod key")), "evil", identity.KindPod)
	fake.Sig = "not-a-signature"
	c2, _, err := websocket.Dial(context.Background(), wsURL(ts, "/_relay/identity"), nil)
	if err != nil {
		t.Fatal("dialing the second link:", err)
	}
	defer c2.CloseNow()
	fakeClient := &wsClient{c2}
	fakeClient.sendId(t, idMsg{Type: "register", Account: a.ID, Cert: fake})
	if m := fakeClient.nextId(t); m.Type != "error" {
		t.Fatalf("a certificate that does not verify must be refused, got %q %q", m.Type, m.Message)
	}
}

// connectIdentity opens a pairing/admin link and registers an account on it.
func connectIdentity(t *testing.T, ts *httptest.Server, account string, cert *identity.Cert) *wsClient {
	t.Helper()
	ws, _, err := websocket.Dial(context.Background(), wsURL(ts, "/_relay/identity"), nil)
	if err != nil {
		t.Fatal("dialing the identity link:", err)
	}
	c := &wsClient{ws}
	c.sendId(t, idMsg{Type: "register", Account: account, Cert: cert})
	return c
}

func (c *wsClient) sendId(t *testing.T, m idMsg) {
	t.Helper()
	b, _ := json.Marshal(m)
	if err := c.c.Write(context.Background(), websocket.MessageText, b); err != nil {
		t.Fatalf("writing the identity link: %v", err)
	}
}

func (c *wsClient) nextId(t *testing.T) idMsg {
	t.Helper()
	_, b, err := c.c.Read(context.Background())
	if err != nil {
		t.Fatalf("reading the identity link: %v", err)
	}
	var m idMsg
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("unmarshal the identity link: %v", err)
	}
	return m
}
