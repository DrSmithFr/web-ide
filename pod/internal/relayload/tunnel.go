package relayload

import (
	"context"
	"crypto/rand"
	"encoding/hex"
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

// gather waits for the ICE candidates to be gathered, so the SDP sent to the relay carries them
// and no trickling has to be routed.
func gather(pc *webrtc.PeerConnection) {
	done := webrtc.GatheringCompletePromise(pc)
	select {
	case <-done:
	case <-time.After(10 * time.Second):
	}
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
			at := time.Now()
			n, err := browserSide(ctx, c, pods[i%len(pods)].id, end)
			if err != nil {
				mu.Lock()
				rep.note(err)
				mu.Unlock()
				return
			}
			mu.Lock()
			rep.OK++
			rep.Bytes += n
			rep.Setup = append(rep.Setup, time.Since(at))
			mu.Unlock()
		}(i)
	}
	wg.Wait()
	return nil
}

// browserSide is the browser end of one load tunnel: offer, answer, channel open, handshake,
// payload. It returns the bytes it pushed.
func browserSide(ctx context.Context, c *Config, podID string, end time.Time) (int64, error) {
	session := sessionID()
	b, ice, err := dialBrowser(ctx, c.RelayURL, podID, session)
	if err != nil {
		return 0, err
	}
	defer b.close()

	pc, err := webrtc.NewPeerConnection(iceConfig(ice, c.ForceTurn))
	if err != nil {
		return 0, err
	}
	defer pc.Close()

	rpc, err := pc.CreateDataChannel("rpc", nil)
	if err != nil {
		return 0, err
	}
	opened := make(chan struct{})
	rpc.OnOpen(func() { close(opened) })

	offer, err := pc.CreateOffer(nil)
	if err != nil {
		return 0, err
	}
	if err := pc.SetLocalDescription(offer); err != nil {
		return 0, err
	}
	gather(pc)
	if err := b.send(msg{Type: "offer", Session: session, SDP: pc.LocalDescription().SDP}); err != nil {
		return 0, err
	}

	deadline, cancel := context.WithTimeout(ctx, c.Timeout)
	defer cancel()
	for {
		m, err := b.read(deadline)
		if err != nil {
			return 0, fmt.Errorf("session %s: %w", session, err)
		}
		if m.Type == "answer" {
			if err := pc.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeAnswer, SDP: m.SDP}); err != nil {
				return 0, err
			}
			break
		}
		if m.Type == "bye" {
			return 0, fmt.Errorf("session %s: the pod closed", session)
		}
	}

	select {
	case <-opened:
	case <-time.After(c.Timeout):
		return 0, fmt.Errorf("session %s: the channel did not open", session)
	}

	conn := tunnel.NewFramed(newChunk(rpc), 8*1024*1024)
	fpB := tunnel.Fingerprint(pc.LocalDescription().SDP)
	fpP := tunnel.Fingerprint(pc.RemoteDescription().SDP)
	nonce := randHex()
	if err := tunnel.RunBrowser(conn, loadToken, fpB, fpP, nonce); err != nil {
		conn.Close()
		return 0, fmt.Errorf("session %s: %w", session, err)
	}
	defer conn.Close()

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
			return sent, fmt.Errorf("session %s: %w", session, err)
		}
		sent += n
	}
	// The tunnel stays up until the end of the hold window: the relay is measured carrying it.
	select {
	case <-ctx.Done():
	case <-time.After(time.Until(end)):
	}
	return sent, nil
}

// podSide is the pod end: it answers the offers its link carries and proves the token on the
// channel that opens, then reads what the browser pushes (the bytes leave the relay through the
// TURN allocation, so reading them is what keeps the tunnel honest).
func podSide(p *podLink) {
	ctx := context.Background()
	for {
		m, err := p.read(ctx)
		if err != nil {
			return
		}
		if m.Type == "offer" {
			go podAnswer(p, m.Session, m.SDP)
		}
	}
}

// podAnswer runs one session of a load pod: the answer, then the handshake, then the read.
func podAnswer(p *podLink, session, offerSDP string) {
	pc, err := webrtc.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		return
	}
	defer pc.Close()

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
	answer, err := pc.CreateAnswer(nil)
	if err != nil {
		return
	}
	if err := pc.SetLocalDescription(answer); err != nil {
		return
	}
	gather(pc)
	if err := p.send(msg{Type: "answer", Session: session, SDP: pc.LocalDescription().SDP}); err != nil {
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
