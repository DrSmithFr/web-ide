package relayclient

// Pod to pod WebRTC (milestone 3b): a pod plays the browser against another pod of the account,
// through the relay's signaling, and becomes the gateway of its own origin for the pods of
// another cluster (127.0.0.1 has no relay page of its own). The handshake is the same v2 as a
// browser's, bound to the two DTLS fingerprints of this tunnel: a relay that swaps the offer
// changes one, so a signature fails.

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/pion/webrtc/v4"

	"github.com/DrSmithFr/web-ide/lib/identity"
	"github.com/DrSmithFr/web-ide/lib/tunnel"
)

// Peer is one pod-to-pod tunnel. Its rpc channel already passed the handshake; Open adds a
// channel of the same window (http:<n>, term:<id>).
type Peer struct {
	pc      *webrtc.PeerConnection
	sig     *websocket.Conn
	session string
	done    chan struct{}
	rpc     tunnel.MsgConn // the channel the handshake ran on: the window's rpc channel
	mu      sync.Mutex
	closed  bool
}

// RPC is the rpc channel of the window: the one the handshake ran on. The other channels of
// the same window are opened with Open.
func (p *Peer) RPC() tunnel.MsgConn { return p.rpc }

// dialNonce is the session id of a pod-to-pod tunnel: the relay routes every signaling message
// by session, and one pod serves several windows at once.
func dialNonce() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return fmt.Sprintf("%x", b)
}

// Dial opens a tunnel to the pod podID, whose device key must be key. The pod's own certificate
// is shown in the browser role; via, when given, is the certificate of the browser this pod
// vouches for (the end pod checks it against the list it holds).
func (c *Client) Dial(ctx context.Context, podID, key string, via *identity.Cert) (*Peer, error) {
	if c.cfg.Identity == nil || !c.cfg.Identity.Paired() {
		return nil, errors.New("relay: this pod is not paired")
	}
	account, own, _ := c.cfg.Identity.State()
	if own == nil {
		return nil, errors.New("relay: this pod has no certificate")
	}
	session := dialNonce()
	base := c.cfg.RelayURL
	if c.cfg.SignalURL != "" {
		base = c.cfg.SignalURL
	}
	u := strings.TrimSuffix(base, "/") + "/_relay/signal?pod=" + url.QueryEscape(podID) + "&session=" + session
	sig, _, err := websocket.Dial(ctx, u, nil)
	if err != nil {
		return nil, err
	}
	p := &Peer{sig: sig, session: session, done: make(chan struct{})}
	defer func() {
		if p.pc == nil {
			_ = sig.CloseNow()
		}
	}()

	// The relay answers with the ICE servers of this session (TURN credentials included).
	ice, err := readSignal(ctx, sig)
	if err != nil {
		return nil, err
	}
	if ice.Type != "ice" {
		return nil, errors.New("relay: no ice for the tunnel")
	}
	policy := webrtc.ICETransportPolicyAll
	if c.cfg.ForceTurn {
		policy = webrtc.ICETransportPolicyRelay
	}
	pc, err := webrtc.NewPeerConnection(webrtc.Configuration{
		ICEServers:         []webrtc.ICEServer{iceServer(ice)},
		ICETransportPolicy: policy,
	})
	if err != nil {
		return nil, err
	}
	p.pc = pc
	pc.OnICEConnectionStateChange(func(s webrtc.ICEConnectionState) {
		if s == webrtc.ICEConnectionStateFailed || s == webrtc.ICEConnectionStateClosed {
			p.Close()
		}
	})
	// The rpc channel is created before the offer: an offer with no application section carries
	// no DataChannel multiplexer, and the peer could not answer it.
	rpc, err := pc.CreateDataChannel("rpc", nil)
	if err != nil {
		return nil, err
	}
	opened := make(chan struct{})
	rpc.OnOpen(func() { close(opened) })
	offer, err := pc.CreateOffer(nil)
	if err != nil {
		return nil, err
	}
	// The offer leaves with the candidates gathered within earlyWait, the later ones are trickled
	// after it (#75): it used to wait for the whole gathering, up to 8 s.
	tr := trickle(pc, func(c json.RawMessage) {
		_ = sig.Write(context.Background(), websocket.MessageText, []byte(mustSignal(sigMsg{Type: "candidate", Session: session, Candidate: c})))
	})
	gather := webrtc.GatheringCompletePromise(pc)
	if err := pc.SetLocalDescription(offer); err != nil {
		return nil, err
	}
	desc, _ := tr.describe(pc, gather)
	if err := sig.Write(ctx, websocket.MessageText, []byte(mustSignal(sigMsg{Type: "offer", Session: session, SDP: desc.SDP}))); err != nil {
		return nil, err
	}
	tr.sent()

	// The pod answers with the candidates it gathered quickly and trickles the others, before and
	// after its answer: the ones that come first wait for the answer, the later ones are read
	// until the link closes.
	var early []webrtc.ICECandidateInit
	for pc.RemoteDescription() == nil {
		m, err := readSignal(ctx, sig)
		if err != nil {
			return nil, err
		}
		switch m.Type {
		case "answer":
			if err := pc.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeAnswer, SDP: m.SDP}); err != nil {
				return nil, err
			}
			for _, c := range early {
				_ = pc.AddICECandidate(c)
			}
		case "candidate":
			if init, ok := candidateInit(m.Candidate); ok {
				early = append(early, init)
			}
		case "bye":
			return nil, errors.New("the pod closed the tunnel")
		case "error":
			return nil, errors.New("the pod refused the tunnel: " + m.Message)
		}
	}
	go p.follow(ice.TTL)

	select {
	case <-opened:
	case <-p.closedDone():
		return nil, errors.New("the tunnel closed")
	case <-time.After(20 * time.Second):
		return nil, errors.New("the rpc channel did not open")
	}
	conn := tunnel.NewFramed(newDataChannelChunk(rpc), 8*1024*1024)
	p.rpc = conn
	fpB := tunnel.Fingerprint(pc.LocalDescription().SDP)
	fpP := tunnel.Fingerprint(pc.RemoteDescription().SDP)
	peer, err := tunnel.RunBrowserCertVia(conn, c.cfg.Identity.Key(), own, account, fpB, fpP, dialNonce(), via)
	if err != nil {
		conn.Close()
		p.Close()
		return nil, err
	}
	if peer == nil || peer.Key != key {
		conn.Close()
		p.Close()
		return nil, fmt.Errorf("the pod at %s is %s", key, certKey(peer))
	}
	if peer.Kind != identity.KindPod {
		conn.Close()
		p.Close()
		return nil, errors.New("the peer is not a pod")
	}
	if err := c.cfg.Identity.PeerError(peer); err != nil {
		conn.Close()
		p.Close()
		return nil, err
	}
	_ = account
	return p, nil
}

// follow reads the signaling of the tunnel once the pod answered: its late candidates, and the
// answer to a renewal. Before the TURN credentials end, it asks fresh ones and restarts ICE with
// them (#86): the tunnel and its channels stay.
func (p *Peer) follow(ttl int64) {
	msgs := make(chan sigMsg)
	go func() {
		defer close(msgs)
		for {
			m, err := readSignal(context.Background(), p.sig)
			if err != nil {
				return
			}
			select {
			case msgs <- m:
			case <-p.done:
				return
			}
		}
	}()
	var renew <-chan time.Time
	schedule := func(ttl int64) {
		if d, ok := renewAfter(ttl); ok {
			renew = time.After(d)
		}
	}
	schedule(ttl)
	for {
		select {
		case <-p.done:
			return
		case <-renew:
			renew = nil
			_ = p.sig.Write(context.Background(), websocket.MessageText, []byte(mustSignal(sigMsg{Type: "ice", Session: p.session})))
		case m, ok := <-msgs:
			if !ok {
				return
			}
			switch m.Type {
			case "candidate":
				if init, ok := candidateInit(m.Candidate); ok {
					_ = p.pc.AddICECandidate(init)
				}
			case "ice":
				if err := p.restart(m); err != nil {
					log.Printf("relayclient: tunnel %s: ICE restart: %v", p.session[:6], err)
				}
				schedule(m.TTL)
			case "answer":
				_ = p.pc.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeAnswer, SDP: m.SDP})
			case "bye":
				go p.Close()
				return
			}
		}
	}
}

// restart offers again with the fresh credentials of an "ice" message, restarting ICE. The trickle
// starts before the offer: the restart gathers at once.
func (p *Peer) restart(ice sigMsg) error {
	cfg := p.pc.GetConfiguration()
	cfg.ICEServers = []webrtc.ICEServer{iceServer(ice)}
	if err := p.pc.SetConfiguration(cfg); err != nil {
		return err
	}
	tr := trickle(p.pc, func(c json.RawMessage) {
		_ = p.sig.Write(context.Background(), websocket.MessageText, []byte(mustSignal(sigMsg{Type: "candidate", Session: p.session, Candidate: c})))
	})
	offer, err := p.pc.CreateOffer(&webrtc.OfferOptions{ICERestart: true})
	if err != nil {
		return err
	}
	gather := webrtc.GatheringCompletePromise(p.pc)
	if err := p.pc.SetLocalDescription(offer); err != nil {
		return err
	}
	desc, _ := tr.describe(p.pc, gather)
	if err := p.sig.Write(context.Background(), websocket.MessageText, []byte(mustSignal(sigMsg{Type: "offer", Session: p.session, SDP: desc.SDP}))); err != nil {
		return err
	}
	tr.sent()
	return nil
}

// Open adds one channel of this window and waits for it.
func (p *Peer) Open(ctx context.Context, label string) (tunnel.MsgConn, error) {
	dc, err := p.pc.CreateDataChannel(label, nil)
	if err != nil {
		return nil, err
	}
	opened := make(chan struct{})
	dc.OnOpen(func() { close(opened) })
	select {
	case <-opened:
	case <-p.closedDone():
		return nil, tunnel.ErrClosed
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return tunnel.NewFramed(newDataChannelChunk(dc), 8*1024*1024), nil
}

// closedDone is the channel closed when the tunnel ends (the peer closed, ICE failed, or Close).
func (p *Peer) closedDone() <-chan struct{} { return p.done }

// Close ends the tunnel: the pod is told on the signaling link, and the PeerConnection closed.
func (p *Peer) Close() {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return
	}
	p.closed = true
	sig, pc := p.sig, p.pc
	p.mu.Unlock()
	close(p.done)
	if sig != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		_ = sig.Write(ctx, websocket.MessageText, []byte(mustSignal(sigMsg{Type: "bye", Session: p.session})))
		cancel()
		_ = sig.CloseNow()
	}
	if pc != nil {
		_ = pc.Close()
	}
}

// readSignal reads one signaling message of the link.
func readSignal(ctx context.Context, c *websocket.Conn) (sigMsg, error) {
	var m sigMsg
	_, b, err := c.Read(ctx)
	if err != nil {
		return m, err
	}
	if err := json.Unmarshal(b, &m); err != nil {
		return m, err
	}
	return m, nil
}

func mustSignal(m sigMsg) string {
	b, _ := json.Marshal(m)
	return string(b)
}

func certKey(c *identity.Cert) string {
	if c == nil {
		return "nobody"
	}
	return c.Key
}
