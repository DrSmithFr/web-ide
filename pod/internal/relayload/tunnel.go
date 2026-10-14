package relayload

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/pion/webrtc/v4"

	"github.com/DrSmithFr/web-ide/pod/internal/tunnel"
)

// The tunnel tier is the one that costs the relay: each session is a real WebRTC connection
// carried by the embedded TURN server, with the tunnel handshake on a DataChannel and a payload
// pumped through it. One allocation takes one relay port, so this is where the port range runs
// out.
//
// Both ends run here, on two PeerConnections joined by the relay's signaling: the relay only
// carries the signaling, so what is measured is the relay and its TURN server, not a pod. The
// handshake is the token mode: the load pods are unpaired.

// loadToken is the token both ends of a load tunnel prove.
const loadToken = "relayload-token"

// iceConfig is the ICE configuration of one end, from the ice message the relay sent.
func iceConfig(m msg, forceTurn bool) webrtc.Configuration {
	policy := webrtc.ICETransportPolicyAll
	if forceTurn {
		policy = webrtc.ICETransportPolicyRelay
	}
	return webrtc.Configuration{
		ICEServers:         []webrtc.ICEServer{{URLs: m.URLs, Username: m.Username, Credential: m.Credential}},
		ICETransportPolicy: policy,
	}
}

// chunk adapts a DataChannel to a tunnel.Chunk (the same adapter the pod and the relayclient use).
type chunk struct {
	dc   *webrtc.DataChannel
	in   chan []byte
	done chan struct{}
	once sync.Once
}

func newChunk(dc *webrtc.DataChannel) *chunk {
	c := &chunk{dc: dc, in: make(chan []byte, 512), done: make(chan struct{})}
	dc.OnMessage(func(m webrtc.DataChannelMessage) {
		select {
		case c.in <- m.Data:
		default: // a slow reader: drop rather than grow
		}
	})
	dc.OnClose(func() { c.once.Do(func() { close(c.done) }) })
	return c
}

func (c *chunk) ReadMsg() ([]byte, error) {
	select {
	case b := <-c.in:
		return b, nil
	case <-c.done:
		return nil, io.EOF
	}
}

func (c *chunk) WriteMsg(b []byte) error { return c.dc.Send(b) }
func (c *chunk) Buffered() int           { return int(c.dc.BufferedAmount()) }
func (c *chunk) Close() error {
	c.once.Do(func() { close(c.done) })
	return c.dc.Close()
}

// trickle sends every candidate the agent gathers on the link given, the way a browser does: an
// offer sent before gathering is complete carries none, and the relay routes them as they come.
// Waiting for the gathering to finish instead costs the slowest candidate of both ends.
func trickle(pc *webrtc.PeerConnection, send func(*webrtc.ICECandidate)) {
	pc.OnICECandidate(func(c *webrtc.ICECandidate) {
		if c != nil {
			send(c)
		}
	})
}

// candidateMsg is a candidate as the relay carries it.
func candidateMsg(c *webrtc.ICECandidate) json.RawMessage {
	b, _ := json.Marshal(c.ToJSON())
	return b
}

// runTunnel is the tunnel tier: sessions brought up through TURN, held, and pumped.
func runTunnel(ctx context.Context, c *Config, rep *Report, mu *sync.Mutex) error {
	pods, err := registerPods(ctx, c, rep)
	if err != nil {
		return err
	}
	defer func() {
		for _, p := range pods {
			p.close()
		}
	}()

	// The pod end of each pod link: it answers the offers its sessions get and runs the pod side
	// of the handshake on the channel that opens.
	for _, p := range pods {
		go podSide(p)
	}

	var wg sync.WaitGroup
	step := time.Duration(0)
	if c.Sessions > 0 {
		step = c.Ramp / time.Duration(c.Sessions)
	}
	end := time.Now().Add(c.Ramp + c.Hold)
	for i := 0; i < c.Sessions; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if step > 0 {
				select {
				case <-ctx.Done():
					return
				case <-time.After(time.Duration(i) * step):
				}
			}
			n, setup, err := browserSide(ctx, c, pods[i%len(pods)].id, end)
			if err != nil {
				mu.Lock()
				rep.note(err)
				mu.Unlock()
				return
			}
			mu.Lock()
			rep.OK++
			rep.Bytes += n
			rep.Setup = append(rep.Setup, setup)
			mu.Unlock()
		}(i)
	}
	wg.Wait()
	return nil
}

// browserSide is the browser end of one load tunnel: offer, answer, channel open, handshake,
// payload. It returns the bytes it pushed.
func browserSide(ctx context.Context, c *Config, podID string, end time.Time) (int64, time.Duration, error) {
	session := sessionID()
	at := time.Now()
	b, ice, err := dialBrowser(ctx, c, podID, session)
	if err != nil {
		return 0, 0, err
	}
	defer b.close()

	pc, err := webrtc.NewPeerConnection(iceConfig(ice, c.ForceTurn))
	if err != nil {
		return 0, 0, err
	}
	defer pc.Close()

	rpc, err := pc.CreateDataChannel("rpc", nil)
	if err != nil {
		return 0, 0, err
	}
	opened := make(chan struct{})
	rpc.OnOpen(func() { close(opened) })

	offer, err := pc.CreateOffer(nil)
	if err != nil {
		return 0, 0, err
	}
	trickle(pc, func(c *webrtc.ICECandidate) {
		_ = b.send(msg{Type: "candidate", Session: session, Candidate: candidateMsg(c)})
	})
	if err := pc.SetLocalDescription(offer); err != nil {
		return 0, 0, err
	}
	if err := b.send(msg{Type: "offer", Session: session, SDP: pc.LocalDescription().SDP}); err != nil {
		return 0, 0, err
	}

	// The signaling keeps being read while the channel comes up: a candidate from the pod may
	// arrive before its answer does, and pion only accepts them once the remote description is set.
	var buffered []webrtc.ICECandidateInit
	var remoteSet bool
	deadline, cancel := context.WithTimeout(ctx, c.Timeout)
	defer cancel()
	go func() {
		for {
			m, err := b.read(deadline)
			if err != nil {
				return
			}
			switch m.Type {
			case "answer":
				if err := pc.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeAnswer, SDP: m.SDP}); err != nil {
					return
				}
				remoteSet = true
				for _, init := range buffered {
					_ = pc.AddICECandidate(init)
				}
				buffered = nil
			case "candidate":
				var init webrtc.ICECandidateInit
				if json.Unmarshal(m.Candidate, &init) != nil {
					continue
				}
				if remoteSet {
					_ = pc.AddICECandidate(init)
				} else {
					buffered = append(buffered, init)
				}
			case "bye":
				return
			}
		}
	}()

	select {
	case <-opened:
	case <-time.After(c.Timeout):
		return 0, 0, fmt.Errorf("session %s: the channel did not open", session)
	}

	conn := tunnel.NewFramed(newChunk(rpc), 8*1024*1024)
	fpB := tunnel.Fingerprint(pc.LocalDescription().SDP)
	fpP := tunnel.Fingerprint(pc.RemoteDescription().SDP)
	nonce := randHex()
	if err := tunnel.RunBrowser(conn, loadToken, fpB, fpP, nonce); err != nil {
		conn.Close()
		return 0, 0, fmt.Errorf("session %s: %w", session, err)
	}
	defer conn.Close()
	setup := time.Since(at)

	// The payload: chunks of 16 KB, back-pressure left to the framing.
	total := int64(c.MB) << 20
	payload := make([]byte, 16*1024)
	var sent int64
	for sent < total {
		n := int64(len(payload))
		if total-sent < n {
			n = total - sent
		}
		if err := conn.Write(payload[:n]); err != nil {
			return sent, setup, fmt.Errorf("session %s: %w", session, err)
		}
		sent += n
	}
	// The tunnel stays up until the end of the hold window: the relay is measured carrying it.
	select {
	case <-ctx.Done():
	case <-time.After(time.Until(end)):
	}
	return sent, setup, nil
}

// podSide is the pod end: it answers the offers its link carries and proves the token on the
// channel that opens, then reads what the browser pushes (the bytes leave the relay through the
// TURN allocation, so reading them is what keeps the tunnel honest).
//
// The relay sends the ice of a session just before the offer that starts it: the pod end needs
// those credentials too, since its own address is a private one the TURN server cannot reach.
// podEnd is one pod link seen from the pod side: the ICE servers the relay told it per session,
// and the PeerConnection answering each session.
type podEnd struct {
	p   *podLink
	mu  sync.Mutex
	ice map[string]msg
	pcs map[string]*webrtc.PeerConnection
	// A candidate may arrive before the answerer of its session exists (the browser gathers as it
	// is offered), or before its offer is set: pion only accepts a candidate once the remote
	// description is there, so they wait here until then.
	cands map[string][]webrtc.ICECandidateInit
	ready map[string]bool
}

func podSide(p *podLink) {
	e := &podEnd{p: p, ice: map[string]msg{}, pcs: map[string]*webrtc.PeerConnection{},
		cands: map[string][]webrtc.ICECandidateInit{}, ready: map[string]bool{}}
	ctx := context.Background()
	for {
		m, err := p.read(ctx)
		if err != nil {
			return
		}
		switch m.Type {
		case "ice":
			e.mu.Lock()
			e.ice[m.Session] = m
			e.mu.Unlock()
		case "offer":
			e.mu.Lock()
			srv := e.ice[m.Session]
			delete(e.ice, m.Session)
			e.mu.Unlock()
			go e.answer(m.Session, m.SDP, srv)
		case "candidate":
			var init webrtc.ICECandidateInit
			if json.Unmarshal(m.Candidate, &init) != nil {
				continue
			}
			e.mu.Lock()
			pc, ok := e.pcs[m.Session], e.ready[m.Session]
			if pc == nil || !ok {
				e.cands[m.Session] = append(e.cands[m.Session], init)
			}
			e.mu.Unlock()
			if pc != nil && ok {
				_ = pc.AddICECandidate(init)
			}
		case "bye":
			e.mu.Lock()
			pc := e.pcs[m.Session]
			delete(e.pcs, m.Session)
			delete(e.ready, m.Session)
			delete(e.cands, m.Session)
			e.mu.Unlock()
			if pc != nil {
				_ = pc.Close()
			}
		}
	}
}

// answer runs one session of a load pod: the answer, then the handshake, then the read.
func (e *podEnd) answer(session, offerSDP string, srv msg) {
	pc, err := webrtc.NewPeerConnection(iceConfig(srv, true))
	if err != nil {
		return
	}
	defer pc.Close()
	e.mu.Lock()
	e.pcs[session] = pc
	e.mu.Unlock()
	defer func() {
		e.mu.Lock()
		delete(e.pcs, session)
		delete(e.ready, session)
		delete(e.cands, session)
		e.mu.Unlock()
	}()

	trickle(pc, func(c *webrtc.ICECandidate) {
		_ = e.p.send(msg{Type: "candidate", Session: session, Candidate: candidateMsg(c)})
	})
	rpcCh := make(chan *webrtc.DataChannel, 1)
	pc.OnDataChannel(func(dc *webrtc.DataChannel) {
		if dc.Label() == "rpc" {
			select {
			case rpcCh <- dc:
			default:
			}
		}
	})
	if err := pc.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeOffer, SDP: offerSDP}); err != nil {
		return
	}
	// The offer is in: the candidates that arrived while the answerer did not exist can be added.
	e.mu.Lock()
	e.ready[session] = true
	buffered := e.cands[session]
	delete(e.cands, session)
	e.mu.Unlock()
	for _, init := range buffered {
		_ = pc.AddICECandidate(init)
	}
	answer, err := pc.CreateAnswer(nil)
	if err != nil {
		return
	}
	if err := pc.SetLocalDescription(answer); err != nil {
		return
	}
	if err := e.p.send(msg{Type: "answer", Session: session, SDP: pc.LocalDescription().SDP}); err != nil {
		return
	}

	var rpc *webrtc.DataChannel
	select {
	case rpc = <-rpcCh:
	case <-time.After(30 * time.Second):
		return
	}
	opened := make(chan struct{})
	rpc.OnOpen(func() { close(opened) })
	select {
	case <-opened:
	case <-time.After(30 * time.Second):
		return
	}

	conn := tunnel.NewFramed(newChunk(rpc), 8*1024*1024)
	defer conn.Close()
	hs := &tunnel.Handshake{
		Token:     loadToken,
		FPBrowser: tunnel.Fingerprint(offerSDP),
		FPPod:     tunnel.Fingerprint(pc.LocalDescription().SDP),
	}
	if err := hs.Run(conn); err != nil {
		return
	}
	for {
		if _, err := conn.Read(); err != nil {
			return
		}
	}
}

// randHex is a nonce, in the form the tunnel handshake uses.
func randHex() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}
