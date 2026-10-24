package relay

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"antennae.space/ide/lib/identity"
)

// pushed is one POST the fake push service received.
type pushed struct {
	path, auth, encoding, ttl, topic string
	body                             []byte
}

// pushRelay is a relay with push on, a fake push service allowed as the test endpoint, and the
// status the push service answers.
type pushRelay struct {
	ts     *httptest.Server
	srv    *Server
	st     *Store
	got    chan pushed
	status int
	svc    *httptest.Server
}

func newPushRelay(t *testing.T) *pushRelay {
	t.Helper()
	dir := relayDir(t)
	st, err := OpenStore(filepath.Join(dir, "relay.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	p := &pushRelay{st: st, got: make(chan pushed, 8), status: http.StatusCreated}
	p.svc = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		p.got <- pushed{r.URL.Path, r.Header.Get("Authorization"), r.Header.Get("Content-Encoding"), r.Header.Get("TTL"), r.Header.Get("Topic"), b}
		w.WriteHeader(p.status)
	}))
	t.Cleanup(p.svc.Close)
	push, err := LoadPush(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	push.TestEndpoint = p.svc.URL + "/push/"
	p.srv = New(st, nil)
	p.srv.Push = push
	p.ts = httptest.NewServer(p.srv.Handler())
	t.Cleanup(p.ts.Close)
	return p
}

// post sends a signed subscription request of a device.
func (p *pushRelay) post(t *testing.T, path string, dev ed25519.PrivateKey, cert *identity.Cert, endpoint, sub string, created time.Time) (int, map[string]string) {
	t.Helper()
	action, target := "subscribe", endpoint
	if sub != "" {
		action, target = "unsubscribe", sub
	}
	ms := created.UnixMilli()
	body, _ := json.Marshal(pushRequest{Cert: cert, Endpoint: endpoint, Sub: sub, Created: ms,
		Sig: identity.Sign(dev, []byte(pushSigned(action, target, ms)))})
	res, err := http.Post(p.ts.URL+path, "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var out map[string]string
	_ = json.NewDecoder(res.Body).Decode(&out)
	return res.StatusCode, out
}

// podLink registers a pod with a certificate of its account.
func (p *pushRelay) podLink(t *testing.T, id string, a *identity.Account) *wsClient {
	t.Helper()
	c, _, err := websocket.Dial(context.Background(), wsURL(p.ts, "/_relay/pod"), nil)
	if err != nil {
		t.Fatal(err)
	}
	_, key, _ := ed25519.GenerateKey(rand.Reader)
	pc := &wsClient{c}
	t.Cleanup(pc.close)
	pc.send(t, sigMsg{Type: "register", ID: id, Secret: "s-" + id, Account: a.ID, Cert: identity.NewCert(a, key, id, identity.KindPod)})
	if m := pc.next(t); m.Type != "ready" {
		t.Fatalf("the pod must get ready, got %q", m.Type)
	}
	return pc
}

func (c *wsClient) sendPush(t *testing.T, m pushSend) {
	t.Helper()
	b, _ := json.Marshal(map[string]any{"type": "push.send", "sub": m.Sub, "body": m.Body, "ttl": m.TTL, "urgency": m.Urgency, "topic": m.Topic})
	if err := c.c.Write(context.Background(), websocket.MessageText, b); err != nil {
		t.Fatal(err)
	}
}

func (p *pushRelay) none(t *testing.T, why string) {
	t.Helper()
	select {
	case g := <-p.got:
		t.Fatalf("%s: the push service got %s", why, g.path)
	case <-time.After(150 * time.Millisecond):
	}
}

func browser(t *testing.T, a *identity.Account, name string) (ed25519.PrivateKey, *identity.Cert) {
	_, dev, _ := ed25519.GenerateKey(rand.Reader)
	return dev, identity.NewCert(a, dev, name, identity.KindBrowser)
}

// TestPushForward: a browser registers its endpoint with a signed request; the pod of its
// account sends ciphertext for it, which the relay POSTs signed with its VAPID key.
func TestPushForward(t *testing.T) {
	p := newPushRelay(t)
	a := testAccount(t)
	dev, cert := browser(t, a, "phone")
	endpoint := p.svc.URL + "/push/abc"

	// The public key, and the VAPID key kept in the data folder.
	res, err := http.Get(p.ts.URL + "/_relay/push/key")
	if err != nil {
		t.Fatal(err)
	}
	var key struct{ Key string }
	_ = json.NewDecoder(res.Body).Decode(&key)
	res.Body.Close()
	if raw, _ := identity.B64d(key.Key); len(raw) != 65 || raw[0] != 4 {
		t.Fatalf("the VAPID public key is an uncompressed P-256 point: %q", key.Key)
	}

	code, out := p.post(t, "/_relay/push/subscribe", dev, cert, endpoint, "", time.Now())
	if code != 200 || out["sub"] == "" {
		t.Fatalf("subscribe: %d %v", code, out)
	}
	sub := out["sub"]
	// The same endpoint keeps its id.
	if _, again := p.post(t, "/_relay/push/subscribe", dev, cert, endpoint, "", time.Now()); again["sub"] != sub {
		t.Fatalf("the same endpoint keeps its id: %v", again)
	}

	pod := p.podLink(t, "pod-a", a)
	cipher := []byte("ciphertext only")
	pod.sendPush(t, pushSend{Sub: sub, Body: cipher, TTL: 60, Urgency: "high", Topic: "chat1"})
	var g pushed
	select {
	case g = <-p.got:
	case <-time.After(5 * time.Second):
		t.Fatal("the push service got nothing")
	}
	if g.path != "/push/abc" || !bytes.Equal(g.body, cipher) || g.encoding != "aes128gcm" || g.ttl != "60" || g.topic != "chat1" {
		t.Fatalf("the POST: %+v", g)
	}
	// Authorization: vapid t=<jwt>, k=<public key>; the JWT verifies with that key.
	parts := strings.SplitN(strings.TrimPrefix(g.auth, "vapid t="), ", k=", 2)
	if len(parts) != 2 || parts[1] != key.Key || !verifyJWT(parts[0], key.Key) {
		t.Fatalf("the VAPID signature must verify: %q", g.auth)
	}
	claims, _ := identity.B64d(strings.Split(parts[0], ".")[1])
	if !strings.Contains(string(claims), `"aud":"`+p.svc.URL+`"`) {
		t.Fatalf("the audience is the origin of the push service: %s", claims)
	}

	// Nothing of the payload in the base.
	db, _ := os.ReadFile(p.st.DBPath())
	if bytes.Contains(db, cipher) {
		t.Fatal("the base holds no payload")
	}
}

// TestPushRefused: the relay is never an open push proxy.
func TestPushRefused(t *testing.T) {
	p := newPushRelay(t)
	a := testAccount(t)
	dev, cert := browser(t, a, "phone")
	endpoint := p.svc.URL + "/push/abc"

	// An endpoint that is no known push service, a bad signature, an old request.
	if code, _ := p.post(t, "/_relay/push/subscribe", dev, cert, "http://127.0.0.1:1/admin", "", time.Now()); code != 403 {
		t.Fatalf("an endpoint of the relay's network is refused: %d", code)
	}
	if code, _ := p.post(t, "/_relay/push/subscribe", dev, cert, "https://fcm.googleapis.com:8443/x", "", time.Now()); code != 403 {
		t.Fatalf("a push service on another port is refused: %d", code)
	}
	_, other := browser(t, a, "other")
	if code, _ := p.post(t, "/_relay/push/subscribe", dev, other, endpoint, "", time.Now()); code != 403 {
		t.Fatalf("a signature by another key than the certificate's is refused: %d", code)
	}
	if code, _ := p.post(t, "/_relay/push/subscribe", dev, cert, endpoint, "", time.Now().Add(-time.Hour)); code != 403 {
		t.Fatalf("an old request is refused: %d", code)
	}
	if !p.srv.Push.allowed("https://fcm.googleapis.com/fcm/send/x") || !p.srv.Push.allowed("https://wns2-db5p.notify.windows.com/w/?token=x") {
		t.Fatal("the push services of the browsers are allowed")
	}

	_, out := p.post(t, "/_relay/push/subscribe", dev, cert, endpoint, "", time.Now())
	sub := out["sub"]

	// An unknown subscription: nothing is sent, the pod is told it is gone.
	pod := p.podLink(t, "pod-a", a)
	pod.sendPush(t, pushSend{Sub: "unknown", Body: []byte("x")})
	if m := pod.next(t); m.Type != "push.gone" || m.ID != "unknown" {
		t.Fatalf("an unknown sub is gone: %+v", m)
	}
	p.none(t, "unknown sub")

	// A pod of another account cannot reach the subscription.
	other2, err := identity.FromPhrase("abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon about")
	if err != nil {
		t.Fatal(err)
	}
	foreign := p.podLink(t, "pod-b", other2)
	foreign.sendPush(t, pushSend{Sub: sub, Body: []byte("x")})
	if m := foreign.next(t); m.Type != "push.gone" {
		t.Fatalf("another account's pod is refused: %+v", m)
	}
	p.none(t, "other account")

	// A pod that claims the account without a certificate cannot send.
	c, _, _ := websocket.Dial(context.Background(), wsURL(p.ts, "/_relay/pod"), nil)
	claimer := &wsClient{c}
	defer claimer.close()
	claimer.send(t, sigMsg{Type: "register", ID: "pod-c", Secret: "s", Account: a.ID})
	claimer.next(t)
	claimer.sendPush(t, pushSend{Sub: sub, Body: []byte("x")})
	p.none(t, "no certificate")

	// The rate limit of the account.
	sent := 0
	for i := 0; i < int(pushBurst)+5; i++ {
		if p.srv.Push.take(a.ID + "-rate") {
			sent++
		}
	}
	if sent != int(pushBurst) {
		t.Fatalf("the burst of an account is %v, got %d", pushBurst, sent)
	}
}

// TestPushGone: a push service that answers 410 dropped the subscription: the relay deletes it
// and tells the pod. Revoking a device deletes its subscriptions.
func TestPushGone(t *testing.T) {
	p := newPushRelay(t)
	a := testAccount(t)
	dev, cert := browser(t, a, "phone")
	_, out := p.post(t, "/_relay/push/subscribe", dev, cert, p.svc.URL+"/push/1", "", time.Now())
	sub := out["sub"]
	pod := p.podLink(t, "pod-a", a)
	p.status = http.StatusGone
	pod.sendPush(t, pushSend{Sub: sub, Body: []byte("x")})
	<-p.got
	if m := pod.next(t); m.Type != "push.gone" || m.ID != sub {
		t.Fatalf("the pod is told: %+v", m)
	}
	if _, _, ok := p.st.PushSub(sub); ok {
		t.Fatal("a 410 deletes the subscription")
	}

	// Unsubscribe by the device itself.
	_, out = p.post(t, "/_relay/push/subscribe", dev, cert, p.svc.URL+"/push/2", "", time.Now())
	if code, _ := p.post(t, "/_relay/push/unsubscribe", dev, cert, "", out["sub"], time.Now()); code != 200 {
		t.Fatalf("unsubscribe: %d", code)
	}
	if _, _, ok := p.st.PushSub(out["sub"]); ok {
		t.Fatal("unsubscribe deletes the subscription")
	}

	// Revoked: its subscriptions go, and it cannot register again.
	_, out = p.post(t, "/_relay/push/subscribe", dev, cert, p.svc.URL+"/push/3", "", time.Now())
	r := &identity.Revocations{Seq: 1, Epoch: 1, Revoked: []string{cert.Key}, Created: time.Now().Unix()}
	_ = identity.SignRevocations(a.Priv, r)
	if kept, err := p.st.SaveRevocations(r); err != nil || !kept {
		t.Fatal(kept, err)
	}
	if _, _, ok := p.st.PushSub(out["sub"]); ok {
		t.Fatal("revoking a device deletes its subscriptions")
	}
	if code, _ := p.post(t, "/_relay/push/subscribe", dev, cert, p.svc.URL+"/push/4", "", time.Now()); code != 403 {
		t.Fatalf("a revoked device cannot subscribe: %d", code)
	}
}
