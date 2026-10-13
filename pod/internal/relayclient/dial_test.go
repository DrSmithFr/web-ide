package relayclient

// Pod to pod WebRTC (milestone 3b): a paired pod plays the browser against another paired pod,
// through the relay's signaling. The test drives it with two real pods on a real relay: the
// handshake is the browser's v2, so a swapped offer, a pod of another account and a key that is
// not the one asked for are all refused.

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/DrSmithFr/web-ide/pod/internal/identity"
	"github.com/DrSmithFr/web-ide/pod/internal/tunnel"
)

// readMsg reads one protocol message off a whole-message channel.
func readMsg(t *testing.T, conn tunnel.MsgConn, d time.Duration) envelope {
	t.Helper()
	ch := make(chan envelope, 1)
	go func() {
		b, err := conn.Read()
		if err != nil {
			return
		}
		var e envelope
		if json.Unmarshal(b, &e) == nil {
			ch <- e
		}
	}()
	select {
	case e := <-ch:
		return e
	case <-time.After(d):
		t.Fatalf("no message within %s", d)
	}
	return envelope{}
}

// rpcCallMsg runs one request on a whole-message channel and waits for its answer.
func rpcCallMsg(t *testing.T, conn tunnel.MsgConn, id int64, method string, params any) envelope {
	t.Helper()
	data, _ := json.Marshal(map[string]any{"id": id, "method": method, "params": params})
	if err := conn.Write(data); err != nil {
		t.Fatalf("write %s: %v", method, err)
	}
	for {
		e := readMsg(t, conn, 20*time.Second)
		if e.ID == id {
			if len(e.Error) > 0 {
				t.Fatalf("%s: %s", method, string(e.Error))
			}
			return e
		}
	}
}

// dialTry dials a pod-to-pod tunnel, a few times: the TURN allocation of a relay that was just
// started can take a moment, and a tunnel that stalled is worth dialling again.
func dialTry(ctx context.Context, cli *Client, podID, key string) (*Peer, error) {
	var peer *Peer
	var err error
	for i := 0; i < 4; i++ {
		// One attempt per deadline: a tunnel that stalls must not eat the whole budget. A pod
		// that just registered is worth another try a moment later.
		actx, cancel := context.WithTimeout(ctx, 12*time.Second)
		peer, err = cli.Dial(actx, podID, key, nil)
		cancel()
		if err == nil {
			return peer, nil
		}
		if peer != nil {
			peer.Close()
		}
		time.Sleep(400 * time.Millisecond)
	}
	return nil, err
}

// wsURL is the relay of an env as a signaling base.
func wsURL(env *testEnv) string {
	return strings.Replace(env.httpSrv.URL, "http", "ws", 1)
}

// startSecond starts another pod of the account on the relay of the env, with its own device key
// and pod id, and waits until the relay sees it online.
func startSecond(t *testing.T, env *testEnv, ident *identity.PodIdentity, podID string) *Client {
	t.Helper()
	return startSecondSignal(t, env, ident, podID, "")
}

func startSecondSignal(t *testing.T, env *testEnv, ident *identity.PodIdentity, podID, signal string) *Client {
	t.Helper()
	if err := env.st.Register(podID, "secret-two", "second"); err != nil {
		t.Fatal(err)
	}
	s, _ := buildPod(t)
	cli := New(Config{
		RelayURL:  wsURL(env),
		SignalURL: signal,
		PodID:     podID,
		Secret:    "secret-two",
		Server:    s,
		Token:     podToken,
		Identity:  ident,
		ForceTurn: true,
	})
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go cli.Run(ctx)
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) && !env.st.OnlinePod(podID) {
		time.Sleep(50 * time.Millisecond)
	}
	if !env.st.OnlinePod(podID) {
		t.Fatal("the second pod did not register with the relay")
	}
	return cli
}

// pairAccount makes a pod identity paired with the account of the test vectors.
func pairAccount(t *testing.T, a *identity.Account, name string) *identity.PodIdentity {
	t.Helper()
	p, err := identity.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Pin(a.ID); err != nil {
		t.Fatal(err)
	}
	if err := p.SetCert(identity.NewCert(a, p.Key(), name, identity.KindPod)); err != nil {
		t.Fatal(err)
	}
	return p
}

// TestDialPodToPod: pod alpha dials pod bravo through the relay and serves itself as its
// gateway: the rpc channel of the window carries the protocol of the end pod.
func TestDialPodToPod(t *testing.T) {
	a, err := identity.FromPhrase(vectorsPhrase)
	if err != nil {
		t.Fatal(err)
	}
	podA := pairAccount(t, a, "alpha")
	podB := pairAccount(t, a, "bravo")
	env := startEnv(t, podA, nil)
	startSecond(t, env, podB, "bravo-pod")

	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	peer, err := dialTry(ctx, env.cli, "bravo-pod", podB.ID())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer peer.Close()

	hello := readMsg(t, peer.RPC(), 10*time.Second)
	if hello.Event != "hello" {
		t.Fatalf("expected the end pod hello, got %q", hello.Event)
	}
	res := rpcCallMsg(t, peer.RPC(), 1, "workspace.get", map[string]any{})
	if res.Error != nil {
		t.Fatalf("workspace.get on the end pod: %s", string(res.Error))
	}
	var ws map[string]string
	if err := json.Unmarshal(res.Result, &ws); err != nil || ws["version"] == "" {
		t.Fatalf("workspace.get result = %s", string(res.Result))
	}

	// A channel of the same window: one fetch of the end pod's page, under the path the gateway
	// serves it at.
	ch, err := peer.Open(ctx, "http:1")
	if err != nil {
		t.Fatalf("open http:1: %v", err)
	}
	defer ch.Close()
	q, _ := json.Marshal(map[string]any{"path": "/", "method": "GET", "base": "/_lan/pod/" + podB.ID()})
	if err := ch.Write(q); err != nil {
		t.Fatal(err)
	}
	head, err := ch.Read()
	if err != nil {
		t.Fatal(err)
	}
	var h struct {
		Status int `json:"status"`
	}
	if err := json.Unmarshal(head, &h); err != nil || h.Status != 200 {
		t.Fatalf("head = %s", string(head))
	}
	var page []byte
	for {
		b, err := ch.Read()
		if err != nil {
			t.Fatal(err)
		}
		if len(b) == 0 {
			break
		}
		page = append(page, b...)
	}
	if !strings.Contains(string(page), `src="/_lan/pod/`+podB.ID()+`/assets/app.js"`) {
		t.Fatalf("the page does not run under the gateway path: %s", string(page))
	}
}

// The pod at the other end must be the one asked for: a tunnel opened on the right pod id but
// answering with another device key is refused.
func TestDialWrongKey(t *testing.T) {
	a, err := identity.FromPhrase(vectorsPhrase)
	if err != nil {
		t.Fatal(err)
	}
	podA := pairAccount(t, a, "alpha")
	podB := pairAccount(t, a, "bravo")
	env := startEnv(t, podA, nil)
	startSecond(t, env, podB, "bravo-pod")

	_, foreign, _ := ed25519.GenerateKey(nil)
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	_, err = dialTry(ctx, env.cli, "bravo-pod", identity.KeyID(foreign))
	if err == nil {
		t.Fatal("the tunnel accepted a pod that is not the one asked for")
	}
	if !strings.Contains(err.Error(), podB.ID()) {
		t.Fatalf("refusal = %v, it should name the pod expected", err)
	}
}

// A pod of another account is refused by the handshake, whatever pod id it registered with.
func TestDialForeignPod(t *testing.T) {
	a, err := identity.FromPhrase(vectorsPhrase)
	if err != nil {
		t.Fatal(err)
	}
	// Another account: another phrase, made here rather than typed.
	entropy := make([]byte, 32)
	for i := range entropy {
		entropy[i] = 7
	}
	phrase, err := identity.Generate(entropy)
	if err != nil {
		t.Fatal(err)
	}
	other, err := identity.FromPhrase(phrase)
	if err != nil {
		t.Fatal(err)
	}
	podA := pairAccount(t, a, "alpha")
	podB := pairAccount(t, other, "bravo")
	env := startEnv(t, podA, nil)
	startSecond(t, env, podB, "bravo-pod")

	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	_, err = dialTry(ctx, env.cli, "bravo-pod", podB.ID())
	if err == nil {
		t.Fatal("a pod of another account was accepted")
	}
	if !strings.Contains(err.Error(), "foreign") {
		t.Fatalf("refusal = %v, want foreign", err)
	}
}

// A relay that rewrites the offer changes a DTLS fingerprint, so the signature the end pod
// signs no longer matches what the dialing pod reads: the tunnel is refused.
func TestDialSwappedOffer(t *testing.T) {
	a, err := identity.FromPhrase(vectorsPhrase)
	if err != nil {
		t.Fatal(err)
	}
	podA := pairAccount(t, a, "alpha")
	podB := pairAccount(t, a, "bravo")
	env := startEnv(t, podA, nil)
	startSecond(t, env, podB, "bravo-pod")

	// A third pod, same account, whose signaling link goes through a relay that rewrites offers.
	podC := pairAccount(t, a, "carol")
	cli := startSecondSignal(t, env, podC, "carol-pod", swapSignal(t, wsURL(env)))

	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	// The tunnel is refused: the end pod signed a binding built from the fingerprint the proxy
	// rewrote, which no longer matches what the dialing pod reads (or the peer drops the tunnel
	// once it sees the proof of the other side is gone).
	if _, err := dialTry(ctx, cli, "bravo-pod", podB.ID()); err == nil {
		t.Fatal("a swapped offer was accepted")
	}
	// The same pod, dialled straight at the relay, is reached: the refusal comes from the
	// rewritten offer, not from the certificates.
	peer, err := dialTry(ctx, env.cli, "bravo-pod", podB.ID())
	if err != nil {
		t.Fatalf("the same pod, dialled straight: %v", err)
	}
	peer.Close()
}

// swapSignal is a relay that rewrites the offer of the dialing pod; everything else is carried
// as it is.
func swapSignal(t *testing.T, relayURL string) string {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
		if err != nil {
			return
		}
		defer ws.CloseNow()
		up, _, err := websocket.Dial(ctx, relayURL+"/_relay/signal?"+r.URL.RawQuery, nil)
		if err != nil {
			return
		}
		defer up.CloseNow()
		go func() {
			for {
				_, b, err := ws.Read(ctx)
				if err != nil {
					return
				}
				var m sigMsg
				if err := json.Unmarshal(b, &m); err == nil && m.Type == "offer" {
					m.SDP = swapFingerprint(m.SDP)
					b, _ = json.Marshal(m)
				}
				if err := up.Write(ctx, websocket.MessageText, b); err != nil {
					return
				}
			}
		}()
		for {
			_, b, err := up.Read(ctx)
			if err != nil {
				return
			}
			if err := ws.Write(ctx, websocket.MessageText, b); err != nil {
				return
			}
		}
	})
	ts := httptest.NewServer(handler)
	t.Cleanup(ts.Close)
	return strings.Replace(ts.URL, "http", "ws", 1)
}
