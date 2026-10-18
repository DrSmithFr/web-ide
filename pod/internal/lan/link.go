package lan

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sync"

	"github.com/coder/websocket"

	"github.com/DrSmithFr/web-ide/pod/internal/identity"
	"github.com/DrSmithFr/web-ide/pod/internal/power"
)

// The path of the control link: the handshake, then the exchange of the pod infos and of
// the revocation lists for as long as both pods are up.
const helloPath = "/_lan/hello"

// wsConn adapts a WebSocket to a tunnel.MsgConn: a frame is a whole message.
type wsConn struct {
	c   *websocket.Conn
	ctx context.Context
}

func (w *wsConn) Read() ([]byte, error) {
	_, b, err := w.c.Read(w.ctx)
	return b, err
}

func (w *wsConn) Write(b []byte) error { return w.c.Write(w.ctx, websocket.MessageText, b) }

// Buffered: a WebSocket keeps nothing of its own, a frame is sent as it is written.
func (w *wsConn) Buffered() int { return 0 }

func (w *wsConn) Close() error { return w.c.Close(websocket.StatusNormalClosure, "") }

// link is one control link with a peer, made by either side once the handshake passed. While
// it is up, the requests that carry the TLS certificate of the peer are trusted.
type link struct {
	conn *wsConn
	peer *identity.Cert
	// inbound: the peer dialed us; else we dialed addr, whose TLS certificate is serverFP.
	inbound  bool
	addr     string
	serverFP string
	// remoteHost is the IP the peer came from (inbound): with the port of its $info, an
	// address to dial back.
	remoteHost string

	once sync.Once
	done chan struct{}
	mu   sync.Mutex // serializes the writes
}

func newLink(c *websocket.Conn, peer *identity.Cert) *link {
	c.SetReadLimit(1 << 20)
	return &link{conn: &wsConn{c: c, ctx: context.Background()}, peer: peer, done: make(chan struct{})}
}

// message is one message of the control link after the handshake.
type message struct {
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
}

// info is what a pod tells about itself on a control link.
type info struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	// Protocol is the multi-pod protocol of the pod (multipod.Protocol): a pod that speaks
	// another one is listed as outdated or newer and is never routed to. A pod older than #60
	// tells none: it counts as protocol 0.
	Protocol int `json:"protocol,omitempty"`
	// Port is the LAN port of the pod: a pod that was dialed learns where to dial back.
	Port int `json:"port"`
	// Wake and Power (relay.md, *Power*): the cards of its machine a magic packet wakes, and whether
	// the machine lets the pod power it off. A peer keeps them once it is offline: that is when a
	// wake is needed. An older pod tells neither.
	Wake  []power.Target `json:"wake,omitempty"`
	Power *bool          `json:"power,omitempty"`
}

// updateMsg is the body of $update: a signed update request of a browser of the account, carried
// by the gateway of the cluster to the pod it names. req is the id of this hop, so the answer lands
// on the request that asked for it.
type updateMsg struct {
	Req    string                  `json:"req"`
	Update *identity.UpdateRequest `json:"update"`
}

// powerMsg is the body of $power: a signed power request (shutdown, reboot, restart) for the
// pod on the other side, carried like an update request; its answer is a $power.done.
type powerMsg struct {
	Req   string                 `json:"req"`
	Power *identity.PowerRequest `json:"power"`
}

// updateDone is the answer of the pod the request named, sent before it restarts.
type updateDone struct {
	Req     string `json:"req"`
	Status  string `json:"status"`
	Message string `json:"message,omitempty"`
	Command string `json:"command,omitempty"`
}

func (l *link) send(method string, params any) error {
	b, err := json.Marshal(params)
	if err != nil {
		return err
	}
	data, _ := json.Marshal(message{Method: method, Params: b})
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.conn.Write(data)
}

func (l *link) close() {
	l.once.Do(func() {
		close(l.done)
		_ = l.conn.Close()
	})
}

// closed tells whether the link is down.
func (l *link) closed() bool {
	select {
	case <-l.done:
		return true
	default:
		return false
	}
}

// nonce is a fresh random nonce of the handshake (the browser side, played by the dialer).
func nonce() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// errSelf: the address is this pod (mDNS answers with our own announcement too).
var errSelf = errors.New("lan: this pod")
