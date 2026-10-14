package relayload

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/coder/websocket"
)

// msg is one signaling message, as the relay and its clients exchange it (relay.sigMsg on the
// other side). Only the fields this tool uses are here.
type msg struct {
	Type     string          `json:"type"`
	Session  string            `json:"session,omitempty"`
	URLs     []string          `json:"urls,omitempty"`
	Username string            `json:"username,omitempty"`
	Credential string          `json:"credential,omitempty"`
	SDP      string            `json:"sdp,omitempty"`
	Candidate json.RawMessage `json:"candidate,omitempty"`
	ID       string            `json:"id,omitempty"`
	Secret   string            `json:"secret,omitempty"`
	Name     string            `json:"name,omitempty"`
	Version  string            `json:"version,omitempty"`
	Protocol int               `json:"protocol,omitempty"`
	Peers    []string          `json:"peers,omitempty"`
	Message  string            `json:"message,omitempty"`
}

// link is one WebSocket link of the relay, with its writes serialized (a pod link carries the
// signaling of every session it serves at once).
type link struct {
	ws *websocket.Conn
	mu sync.Mutex
}

func (l *link) send(m msg) error {
	b, _ := json.Marshal(m)
	l.mu.Lock()
	defer l.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return l.ws.Write(ctx, websocket.MessageText, b)
}

func (l *link) read(ctx context.Context) (msg, error) {
	var m msg
	_, b, err := l.ws.Read(ctx)
	if err != nil {
		return m, err
	}
	if err := json.Unmarshal(b, &m); err != nil {
		return m, err
	}
	return m, nil
}

func (l *link) close() { _ = l.ws.CloseNow() }

// dialPod opens a pod link and registers: the relay marks the pod online, which is what lets a
// browser open a session of it (and what mints its TURN credentials).
func dialPod(ctx context.Context, base, id, secret, name string) (*link, error) {
	ws, _, err := websocket.Dial(ctx, wsURL(base)+"/_relay/pod", nil)
	if err != nil {
		return nil, fmt.Errorf("dialing the pod link: %w", err)
	}
	l := &link{ws: ws}
	if err := l.send(msg{Type: "register", ID: id, Secret: secret, Name: name, Version: "load", Protocol: 1}); err != nil {
		l.close()
		return nil, err
	}
	m, err := l.read(ctx)
	if err != nil {
		l.close()
		return nil, err
	}
	if m.Type != "ready" {
		l.close()
		return nil, fmt.Errorf("the pod link answered %q", m.Type)
	}
	return l, nil
}

// dialBrowser opens the signaling link of one session and waits for the ice message (the ICE
// servers of the session, TURN credentials included).
func dialBrowser(ctx context.Context, base, podID, session string) (*link, msg, error) {
	ws, _, err := websocket.Dial(ctx, wsURL(base)+"/_relay/signal?pod="+podID+"&session="+session, nil)
	if err != nil {
		return nil, msg{}, fmt.Errorf("dialing the browser link: %w", err)
	}
	l := &link{ws: ws}
	m, err := l.read(ctx)
	if err != nil {
		l.close()
		return nil, msg{}, err
	}
	if m.Type != "ice" {
		l.close()
		return nil, msg{}, fmt.Errorf("the browser link answered %q, not ice", m.Type)
	}
	return l, m, nil
}

// wsURL turns the http(s) base of the relay into its ws(s) form.
func wsURL(base string) string {
	if len(base) > 5 && base[:5] == "http:" {
		return "ws:" + base[5:]
	}
	if len(base) > 6 && base[:6] == "https:" {
		return "wss:" + base[6:]
	}
	return base
}

// sessionID is the id the relay routes a session's signaling by.
func sessionID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return fmt.Sprintf("%x", b)
}

// runSignal is the middle tier: the links and the signaling, without any WebRTC. A fake SDP goes
// offer → pod → answer → browser, and the pods report LAN peers so the relay recomputes its
// sites on every event (the one place its work grows with the number of pods).
func runSignal(ctx context.Context, c *Config, rep *Report, mu *sync.Mutex) error {
	pods, err := registerPods(ctx, c, rep)
	if err != nil {
		return err
	}
	defer func() {
		for _, p := range pods {
			p.close()
		}
	}()

	// Each pod answers the offer of its sessions with a fake SDP, and keeps reporting peers.
	for _, p := range pods {
		go answerer(p)
		go peerReporter(ctx, p, pods)
	}

	var wg sync.WaitGroup
	step := time.Duration(0)
	if c.Sessions > 0 {
		step = c.Ramp / time.Duration(c.Sessions)
	}
	// A session stays open until the end of the hold window: the relay is measured carrying them,
	// not carrying the ramp.
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
			pod := pods[i%len(pods)]
			session := sessionID()
			b, ice, err := dialBrowser(ctx, c.RelayURL, pod.id, session)
			if err != nil {
				mu.Lock()
				rep.note(err)
				mu.Unlock()
				return
			}
			defer b.close()
			_ = ice
			if err := b.send(msg{Type: "offer", Session: session, SDP: "v=0\r\no=- 1 1 IN IP4 127.0.0.1\r\ns=-\r\n"}); err != nil {
				mu.Lock()
				rep.note(err)
				mu.Unlock()
				return
			}
			deadline, cancel := context.WithTimeout(ctx, c.Timeout)
			defer cancel()
			for {
				m, err := b.read(deadline)
				if err != nil {
					mu.Lock()
					rep.note(fmt.Errorf("session %s: %w", session, err))
					mu.Unlock()
					return
				}
				if m.Type == "answer" {
					mu.Lock()
					rep.OK++
					rep.Setup = append(rep.Setup, time.Since(at))
					mu.Unlock()
					break
				}
			}
			select {
			case <-ctx.Done():
			case <-time.After(time.Until(end)):
			}
		}(i)
	}
	wg.Wait()
	return nil
}

// answerer is the pod side of the signaling tier: it answers an offer with a fake SDP and sends a
// candidate back, the way a pod does.
func answerer(p *podLink) {
	ctx := context.Background()
	for {
		m, err := p.read(ctx)
		if err != nil {
			return
		}
		switch m.Type {
		case "offer":
			_ = p.send(msg{Type: "answer", Session: m.Session, SDP: "v=0\r\no=- 2 2 IN IP4 127.0.0.1\r\ns=-\r\n"})
			cand, _ := json.Marshal(map[string]string{"candidate": "candidate:1 1 UDP 2122252543 127.0.0.1 50000 typ host", "sdpMid": "0", "sdpMLineIndex": "0"})
			_ = p.send(msg{Type: "candidate", Session: m.Session, Candidate: cand})
		case "bye":
		}
	}
}

// peerReporter keeps telling the relay which pods this pod sees on its LAN: each message makes it
// recompute the sites of the account and push them (sites.go).
func peerReporter(ctx context.Context, p *podLink, pods []*podLink) {
	t := time.NewTicker(2 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			var peers []string
			for _, o := range pods {
				if o.id != p.id {
					peers = append(peers, o.id)
				}
			}
			_ = p.send(msg{Type: "site", Peers: peers})
		}
	}
}

// podLink is a pod link with the id it registered under.
type podLink struct {
	*link
	id string
}

// registerPods brings the pod links up: the load of the relay starts here.
func registerPods(ctx context.Context, c *Config, rep *Report) ([]*podLink, error) {
	var pods []*podLink
	for i := 0; i < c.Pods; i++ {
		id := fmt.Sprintf("load-pod-%d", i)
		p, err := dialPod(ctx, c.RelayURL, id, "secret-"+id, id)
		if err != nil {
			for _, q := range pods {
				q.close()
			}
			return nil, err
		}
		pods = append(pods, &podLink{link: p, id: id})
	}
	rep.Pods = len(pods)
	return pods, nil
}
