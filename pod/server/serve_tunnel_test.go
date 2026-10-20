package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/DrSmithFr/web-ide/lib/tunnel"
)

// msgChannel is one end of an in-memory whole-message transport: a Read returns one
// message, a Write sends one message to the other end. It plays the WebRTC DataChannel
// for the tests, so ServeTunnel is exercised without a real socket.
type msgChannel struct {
	incoming  chan []byte
	outgoing  chan []byte
	closed    chan struct{}
	closeOnce func()
}

func newMsgChannel() *msgChannel {
	c := &msgChannel{incoming: make(chan []byte, 64), outgoing: make(chan []byte, 64), closed: make(chan struct{})}
	var once sync.Once
	c.closeOnce = func() { once.Do(func() { close(c.closed) }) }
	return c
}

func (m *msgChannel) Read() ([]byte, error) {
	select {
	case b, ok := <-m.incoming:
		if !ok {
			return nil, io.EOF
		}
		return b, nil
	case <-m.closed:
		return nil, io.EOF
	}
}

func (m *msgChannel) Write(b []byte) error {
	select {
	case m.outgoing <- b:
		return nil
	case <-m.closed:
		return io.EOF
	}
}

func (m *msgChannel) Close() error {
	m.closeOnce()
	return nil
}

// msgPipe wires two msgChannel ends back to back, client to server: what the client
// writes is read by the server, and the other way round.
type msgPipe struct {
	client *msgChannel
	server *msgChannel
}

func newMsgPipe() *msgPipe {
	c, s := newMsgChannel(), newMsgChannel()
	c.outgoing = s.incoming
	s.outgoing = c.incoming
	return &msgPipe{client: c, server: s}
}

func (p *msgPipe) call(t *testing.T, id int64, method string, params any) map[string]any {
	t.Helper()
	data, _ := json.Marshal(map[string]any{"id": id, "method": method, "params": params})
	if err := p.client.Write(data); err != nil {
		t.Fatalf("write %s: %v", method, err)
	}
	for {
		b, err := p.client.Read()
		if err != nil {
			t.Fatalf("read: %v", err)
		}
		var m map[string]any
		_ = json.Unmarshal(b, &m)
		if v, ok := m["id"]; ok && int64(v.(float64)) == id {
			return m
		}
	}
}

// httpChan opens one http:<n> channel of the tunnel, sends one fetch and reads the answer:
// the head, then the body, then the empty message that ends it.
func httpChan(t *testing.T, chans chan tunnel.LabeledConn, n int, req map[string]any) (map[string]any, string) {
	t.Helper()
	p := newMsgPipe()
	chans <- tunnel.LabeledConn{Label: fmt.Sprintf("http:%d", n), Conn: p.server}
	data, _ := json.Marshal(req)
	if err := p.client.Write(data); err != nil {
		t.Fatalf("write the fetch: %v", err)
	}
	var head map[string]any
	var body []byte
	for {
		b, err := p.client.Read()
		if err != nil {
			t.Fatalf("read the answer: %v", err)
		}
		if head == nil {
			_ = json.Unmarshal(b, &head)
			continue
		}
		if len(b) == 0 {
			return head, string(body)
		}
		body = append(body, b...)
	}
}

// A window of the tunnel runs under the path the browser used (/pod/<key> on the relay origin):
// the index.html it gets carries its assets under it.
func TestTunnelHTTPBase(t *testing.T) {
	s, _ := newServer(t)
	p := newMsgPipe()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	chans := make(chan tunnel.LabeledConn, 4)
	go s.ServeTunnel(ctx, p.server, chans)

	head, body := httpChan(t, chans, 1, map[string]any{"path": "/project/x", "base": "/pod/abc"})
	if head["status"] != float64(200) {
		t.Fatalf("head = %v", head)
	}
	if !strings.Contains(body, `src="/pod/abc/assets/app.js"`) {
		t.Fatalf("page of a pod on the relay origin: %s", body)
	}
	// A base that is not a path of a pod is ignored.
	_, body = httpChan(t, chans, 2, map[string]any{"path": "/project/x", "base": "/elsewhere"})
	if !strings.Contains(body, `src="/assets/app.js"`) {
		t.Fatalf("page with a base out of the pod paths: %s", body)
	}
}

// fakeLAN plays the LAN link of the pod: it records what the gateway was asked to serve.
type fakeLAN struct {
	calls []string
}

func (f *fakeLAN) View() any                                        { return nil }
func (f *fakeLAN) SetManual([]string) error                         { return nil }
func (f *fakeLAN) ServeIdentity(http.ResponseWriter, *http.Request) {}
func (f *fakeLAN) ServeGateway(w http.ResponseWriter, r *http.Request, base string) {
	f.calls = append(f.calls, r.URL.Path+" base="+base)
	_, _ = w.Write([]byte("peer page"))
}

// A window of the tunnel opens a pod of the gateway's LAN too: /_lan/pod/ goes to the gateway,
// with the path the browser used.
func TestTunnelHTTPGateway(t *testing.T) {
	s, _ := newServer(t)
	l := &fakeLAN{}
	s.LAN = l
	p := newMsgPipe()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	chans := make(chan tunnel.LabeledConn, 4)
	go s.ServeTunnel(ctx, p.server, chans)

	head, body := httpChan(t, chans, 1, map[string]any{"path": "/_lan/pod/xyz/project/x", "base": "/pod/xyz"})
	if head["status"] != float64(200) || body != "peer page" {
		t.Fatalf("answer = %v %q", head, body)
	}
	if len(l.calls) != 1 || l.calls[0] != "/_lan/pod/xyz/project/x base=/pod/xyz" {
		t.Fatalf("gateway calls = %v", l.calls)
	}
}

func TestServeTunnel(t *testing.T) {
	s, _ := newServer(t)
	p := newMsgPipe()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go s.ServeTunnel(ctx, p.server, make(chan tunnel.LabeledConn))

	res := p.call(t, 1, "workspace.get", nil)
	if res["error"] != nil {
		t.Fatalf("workspace.get over the tunnel: %v", res["error"])
	}
	ws := res["result"].(map[string]any)
	if _, ok := ws["workspace"]; !ok {
		t.Fatalf("workspace.get result = %v", ws)
	}
}
