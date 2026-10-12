package server

import (
	"context"
	"encoding/json"
	"io"
	"sync"
	"testing"

	"github.com/DrSmithFr/web-ide/pod/internal/tunnel"
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
