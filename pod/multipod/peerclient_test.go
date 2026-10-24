package multipod

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"antennae.space/ide/lib/tunnel"
)

// pipeMsg is a whole-message channel over one end of a net.Pipe: what the other end serves.
type pipeMsg struct{ c net.Conn }

func (p pipeMsg) Read() ([]byte, error) {
	b := make([]byte, 64*1024)
	n, err := p.c.Read(b)
	return b[:n], err
}
func (p pipeMsg) Write(b []byte) error { _, err := p.c.Write(b); return err }
func (p pipeMsg) Close() error         { return p.c.Close() }

// chanListener hands the server ends of the channels to an http.Server.
type chanListener struct {
	ch   chan net.Conn
	once sync.Once
	done chan struct{}
}

func (l *chanListener) Accept() (net.Conn, error) {
	select {
	case c := <-l.ch:
		return c, nil
	case <-l.done:
		return nil, net.ErrClosed
	}
}
func (l *chanListener) Close() error   { l.once.Do(func() { close(l.done) }); return nil }
func (l *chanListener) Addr() net.Addr { return &net.TCPAddr{} }

// fakeRemote is a pod reached through the relay: each channel it opens is served by h.
type fakeRemote struct {
	ln     *chanListener
	closed atomic.Bool
}

func (f *fakeRemote) RPC() tunnel.MsgConn { return nil }
func (f *fakeRemote) Open(ctx context.Context, label string) (tunnel.MsgConn, error) {
	a, b := net.Pipe()
	f.ln.ch <- b
	return pipeMsg{a}, nil
}
func (f *fakeRemote) Close() { f.closed.Store(true) }

type fakeDialer struct {
	h     http.Handler
	dials atomic.Int32
	last  *fakeRemote
}

func (d *fakeDialer) Dial(ctx context.Context, key string) (RemotePod, error) {
	d.dials.Add(1)
	ln := &chanListener{ch: make(chan net.Conn), done: make(chan struct{})}
	go func() { _ = http.Serve(ln, d.h) }()
	d.last = &fakeRemote{ln: ln}
	return d.last, nil
}

func get(t *testing.T, c *PeerClient, path string) string {
	t.Helper()
	req, _ := http.NewRequest("GET", "http://bravo-key"+path, nil)
	resp, err := c.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return string(b)
}

// LAN first; the relay otherwise, one tunnel for many requests, closed once idle; back to the
// LAN as soon as the pod is a peer again.
func TestPeerClient(t *testing.T) {
	lanSrv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "lan "+r.URL.Path)
	}))
	defer lanSrv.Close()
	var onLAN atomic.Bool
	onLAN.Store(true)
	d := &fakeDialer{h: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "relay "+r.URL.Path)
	})}
	c := NewPeerClient(d)
	c.Idle = 300 * time.Millisecond
	defer c.Close()
	c.LAN = func(key string) (string, *http.Transport, bool) {
		if !onLAN.Load() || key != "bravo-key" {
			return "", nil, false
		}
		return strings.TrimPrefix(lanSrv.URL, "https://"), lanSrv.Client().Transport.(*http.Transport), true
	}

	if got := get(t, c, "/_peer/x"); got != "lan /_peer/x" || c.Route("bravo-key").Name != tunnel.RouteLAN {
		t.Fatalf("on the LAN: %q %v", got, c.Route("bravo-key"))
	}
	onLAN.Store(false)
	if c.Route("bravo-key").Name != tunnel.RouteIdle {
		t.Fatalf("no tunnel yet: %v", c.Route("bravo-key"))
	}
	for range 3 {
		if got := get(t, c, "/_peer/y"); got != "relay /_peer/y" {
			t.Fatalf("through the relay: %q", got)
		}
	}
	if d.dials.Load() != 1 || c.Route("bravo-key").Name != tunnel.RouteRelayDirect {
		t.Fatalf("one tunnel: %d dials, route %v", d.dials.Load(), c.Route("bravo-key"))
	}
	deadline := time.Now().Add(3 * time.Second)
	for c.Route("bravo-key").Name != tunnel.RouteIdle && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if c.Route("bravo-key").Name != tunnel.RouteIdle || !d.last.closed.Load() {
		t.Fatalf("idle: route %v, closed %v", c.Route("bravo-key"), d.last.closed.Load())
	}
	onLAN.Store(true)
	if got := get(t, c, "/_peer/z"); got != "lan /_peer/z" {
		t.Fatalf("back on the LAN: %q", got)
	}
}
