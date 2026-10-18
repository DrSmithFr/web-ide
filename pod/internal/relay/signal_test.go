package relay

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// wsClient is a small helper around coder/websocket for the signaling tests.
type wsClient struct{ c *websocket.Conn }

func (c *wsClient) next(t *testing.T) sigMsg {
	t.Helper()
	_, b, err := c.c.Read(context.Background())
	if err != nil {
		t.Fatalf("reading signaling: %v", err)
	}
	var m sigMsg
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("unmarshal signaling: %v", err)
	}
	return m
}

func (c *wsClient) send(t *testing.T, m sigMsg) {
	t.Helper()
	b, _ := json.Marshal(m)
	if err := c.c.Write(context.Background(), websocket.MessageText, b); err != nil {
		t.Fatalf("writing signaling: %v", err)
	}
}

func (c *wsClient) close() { _ = c.c.CloseNow() }

func newRelay(t *testing.T) *httptest.Server {
	t.Helper()
	st, _ := OpenStore(filepath.Join(relayDir(t), "relay.db"))
	t.Cleanup(func() { st.Close() })
	turn, _ := startTurn(t, st, TurnConfig{AdvertiseIP: "127.0.0.1"})
	t.Cleanup(func() { turn.Close() })
	srv := New(st, turn)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts
}

func wsURL(ts *httptest.Server, path string) string {
	u := ts.URL
	if len(u) > 5 && u[:5] == "http:" {
		u = "ws:" + u[5:]
	}
	return u + path
}

func connectPod(t *testing.T, ts *httptest.Server, id, secret, name string) *wsClient {
	t.Helper()
	c, _, err := websocket.Dial(context.Background(), wsURL(ts, "/_relay/pod"), nil)
	if err != nil {
		t.Fatal("dialing the pod link:", err)
	}
	pc := &wsClient{c}
	pc.send(t, sigMsg{Type: "register", ID: id, Secret: secret, Name: name})
	if m := pc.next(t); m.Type != "ready" {
		t.Fatalf("expected the pod to get ready, got %q", m.Type)
	}
	return pc
}

// connectBrowser opens a browser signaling link in one session of a pod.
func connectBrowser(t *testing.T, ts *httptest.Server, podID, session string) *wsClient {
	t.Helper()
	c, _, err := websocket.Dial(context.Background(), wsURL(ts, "/_relay/signal?pod="+podID+"&session="+session), nil)
	if err != nil {
		t.Fatal("dialing the browser link:", err)
	}
	bc := &wsClient{c}
	if m := bc.next(t); m.Type != "ice" {
		t.Fatalf("expected the browser to get ice, got %q", m.Type)
	}
	return bc
}

// TestSignalingSessions: two browsers on one pod, each in its own session; the relay
// routes the pod's answer and candidates to the right browser only.
func TestSignalingSessions(t *testing.T) {
	ts := newRelay(t)
	pod := connectPod(t, ts, "pod-a", "secret-1", "laptop")
	defer pod.close()

	a := connectBrowser(t, ts, "pod-a", "sA")
	defer a.close()
	b := connectBrowser(t, ts, "pod-a", "sB")
	defer b.close()

	// The browser's offer must reach the pod, tagged with the session.
	a.send(t, sigMsg{Type: "offer", Session: "sA", SDP: "OFFER-A"})
	if m := pod.next(t); m.Type != "ice" {
		t.Fatalf("expected the pod to get fresh ice, got %q", m.Type)
	}
	if m := pod.next(t); m.Type != "offer" || m.Session != "sA" || m.SDP != "OFFER-A" {
		t.Fatalf("expected the pod to get the A offer, got %q %q %q", m.Type, m.Session, m.SDP)
	}

	// A candidate from session B must not be confused with A.
	b.send(t, sigMsg{Type: "candidate", Session: "sB", Candidate: json.RawMessage(`{"candidate":"cB"}`)})
	if m := pod.next(t); m.Type != "candidate" || m.Session != "sB" {
		t.Fatalf("expected the pod to get the B candidate, got %q %q", m.Type, m.Session)
	}

	// The pod's answer to A goes only to A (B gets nothing).
	pod.send(t, sigMsg{Type: "answer", Session: "sA", SDP: "ANSWER-A"})
	if m := a.next(t); m.Type != "answer" || m.Session != "sA" || m.SDP != "ANSWER-A" {
		t.Fatalf("expected A to get the answer, got %q %q %q", m.Type, m.Session, m.SDP)
	}
	// B must not have received A's answer.
	got := make(chan struct{}, 1)
	go func() {
		if _, _, err := b.c.Read(context.Background()); err == nil {
			got <- struct{}{}
		}
	}()
	select {
	case <-got:
		t.Fatal("B must not have received A's answer")
	case <-time.After(50 * time.Millisecond):
	}
}

func TestSignalingWrongSecret(t *testing.T) {
	ts := newRelay(t)
	pod := connectPod(t, ts, "pod-a", "secret-1", "")
	defer pod.close()
	// A second pod link with a different secret must be refused (the id is registered).
	c, _, err := websocket.Dial(context.Background(), wsURL(ts, "/_relay/pod"), nil)
	if err != nil {
		t.Fatal("dialing the second pod link:", err)
	}
	defer c.CloseNow()
	b, _ := json.Marshal(sigMsg{Type: "register", ID: "pod-a", Secret: "WRONG"})
	_ = c.Write(context.Background(), websocket.MessageText, b)
	_, rb, err := c.Read(context.Background())
	if err != nil {
		t.Fatal("expected an error back, got none:", err)
	}
	var m sigMsg
	_ = json.Unmarshal(rb, &m)
	if m.Type != "error" {
		t.Fatalf("a wrong secret must give an error, got %q", m.Type)
	}
}

// relayDir is a data folder for a test relay. Not t.TempDir: the handler of a pod link may still
// write to the base (the pod offline) as the test ends, and the cleanup of t.TempDir then fails
// on the journal file it finds.
func relayDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "relay-test")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

// TestCloseEndsLinks (#102): a pod link still open when the relay closes is ended, and its
// handler has committed the pod offline by the time Close returns: nothing writes to the base
// afterwards (a test removing the folder of the base right after used to fail at random).
func TestCloseEndsLinks(t *testing.T) {
	st, err := OpenStore(filepath.Join(t.TempDir(), "relay.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	srv := New(st, nil)
	ts := httptest.NewServer(srv.Handler())
	pod := connectPod(t, ts, "pod-c", "secret-c", "laptop")
	defer pod.close()
	if !st.OnlinePod("pod-c") {
		t.Fatal("the pod is not online")
	}
	ts.Close()
	srv.Close()
	if st.OnlinePod("pod-c") {
		t.Fatal("Close returned before the handler of the link marked the pod offline")
	}
	if _, _, err := pod.c.Read(context.Background()); err == nil {
		t.Fatal("the link is still open after Close")
	}
}

// TestSignalingRenewal (#86): a browser asks fresh TURN credentials on its session and gets them
// with their ttl, under the quota as the first ones; a pod that does not answer an offer on a live
// session gets its browsers no ttl (they never renew); and a session the pod closes is told to its
// browser.
func TestSignalingRenewal(t *testing.T) {
	st, _ := OpenStore(filepath.Join(relayDir(t), "relay.db"))
	t.Cleanup(func() { st.Close() })
	turn, _ := startTurn(t, st, TurnConfig{AdvertiseIP: "127.0.0.1", Quota: 1000, TTL: time.Minute})
	t.Cleanup(func() { turn.Close() })
	ts := httptest.NewServer(New(st, turn).Handler())
	t.Cleanup(ts.Close)

	c, _, err := websocket.Dial(context.Background(), wsURL(ts, "/_relay/pod"), nil)
	if err != nil {
		t.Fatal(err)
	}
	pod := &wsClient{c}
	defer pod.close()
	pod.send(t, sigMsg{Type: "register", ID: "pod-r", Secret: "secret-r", Renew: true})
	if m := pod.next(t); m.Type != "ready" {
		t.Fatalf("expected ready, got %q", m.Type)
	}
	old := connectPod(t, ts, "pod-old", "secret-o", "old")
	defer old.close()

	// The first credentials and the renewed ones carry their ttl.
	c, _, err = websocket.Dial(context.Background(), wsURL(ts, "/_relay/signal?pod=pod-r&session=sR"), nil)
	if err != nil {
		t.Fatal(err)
	}
	b := &wsClient{c}
	defer b.close()
	first := b.next(t)
	if first.Type != "ice" || first.Username == "" || first.TTL < 55 || first.TTL > 60 {
		t.Fatalf("first ice = %+v", first)
	}
	b.send(t, sigMsg{Type: "ice", Session: "sR"})
	if m := b.next(t); m.Type != "ice" || m.Username == "" || m.TTL < 55 || m.Quota != nil {
		t.Fatalf("renewed ice = %+v", m)
	}

	// Over the quota the renewal gives none, and says why.
	if err := st.AddUsage("pod-r", usageMonth(time.Now()), 2000, 0); err != nil {
		t.Fatal(err)
	}
	b.send(t, sigMsg{Type: "ice", Session: "sR"})
	if m := b.next(t); m.Type != "ice" || m.Username != "" || m.Credential != "" || m.Quota == nil || !m.Quota.Over || m.TTL != 0 {
		t.Fatalf("ice over the quota = %+v", m)
	}

	// The pod closes the session: its browser is told.
	pod.send(t, sigMsg{Type: "bye", Session: "sR"})
	if m := b.next(t); m.Type != "bye" || m.Session != "sR" {
		t.Fatalf("expected the bye of the pod, got %+v", m)
	}

	// An older pod: credentials, no ttl.
	ob := connectBrowser(t, ts, "pod-old", "sO")
	defer ob.close()
	ob.send(t, sigMsg{Type: "ice", Session: "sO"})
	if m := ob.next(t); m.Type != "ice" || m.Username == "" || m.TTL != 0 {
		t.Fatalf("ice of a pod that does not renew = %+v", m)
	}
}
