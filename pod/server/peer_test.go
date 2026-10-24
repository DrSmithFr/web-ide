package server

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/coder/websocket"

	"antennae.space/ide/lib/identity"
	"antennae.space/ide/lib/tunnel"
)

var (
	podCert     = &identity.Cert{Key: "alpha-key", Name: "alpha", Kind: identity.KindPod}
	browserCert = &identity.Cert{Key: "phone-key", Name: "phone", Kind: identity.KindBrowser}
)

// peerServer is a pod whose plugin "svc" answers the pods with the name of who asks, and echoes
// on a WebSocket.
func peerServer(t *testing.T) *Server {
	s, _ := newServer(t)
	s.plugins.peers = map[string]http.Handler{"svc": http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/ws" {
			c, err := websocket.Accept(w, r, nil)
			if err != nil {
				return
			}
			defer c.CloseNow()
			typ, b, err := c.Read(r.Context())
			if err == nil {
				_ = c.Write(r.Context(), typ, append([]byte("echo "), b...))
			}
			return
		}
		_, _ = w.Write([]byte(identity.PeerOf(r.Context()).Name + " " + r.URL.Path))
	})}
	return s
}

// The routes of the pods answer a pod of the LAN (ServeTrusted with its certificate), never a
// window of the pod's own origin, even with its token, nor a browser.
func TestPeerRoutesLAN(t *testing.T) {
	s := peerServer(t)
	get := func(peer *identity.Cert, trusted bool) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", "/_peer/svc/x", nil)
		r.RemoteAddr = "127.0.0.1:1"
		r.Header.Set("Cookie", cookieName+"="+s.Token)
		r = r.WithContext(identity.WithPeer(r.Context(), peer))
		w := httptest.NewRecorder()
		if trusted {
			s.ServeTrusted(w, r)
		} else {
			s.ServeHTTP(w, r)
		}
		return w
	}
	if w := get(nil, false); w.Code != http.StatusForbidden {
		t.Fatalf("own origin with the token: %d", w.Code)
	}
	if w := get(browserCert, true); w.Code != http.StatusForbidden {
		t.Fatalf("a browser: %d", w.Code)
	}
	if w := get(podCert, true); w.Code != 200 || w.Body.String() != "alpha /x" {
		t.Fatalf("a pod of the LAN: %d %q", w.Code, w.Body)
	}
	r := httptest.NewRequest("GET", "/_peer/nope/x", nil)
	r.Header.Set("Cookie", cookieName+"="+s.Token)
	w := httptest.NewRecorder()
	s.ServeTrusted(w, r.WithContext(identity.WithPeer(r.Context(), podCert)))
	if w.Code != http.StatusNotFound {
		t.Fatalf("no plugin of that id: %d", w.Code)
	}
}

// Through a WebRTC tunnel, the routes of the pods answer when a pod opened it.
func TestPeerRoutesTunnel(t *testing.T) {
	for _, tc := range []struct {
		peer *identity.Cert
		want float64
	}{{podCert, 200}, {browserCert, 403}, {nil, 403}} {
		s := peerServer(t)
		p := newMsgPipe()
		ctx, cancel := context.WithCancel(context.Background())
		chans := make(chan tunnel.LabeledConn, 4)
		go s.ServeTunnel(identity.WithPeer(ctx, tc.peer), p.server, chans)
		head, body := httpChan(t, chans, 1, map[string]any{"path": "/_peer/svc/y"})
		if head["status"] != tc.want || (tc.want == 200 && body != "alpha /y") {
			t.Fatalf("peer %v: %v %q", tc.peer, head, body)
		}
		cancel()
	}
}

// A conn:<n> channel carries a WebSocket of a pod to the routes of the pods.
func TestPeerConnChannel(t *testing.T) {
	s := peerServer(t)
	p := newMsgPipe()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	chans := make(chan tunnel.LabeledConn, 4)
	go s.ServeTunnel(identity.WithPeer(ctx, podCert), p.server, chans)
	conn := newMsgPipe()
	chans <- tunnel.LabeledConn{Label: "conn:1", Conn: conn.server}
	client := &http.Client{Transport: &http.Transport{DialContext: func(context.Context, string, string) (net.Conn, error) {
		return tunnel.NetConn(conn.client), nil
	}}}
	c, _, err := websocket.Dial(ctx, "ws://pod/_peer/svc/ws", &websocket.DialOptions{HTTPClient: client})
	if err != nil {
		t.Fatal(err)
	}
	defer c.CloseNow()
	if err := c.Write(ctx, websocket.MessageText, []byte("hi")); err != nil {
		t.Fatal(err)
	}
	if _, b, err := c.Read(ctx); err != nil || string(b) != "echo hi" {
		t.Fatalf("echo: %q %v", b, err)
	}
}
