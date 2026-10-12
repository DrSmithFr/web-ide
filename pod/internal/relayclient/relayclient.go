// Package relayclient is the pod's side of the relay: it keeps one outbound WebSocket
// link to the relay and, for each browser that opens it, a WebRTC PeerConnection that
// carries the DataChannels. The pod token is proved inside the tunnel (bound to both
// DTLS fingerprints); it never reaches the relay.
//
// Channels by label: "rpc" carries the JSON protocol (unchanged); "term:<id>",
// "xfer:<n>" and "http:<n>" carry terminal I/O, big transfers and HTTP requests, one
// channel each, so a 2 MB file read does not stall keystrokes. The pod creates the
// "rpc" channel and answers offers; the browser creates the rest, and the pod routes
// them.
package relayclient

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/pion/webrtc/v4"

	"github.com/DrSmithFr/web-ide/pod/internal/identity"
	"github.com/DrSmithFr/web-ide/pod/internal/server"
	"github.com/DrSmithFr/web-ide/pod/internal/tunnel"
)

// sigMsg is one signaling message between the relay and the pod (mirrored by the relay).
type sigMsg struct {
	Type       string                `json:"type"`
	Session    string                `json:"session,omitempty"`
	URLs       []string              `json:"urls,omitempty"`
	Username   string                `json:"username,omitempty"`
	Credential string                `json:"credential,omitempty"`
	SDP        string                `json:"sdp,omitempty"`
	Candidate  json.RawMessage       `json:"candidate,omitempty"`
	ID         string                `json:"id,omitempty"`
	Secret     string                `json:"secret,omitempty"`
	Name       string                `json:"name,omitempty"`
	Account    string                `json:"account,omitempty"`
	Cert       *identity.Cert        `json:"cert,omitempty"`
	List       *identity.Revocations `json:"list,omitempty"` // pushed to the pods of an account
}

// Config of a relayclient.
type Config struct {
	RelayURL string // ws:// or wss:// URL of the relay (the base, without /_relay/pod)
	PodID    string // the pod id registered with the relay
	Secret   string // the pod secret (register, or re-register)
	Name     string // a human name for the relay UI (optional)
	Server   *server.Server
	Token    string                // the pod token, proved inside the tunnel (token mode)
	Identity *identity.PodIdentity // the paired pod: its device key and its pinned account
	Limits   *tunnel.RateLimiter
	// ForceTurn only advertises relay (TURN) candidates (e2e).
	ForceTurn bool
	// OnState is called with the state of the link to the relay (connecting, connected,
	// disconnected); the page shows it in Settings > Remote access.
	OnState func(state string)
}

// Client is one pod's relay client: one outbound link to the relay and one
// PeerConnection per browser session.
type Client struct {
	cfg      Config
	limits   *tunnel.RateLimiter
	mu       sync.Mutex
	sessions map[string]*session
	iceMu    sync.Mutex
	iceFor   map[string]webrtc.ICEServer
}

// New builds a relayclient Client.
func New(cfg Config) *Client {
	if cfg.Limits == nil {
		cfg.Limits = tunnel.NewRateLimiter(5, 5*time.Minute)
	}
	return &Client{cfg: cfg, limits: cfg.Limits, sessions: map[string]*session{}, iceFor: map[string]webrtc.ICEServer{}}
}

// Run dials the relay, registers, and routes signaling until ctx ends. It reconnects
// with backoff if the link drops.
func (c *Client) Run(ctx context.Context) error {
	backoff := time.Second
	for {
		c.tell("connecting")
		if err := c.runOnce(ctx); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			c.tell("disconnected")
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(backoff):
				if backoff < 30*time.Second {
					backoff *= 2
				}
			}
		} else {
			backoff = time.Second
		}
	}
}

func (c *Client) tell(state string) {
	if c.cfg.OnState != nil {
		c.cfg.OnState(state)
	}
}

// runOnce is one live link to the relay: register, then route signaling.
func (c *Client) runOnce(ctx context.Context) error {
	u := strings.TrimSuffix(c.cfg.RelayURL, "/") + "/_relay/pod"
	conn, _, err := websocket.Dial(ctx, u, nil)
	if err != nil {
		return err
	}
	link := &podLink{c: conn}
	defer conn.Close(websocket.StatusNormalClosure, "")

	// Register: the relay stores the secret and marks the pod online. A paired pod also
	// gives its account and its certificate — the relay can check them but never make one.
	var reg sigMsg
	if c.cfg.Identity != nil && c.cfg.Identity.Paired() {
		reg = sigMsg{Type: "register", ID: c.cfg.PodID, Secret: c.cfg.Secret, Name: c.cfg.Name,
			Account: c.cfg.Identity.Account, Cert: c.cfg.Identity.Cert}
	} else {
		reg = sigMsg{Type: "register", ID: c.cfg.PodID, Secret: c.cfg.Secret, Name: c.cfg.Name}
	}
	if err := link.send(reg); err != nil {
		return err
	}
	m, err := link.recv(ctx)
	if err != nil {
		return err
	}
	if m.Type != "ready" {
		return errors.New("relay refused the pod: " + m.Type)
	}
	// The revocation list the relay holds: the pod checks it itself and refuses the devices
	// on it, whatever the relay says.
	if err := c.applyRevocations(m.List); err != nil {
		return err
	}
	c.tell("connected")
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		m, err := link.recv(ctx)
		if err != nil {
			return err
		}
		switch m.Type {
		case "revocations":
			if err := c.applyRevocations(m.List); err != nil {
				return err
			}
		case "ice":
			c.setICE(m.Session, m)
		case "offer":
			go c.handleOffer(m, link)
		case "candidate":
			c.route(m.Session, func(s *session) { s.addBrowserCandidate(m.Candidate) })
		case "bye":
			c.route(m.Session, func(s *session) { s.close() })
		}
	}
}

// applyRevocations takes a revocation list from the relay. The pod checks the signature
// against the account it pinned — it never trusts the relay to say who is revoked — and
// keeps only a list with a higher seq.
func (c *Client) applyRevocations(list *identity.Revocations) error {
	if c.cfg.Identity == nil || list == nil {
		return nil
	}
	return c.cfg.Identity.ApplyRevocations(list)
}

// route runs fn on the session of an id, if it is alive.
func (c *Client) route(id string, fn func(*session)) {
	if s := c.session(id); s != nil {
		fn(s)
	}
}

func (c *Client) setICE(sessionID string, m sigMsg) {
	srv := webrtc.ICEServer{URLs: m.URLs, Username: m.Username, Credential: m.Credential}
	c.iceMu.Lock()
	c.iceFor[sessionID] = srv
	c.iceMu.Unlock()
}

func (c *Client) ICE(sessionID string) webrtc.ICEServer {
	c.iceMu.Lock()
	defer c.iceMu.Unlock()
	return c.iceFor[sessionID]
}

func (c *Client) session(id string) *session {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.sessions[id]
}

func (c *Client) addSession(id string, s *session) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.sessions[id] = s
}

func (c *Client) dropSession(id string) {
	c.mu.Lock()
	delete(c.sessions, id)
	c.mu.Unlock()
}

// handleOffer sets up one PeerConnection for a browser session, answers the offer, and
// serves the tunnel. It runs in its own goroutine (per session), so one handshake does
// not block the signaling loop.
func (c *Client) handleOffer(m sigMsg, link *podLink) {
	s := &session{client: c, link: link, id: m.Session, rpcCh: make(chan *dataChannelChunk, 1), chans: make(chan tunnel.LabeledConn, 64)}
	s.ctx, s.cancel = context.WithCancel(context.Background())
	c.addSession(m.Session, s)
	if err := s.setupOffer(m); err != nil {
		link.send(sigMsg{Type: "error", Session: m.Session})
		s.close()
		return
	}
	// serveRPC waits for the browser's "rpc" DataChannel (OnDataChannel hands it to
	// s.rpcCh, registered synchronously) and runs the handshake and the tunnel.
	go s.serveRPC()
}

// podLink is one pod's persistent WebSocket to the relay; writes are serialized.
type podLink struct {
	c  *websocket.Conn
	mu sync.Mutex
}

func (l *podLink) send(m sigMsg) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	b, _ := json.Marshal(m)
	return l.c.Write(context.Background(), websocket.MessageText, b)
}

func (l *podLink) recv(ctx context.Context) (sigMsg, error) {
	var m sigMsg
	_, b, err := l.c.Read(ctx)
	if err != nil {
		return m, err
	}
	_ = json.Unmarshal(b, &m)
	return m, nil
}

// session is one browser's PeerConnection and tunnel, in its own goroutine.
type session struct {
	client *Client
	link   *podLink
	id     string
	ctx    context.Context
	cancel context.CancelFunc
	mu     sync.Mutex
	pc     *webrtc.PeerConnection
	answer webrtc.SessionDescription
	offer  webrtc.SessionDescription
	// rpcCh carries the "rpc" DataChannel the browser created: OnDataChannel hands it
	// here (its OnMessage is registered synchronously, before any data can arrive), and
	// serveRPC — started once the answer is set, so its fingerprint is known — takes it.
	rpcCh chan *dataChannelChunk
	// chans carries every other DataChannel of this session (term:<id>, xfer:<n>,
	// http:<n>), framed, to ServeTunnel.
	chans chan tunnel.LabeledConn
	// rpcConn is the framed rpc channel, so the pod can answer on it (terminal output).
	rpcConn *tunnel.Framed
	closed  bool
}

func (s *session) setupOffer(m sigMsg) error {
	s.offer = webrtc.SessionDescription{Type: webrtc.SDPTypeOffer, SDP: m.SDP}
	policy := webrtc.ICETransportPolicyAll
	if s.client.cfg.ForceTurn {
		policy = webrtc.ICETransportPolicyRelay
	}
	cfg := webrtc.Configuration{ICEServers: []webrtc.ICEServer{s.client.ICE(s.id)}, ICETransportPolicy: policy}
	pc, err := webrtc.NewPeerConnection(cfg)
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.pc = pc
	s.mu.Unlock()
	pc.OnDataChannel(func(dc *webrtc.DataChannel) {
		// Register the incoming-message handler synchronously (before any data can
		// arrive). "rpc" goes to serveRPC; the rest are handed to the pod as labeled
		// channels. The handler runs in its own goroutine: this callback must return
		// before Pion starts the channel's read loop, and the pod reads the channel at its
		// own pace.
		chunk := newDataChannelChunk(dc)
		if dc.Label() == "rpc" {
			select {
			case s.rpcCh <- chunk:
			default:
			}
			return
		}
		conn := tunnel.NewFramed(chunk, 8*1024*1024)
		s.chans <- tunnel.LabeledConn{Label: dc.Label(), Conn: conn}
	})
	pc.OnICEConnectionStateChange(func(state webrtc.ICEConnectionState) {
		if state == webrtc.ICEConnectionStateFailed {
			s.close()
		}
	})
	if err := pc.SetRemoteDescription(s.offer); err != nil {
		return err
	}
	answer, err := pc.CreateAnswer(nil)
	if err != nil {
		return err
	}
	// Non-trickle: wait for the pod's ICE candidates to be gathered, then send the full
	// answer SDP (with the candidates embedded). The browser trickles its own, or embeds
	// them in the offer; either way the pod has them from SetRemoteDescription.
	gather := webrtc.GatheringCompletePromise(pc)
	if err := pc.SetLocalDescription(answer); err != nil {
		return err
	}
	<-gather
	// The final answer SDP carries the pod's DTLS fingerprint (the handshake binds it);
	// the raw CreateAnswer SDP may not have it yet.
	lc := pc.LocalDescription()
	s.mu.Lock()
	s.answer = *lc
	s.mu.Unlock()
	return s.link.send(sigMsg{Type: "answer", Session: s.id, SDP: lc.SDP})
}

func (s *session) addBrowserCandidate(raw json.RawMessage) {
	var init webrtc.ICECandidateInit
	if err := json.Unmarshal(raw, &init); err != nil {
		return
	}
	s.mu.Lock()
	pc := s.pc
	s.mu.Unlock()
	if pc != nil {
		_ = pc.AddICECandidate(init)
	}
}

// serveRPC runs the handshake on the "rpc" channel (created by the browser), then hands
// the connection to the server. It waits for the DataChannel (OnDataChannel hands it to
// s.rpcCh, registered synchronously) and runs in its own goroutine.
func (s *session) serveRPC() {
	chunk := <-s.rpcCh
	if chunk == nil {
		return
	}
	offer, answer := s.offer, s.answer
	conn := tunnel.NewFramed(chunk, 8*1024*1024)
	hs := &tunnel.Handshake{
		Token:     s.client.cfg.Token,
		Identity:  s.client.cfg.Identity,
		FPBrowser: tunnel.Fingerprint(offer.SDP),
		FPPod:     tunnel.Fingerprint(answer.SDP),
		Limits:    s.client.limits,
	}
	if err := hs.Run(conn); err != nil {
		log.Printf("relayclient: handshake: %v (fpB=%s fpP=%s)", err, hs.FPBrowser, hs.FPPod)
		// The refusal is the message the user reads ("device revoked", "the pod refuses the
		// token mode"). Closing the channel at once drops the frame still queued in SCTP, and
		// the browser only sees the close: give the refusal a moment to reach the peer.
		time.Sleep(300 * time.Millisecond)
		_ = conn.Close()
		s.close()
		return
	}
	s.client.cfg.Server.ServeTunnel(s.ctx, conn, s.chans)
	// ServeTunnel returned: the browser closed the tunnel.
	s.close()
}

// close ends the session (browser bye, ICE failed, or the tunnel closed).
func (s *session) close() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	pc := s.pc
	s.mu.Unlock()
	close(s.rpcCh) // unblock serveRPC if it was still waiting for the "rpc" channel
	s.client.dropSession(s.id)
	s.link.send(sigMsg{Type: "bye", Session: s.id})
	if s.cancel != nil {
		s.cancel()
	}
	if pc != nil {
		_ = pc.Close()
	}
}

// dataChannelChunk adapts a WebRTC DataChannel to a tunnel.Chunk: one Send/Read is one
// transport message (a DataChannel message), bounded by the SCTP buffer. Pion delivers
// incoming messages on OnMessage; they are buffered here for ReadMsg.
type dataChannelChunk struct {
	dc   *webrtc.DataChannel
	in   chan []byte
	done chan struct{}
	once sync.Once
}

func newDataChannelChunk(dc *webrtc.DataChannel) *dataChannelChunk {
	c := &dataChannelChunk{dc: dc, in: make(chan []byte, 512), done: make(chan struct{})}
	dc.OnMessage(func(msg webrtc.DataChannelMessage) {
		select {
		case c.in <- msg.Data:
		default:
			// The reader is slow; drop rather than grow memory.
		}
	})
	dc.OnClose(func() { c.once.Do(func() { close(c.done) }) })
	return c
}

func (c *dataChannelChunk) ReadMsg() ([]byte, error) {
	select {
	case b := <-c.in:
		return b, nil
	case <-c.done:
		return nil, io.EOF
	}
}

func (c *dataChannelChunk) WriteMsg(b []byte) error { return c.dc.Send(b) }

func (c *dataChannelChunk) Buffered() int { return int(c.dc.BufferedAmount()) }

func (c *dataChannelChunk) Close() error {
	c.once.Do(func() { close(c.done) })
	return c.dc.Close()
}
