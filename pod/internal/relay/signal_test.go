package relay

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"testing"

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
	st, _ := OpenStore(filepath.Join(t.TempDir(), "relay.db"))
	t.Cleanup(func() { st.Close() })
	turn, err := NewTurn(st, TurnConfig{AdvertiseIP: "127.0.0.1", Port: 0})
	if err != nil {
		t.Fatal("starting the embedded TURN:", err)
	}
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

func connectBrowser(t *testing.T, ts *httptest.Server, podID string) *wsClient {
	t.Helper()
	c, _, err := websocket.Dial(context.Background(), wsURL(ts, "/_relay/signal?pod="+podID), nil)
	if err != nil {
		t.Fatal("dialing the browser link:", err)
	}
	bc := &wsClient{c}
	if m := bc.next(t); m.Type != "ice" {
		t.Fatalf("expected the browser to get ice, got %q", m.Type)
	}
	return bc
}

func TestSignalingRouting(t *testing.T) {
	ts := newRelay(t)
	pod := connectPod(t, ts, "pod-a", "secret-1", "laptop")
	defer pod.close()
	browser := connectBrowser(t, ts, "pod-a")
	defer browser.close()

	// The browser's offer must reach the pod, with fresh credentials before it.
	browser.send(t, sigMsg{Type: "offer", SDP: "OFFER-SDP"})
	if m := pod.next(t); m.Type != "ice" {
		t.Fatalf("expected the pod to get fresh ice, got %q", m.Type)
	}
	if m := pod.next(t); m.Type != "offer" || m.SDP != "OFFER-SDP" {
		t.Fatalf("expected the pod to get the offer, got %q %q", m.Type, m.SDP)
	}

	// The pod's answer and candidates must reach the browser.
	pod.send(t, sigMsg{Type: "answer", SDP: "ANSWER-SDP"})
	if m := browser.next(t); m.Type != "answer" || m.SDP != "ANSWER-SDP" {
		t.Fatalf("expected the browser to get the answer, got %q %q", m.Type, m.SDP)
	}
	pod.send(t, sigMsg{Type: "candidate", Candidate: json.RawMessage(`{"candidate":"c1"}`)})
	if m := browser.next(t); m.Type != "candidate" {
		t.Fatalf("expected the browser to get a candidate, got %q", m.Type)
	}
}

func TestSignalingWrongSecret(t *testing.T) {
	ts := newRelay(t)
	pod := connectPod(t, ts, "pod-a", "secret-1", "")
	defer pod.close()
	// A second pod link with a different secret must be refused (the id is already registered).
	c, _, err := websocket.Dial(context.Background(), wsURL(ts, "/_relay/pod"), nil)
	if err != nil {
		t.Fatal("dialing the second pod link:", err)
	}
	defer c.CloseNow()
	_ = c.Write(context.Background(), websocket.MessageText, mustJSON(sigMsg{Type: "register", ID: "pod-a", Secret: "WRONG"}))
	if m := readMsg(t, c); m.Type != "error" {
		t.Fatalf("a wrong secret must give an error, got %q", m.Type)
	}
}

func mustJSON(m sigMsg) []byte {
	b, _ := json.Marshal(m)
	return b
}

func readMsg(t *testing.T, c *websocket.Conn) sigMsg {
	t.Helper()
	_, b, err := c.Read(context.Background())
	if err != nil {
		t.Fatalf("reading signaling: %v", err)
	}
	var m sigMsg
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("unmarshal signaling: %v", err)
	}
	return m
}
