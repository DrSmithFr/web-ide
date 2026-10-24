package server

import (
	"context"
	"errors"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/DrSmithFr/web-ide/lib/identity"
	"github.com/DrSmithFr/web-ide/lib/tunnel"
)

// The routes the pods of the account reach on this pod (Host.HandlePeer: the shared services),
// under /_peer/<plugin>/ (lan.PodsOnly). Who asks is read from the transport only: the
// certificate of a pod proved on the LAN link (lan.Manager) or in the handshake of a pod-to-pod
// WebRTC tunnel (relayclient). A window of this pod's own origin, a browser, a token, a request
// a gateway forwards: refused.

const podsOnly = "/_peer/"

// servePeer serves a request under podsOnly to the plugin that handles it.
func (s *Server) servePeer(w http.ResponseWriter, r *http.Request) {
	peer := identity.PeerOf(r.Context())
	if peer == nil || peer.Kind != identity.KindPod {
		http.Error(w, "for the pods of the account only", http.StatusForbidden)
		return
	}
	id, _, _ := strings.Cut(strings.TrimPrefix(r.URL.Path, podsOnly), "/")
	h := s.plugins.peers[id]
	if h == nil {
		http.NotFound(w, r)
		return
	}
	http.StripPrefix(podsOnly+id, h).ServeHTTP(w, r)
}

// serveConnChannel runs HTTP/1.1 on a conn:<n> channel of a pod-to-pod tunnel: a request whose
// connection outlives its answer (a WebSocket) on the routes of the pods, and only them.
func (c *Client) serveConnChannel(ctx context.Context, conn tunnel.MsgConn) {
	nc := newMsgNetConn(conn)
	srv := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !strings.HasPrefix(r.URL.Path, podsOnly) {
				http.NotFound(w, r)
				return
			}
			c.srv.servePeer(w, r)
		}),
		BaseContext:       func(net.Listener) context.Context { return ctx },
		ReadHeaderTimeout: 30 * time.Second,
	}
	_ = srv.Serve(&oneConnListener{conn: nc})
	_ = conn.Close()
}

// msgNetConn is a byte stream over a whole-message channel: each Write one message.
type msgNetConn struct {
	conn tunnel.MsgConn
	buf  []byte
	once sync.Once
}

func newMsgNetConn(conn tunnel.MsgConn) *msgNetConn { return &msgNetConn{conn: conn} }

func (m *msgNetConn) Read(b []byte) (int, error) {
	for len(m.buf) == 0 {
		msg, err := m.conn.Read()
		if err != nil {
			return 0, err
		}
		m.buf = msg
	}
	n := copy(b, m.buf)
	m.buf = m.buf[n:]
	return n, nil
}

func (m *msgNetConn) Write(b []byte) (int, error) {
	n := len(b)
	for len(b) > 0 {
		c := min(len(b), 64*1024)
		chunk := make([]byte, c)
		copy(chunk, b[:c])
		if err := m.conn.Write(chunk); err != nil {
			return 0, err
		}
		b = b[c:]
	}
	return n, nil
}

func (m *msgNetConn) Close() error {
	var err error
	m.once.Do(func() { err = m.conn.Close() })
	return err
}

func (m *msgNetConn) LocalAddr() net.Addr                { return tunnelAddr{} }
func (m *msgNetConn) RemoteAddr() net.Addr               { return tunnelAddr{} }
func (m *msgNetConn) SetDeadline(t time.Time) error      { return nil }
func (m *msgNetConn) SetReadDeadline(t time.Time) error  { return nil }
func (m *msgNetConn) SetWriteDeadline(t time.Time) error { return nil }

type tunnelAddr struct{}

func (tunnelAddr) Network() string { return "tunnel" }
func (tunnelAddr) String() string  { return "tunnel" }

// oneConnListener hands one connection to http.Server, then reports it closed.
type oneConnListener struct {
	mu   sync.Mutex
	conn net.Conn
	done chan struct{}
}

func (l *oneConnListener) Accept() (net.Conn, error) {
	l.mu.Lock()
	if l.done == nil {
		l.done = make(chan struct{})
	}
	c := l.conn
	l.conn = nil
	done := l.done
	l.mu.Unlock()
	if c != nil {
		return &closeNotify{Conn: c, done: done}, nil
	}
	<-done
	return nil, errors.New("tunnel: channel closed")
}

func (l *oneConnListener) Close() error   { return nil }
func (l *oneConnListener) Addr() net.Addr { return tunnelAddr{} }

// closeNotify ends the listener when the server closes its only connection.
type closeNotify struct {
	net.Conn
	done chan struct{}
	once sync.Once
}

func (c *closeNotify) Close() error {
	c.once.Do(func() { close(c.done) })
	return c.Conn.Close()
}
