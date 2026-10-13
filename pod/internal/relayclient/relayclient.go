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
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/pion/webrtc/v4"

	"github.com/DrSmithFr/web-ide/pod/internal/identity"
	"github.com/DrSmithFr/web-ide/pod/internal/server"
	"github.com/DrSmithFr/web-ide/pod/internal/settingsync"
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
	// Sync (milestone 4): the pod publishes its X25519 key, reads the wrap of the epoch key made
	// for it, and exchanges encrypted blobs. The relay cannot read a blob, it only checks the
	// signature against the device that wrote it.
	SyncKey *identity.SyncKeyRecord `json:"syncKey,omitempty"`
	Wrap    *identity.WrapRecord    `json:"wrap,omitempty"`
	Blob    *identity.BlobRecord    `json:"blob,omitempty"`
	Device  string                  `json:"device,omitempty"`
	Kind    string                  `json:"kind,omitempty"`
	Updated int64                   `json:"updated,omitempty"`
	Message string                  `json:"message,omitempty"`
	// Sites (milestone 3): register announces that this pod answers the proof of possession and
	// carries its version and the preferred switch, `site` the device keys of its LAN peers,
	// `challenge`/`proof` the nonce, `pods`/`gateway` what the relay sees of the account.
	Prove     bool      `json:"prove,omitempty"`
	Nonce     string    `json:"nonce,omitempty"`
	Sig       string    `json:"sig,omitempty"`
	Version   string    `json:"version,omitempty"`
	Preferred bool      `json:"preferred,omitempty"`
	Peers     []string  `json:"peers,omitempty"`
	Site      string    `json:"site,omitempty"`
	Gateway   string    `json:"gateway,omitempty"`
	Pods      []PodInfo `json:"pods,omitempty"`
}

// PodInfo is one pod of the account as the relay sees it (mirrors relay.PodInfo).
type PodInfo struct {
	Key     string `json:"key"`
	PodID   string `json:"podId"`
	Name    string `json:"name"`
	Version string `json:"version"`
	Site    string `json:"site"`
	Gateway bool   `json:"gateway"`
	Since   int64  `json:"since"`
}

// toSync turns the sync part of a signaling message into the message the sync loop reads.
func toSync(m sigMsg) settingsync.Msg {
	return settingsync.Msg{Type: m.Type, Account: m.Account, Device: m.Device, Kind: m.Kind,
		SyncKey: m.SyncKey, Wrap: m.Wrap, Blob: m.Blob, Updated: m.Updated, Message: m.Message}
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
	// Sync is the settings sync loop of the pod: it sends its messages on this link and receives
	// the answers and the pushes of the relay (nil: the pod does not sync).
	Sync   *settingsync.Hub
	Limits *tunnel.RateLimiter
	// Version is the pod version, told to the relay (it lists the pods of the account).
	Version string
	// Preferred marks this pod as the entry point of its site (Settings > Remote access).
	Preferred bool
	// OnPods is called with the pods of the account the relay sees, OnGateway with the gateway of
	// a site (multipod.Directory).
	OnPods    func([]PodInfo)
	OnGateway func(site, key string)
	// OnForeignWrap is called with a wrap the relay pushed for another device: this pod passes it
	// on over its LAN links, which is how a pod with no link of its own gets its key back.
	OnForeignWrap func(*identity.WrapRecord)
	// ForceTurn only advertises relay (TURN) candidates (e2e).
	ForceTurn bool
	// OnState is called with the state of the link to the relay (connecting, connected,
	// disconnected); the page shows it in Settings > Remote access.
	OnState func(state string)
	// OnPushGone is called with a push subscription the relay dropped (the push service said
	// 410, or the subscription is not of this account).
	OnPushGone func(sub string)
}

// Client is one pod's relay client: one outbound link to the relay and one
// PeerConnection per browser session.
type Client struct {
	cfg      Config
	limits   *tunnel.RateLimiter
	mu       sync.Mutex
	sessions map[string]*session
	// link is the link to the relay while it is up (PushRevocations).
	link   *podLink
	iceMu  sync.Mutex
	iceFor map[string]webrtc.ICEServer
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

	// Register: the relay stores the secret and marks the pod online. A paired pod also gives its
	// account and its certificate — the relay can check them but never make one — and answers the
	// nonce it is sent with the device key: the proof of possession. Without it the relay knows
	// nothing about this pod: no presence, no gateway role.
	var reg sigMsg
	if c.cfg.Identity != nil && c.cfg.Identity.Paired() {
		account, cert, _ := c.cfg.Identity.State()
		reg = sigMsg{Type: "register", ID: c.cfg.PodID, Secret: c.cfg.Secret, Name: c.cfg.Name,
			Account: account, Cert: cert, Prove: true, Version: c.cfg.Version, Preferred: c.cfg.Preferred}
	} else {
		reg = sigMsg{Type: "register", ID: c.cfg.PodID, Secret: c.cfg.Secret, Name: c.cfg.Name, Version: c.cfg.Version}
	}
	if err := link.send(reg); err != nil {
		return err
	}
	m, err := link.recv(ctx)
	if err != nil {
		return err
	}
	if m.Type == "challenge" {
		sig := identity.Sign(c.cfg.Identity.Key(), []byte(identity.RelayBinding(relayHost(c.cfg.RelayURL), m.Nonce)))
		if err := link.send(sigMsg{Type: "proof", Nonce: m.Nonce, Sig: sig}); err != nil {
			return err
		}
		m, err = link.recv(ctx)
		if err != nil {
			return err
		}
	}
	if m.Type != "ready" {
		return errors.New("relay refused the pod: " + m.Type)
	}
	// The revocation list the relay holds: the pod checks it itself and refuses the devices
	// on it, whatever the relay says.
	if err := c.applyRevocations(m.List); err != nil {
		return err
	}
	// The other way: a newer list the pod holds goes to the relay, so that the next list an
	// admin signs from the relay's copy keeps its revocations.
	if c.cfg.Identity != nil {
		if _, _, own := c.cfg.Identity.State(); own != nil && (m.List == nil || own.Seq > m.List.Seq) {
			_ = link.send(sigMsg{Type: "revocations", List: own})
		}
	}
	c.mu.Lock()
	c.link = link
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		c.link = nil
		c.mu.Unlock()
	}()
	c.tell("connected")
	// The sync loop publishes the X25519 key of this device, reads the wrap an admin made for it
	// and the state the relay holds: a pod that comes back online catches up here.
	if c.cfg.Sync != nil {
		c.cfg.Sync.SetTransport(&syncLink{c, link})
		if err := c.cfg.Sync.Publish(); err != nil {
			log.Printf("relayclient: sync: %v", err)
			c.cfg.Sync.SetError(err)
		}
	}
	for {
		if ctx.Err() != nil {
			// A clean goodbye: the relay moves the gateway role at once instead of waiting for
			// the grace, and this pod starts fresh next time it comes up.
			_ = link.send(sigMsg{Type: "bye"})
			return ctx.Err()
		}
		m, err := link.recv(ctx)
		if err != nil {
			return err
		}
		switch m.Type {
		case "pods":
			if c.cfg.OnPods != nil {
				c.cfg.OnPods(m.Pods)
			}
		case "gateway":
			if c.cfg.OnGateway != nil {
				c.cfg.OnGateway(m.Site, m.Gateway)
			}
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
		case "wrap", "blob":
			if m.Type == "wrap" && m.Wrap != nil && c.cfg.Identity != nil && m.Wrap.Device != c.cfg.Identity.ID() {
				// Not for this pod: it is for a pod of its site that has no link of its own.
				if c.cfg.OnForeignWrap != nil {
					c.cfg.OnForeignWrap(m.Wrap)
				}
				continue
			}
			if c.cfg.Sync == nil {
				continue
			}
			if err := c.cfg.Sync.Handle(toSync(m)); err != nil {
				log.Printf("relayclient: sync: %v", err)
				c.cfg.Sync.SetError(err)
			}
		case "push.gone":
			if c.cfg.OnPushGone != nil {
				c.cfg.OnPushGone(m.ID)
			}
		}
	}
}

// syncLink is the transport the sync loop sends its messages on: the same link, so a write and its
// answer stay in order.
type syncLink struct {
	c    *Client
	link *podLink
}

func (l *syncLink) Send(m settingsync.Msg) error {
	return l.link.send(sigMsg{Type: m.Type, Account: m.Account, Device: m.Device, Kind: m.Kind,
		SyncKey: m.SyncKey, Wrap: m.Wrap, Blob: m.Blob, Updated: m.Updated, Message: m.Message})
}

// Push gives the relay a Web Push message for a subscription of the account: the payload is
// already encrypted for the browser (package push), the relay signs and forwards it.
func (c *Client) Push(sub string, body []byte, ttl int, urgency, topic string) error {
	c.mu.Lock()
	link := c.link
	c.mu.Unlock()
	if link == nil {
		return errors.New("relay: not connected")
	}
	return link.sendAny(map[string]any{"type": "push.send", "sub": sub, "body": body, "ttl": ttl, "urgency": urgency, "topic": topic})
}

// PushRevocations gives the relay a newer revocation list the pod received another way (a
// pod of the LAN, the pair command). The relay keeps it only if it is newer than its own.
func (c *Client) PushRevocations(list *identity.Revocations) {
	c.mu.Lock()
	link := c.link
	c.mu.Unlock()
	if link != nil && list != nil {
		_ = link.send(sigMsg{Type: "revocations", List: list})
	}
}

// ReportSite tells the relay the device keys of the pods this pod sees on its LAN: the relay
// builds the sites from what every pod reports, and elects one gateway per site.
func (c *Client) ReportSite(peers []string) {
	c.mu.Lock()
	link := c.link
	c.mu.Unlock()
	if link == nil {
		return
	}
	_ = link.send(sigMsg{Type: "site", Peers: peers})
}

// PutBlob stores a blob a peer of the site handed over: a pod with no link to the relay publishes
// its project list through this pod. The record is forwarded unchanged — the relay checks the
// signature of the device that wrote it, so the hop adds no trust.
func (c *Client) PutBlob(rec *identity.BlobRecord) error {
	c.mu.Lock()
	link := c.link
	c.mu.Unlock()
	if link == nil {
		return errors.New("relay: not connected")
	}
	return link.send(sigMsg{Type: "blobPut", Account: rec.Account, Device: rec.Device, Kind: rec.Kind, Blob: rec})
}

// PutSyncKey hands the X25519 key a peer of the site published to the relay, so an admin can wrap
// the key of the epoch for a pod that has no link of its own. The record is forwarded unchanged.
func (c *Client) PutSyncKey(rec *identity.SyncKeyRecord) error {
	c.mu.Lock()
	link := c.link
	c.mu.Unlock()
	if link == nil {
		return errors.New("relay: not connected")
	}
	return link.send(sigMsg{Type: "syncKey", Account: rec.Account, Device: rec.Device, SyncKey: rec})
}

// SetPreferred marks this pod as the entry point of its site (Settings > Remote access), and tells
// the relay about the change.
func (c *Client) SetPreferred(on bool) {
	c.mu.Lock()
	c.cfg.Preferred = on
	link := c.link
	c.mu.Unlock()
	if link != nil {
		_ = link.send(sigMsg{Type: "preferred", Preferred: on})
	}
}

// relayHost is the origin the pod dials: the string a proof of possession is bound to, the same
// one the relay read in the Host header of the link.
func relayHost(u string) string {
	p, err := url.Parse(u)
	if err != nil || p.Host == "" {
		return u
	}
	return p.Host
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

func (l *podLink) send(m sigMsg) error { return l.sendAny(m) }

func (l *podLink) sendAny(m any) error {
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
