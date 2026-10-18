package multipod

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"

	"github.com/DrSmithFr/web-ide/pod/internal/lan"
	"github.com/DrSmithFr/web-ide/pod/internal/tunnel"
)

// The local pod is the gateway of its own origin (127.0.0.1, a LAN address): a pod of another
// cluster, which no LAN link reaches, is served through a pod-to-pod WebRTC tunnel dialled at the
// relay (relay.md, *Multi-pod*). Its page runs under /_lan/pod/<key>/ like a peer's, its fetches
// become http:<n> channels and its WebSocket the rpc channel of one window.
//
// The gateway is a device of the account the end pod checked; what it relays is its own
// responsibility, and the traffic crosses it in clear (end-to-end encryption is #57).

// RemotePod is one pod-to-pod tunnel (pod/internal/relayclient.Peer): its rpc channel already
// passed the handshake, Open adds a channel of the same window.
type RemotePod interface {
	RPC() tunnel.MsgConn
	Open(ctx context.Context, label string) (tunnel.MsgConn, error)
	Close()
}

// Dialer reaches the pod of a device key through the relay.
type Dialer interface {
	Dial(ctx context.Context, key string) (RemotePod, error)
}

// remoteIdle is how long the window carrying the fetches of a pod stays open with nothing on it.
const remoteIdle = 2 * time.Minute

// RemoteGateway serves the pods a dialer reaches, under the peer path of this pod's origin.
type RemoteGateway struct {
	dial Dialer
	// gap is what this pod makes of the protocol of a pod, by its device key (Directory.GapOf);
	// nil when nothing is known, which routes everything.
	gap GapOf

	mu    sync.Mutex
	fetch map[string]*fetchWindow // by device key: the window carrying the http channels
	seq   int
}

// GapOf says what this pod makes of the protocol of the pod at key: -1 older, 1 newer, 0 the same.
// Implemented by multipod.Directory.GapOf.
type GapOf func(key string) (name, version string, gap int)

// fetchWindow is the window one pod's fetches run on: its rpc channel stays open, idle, only so
// the pod keeps serving the http channels opened on it.
type fetchWindow struct {
	pod  RemotePod
	last time.Time
	n    int
}

// NewRemoteGateway makes the handler; dial is nil when this pod cannot reach another pod.
func NewRemoteGateway(dial Dialer, gap GapOf) *RemoteGateway {
	if dial == nil {
		return nil
	}
	return &RemoteGateway{dial: dial, gap: gap, fetch: map[string]*fetchWindow{}}
}

// ServeHTTP serves /_lan/pod/<key>/… of a pod this pod reaches over the relay.
func (g *RemoteGateway) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	key, rest, _ := strings.Cut(strings.TrimPrefix(r.URL.Path, lan.PeerPrefix), "/")
	if key == "" {
		http.Error(w, "no pod", http.StatusBadRequest)
		return
	}
	// A pod of another protocol is not routed to, pod to pod or over the LAN: the page of the
	// gateway says what to update instead of the app of a pod it cannot serve.
	if g.gap != nil {
		if name, version, gap := g.gap(key); gap != 0 {
			lan.UpdatePage(w, r, first(name, key), version, gap < 0)
			return
		}
	}
	if rest == "ws" {
		g.serveWS(w, r, key)
		return
	}
	g.serveFetch(w, r, key, rest)
}

// serveFetch answers one fetch of the page on an http:<n> channel of the pod's window: the JSON
// head, then the body as raw chunks, closed by an empty one — what the pod's own tunnel answers.
func (g *RemoteGateway) serveFetch(w http.ResponseWriter, r *http.Request, key, rest string) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()
	win, err := g.window(ctx, key)
	if err != nil {
		log.Printf("multipod: remote gateway %s: %v", key, err)
		http.Error(w, "this pod does not answer", http.StatusBadGateway)
		return
	}
	g.mu.Lock()
	win.n++
	n := win.n
	g.mu.Unlock()
	ch, err := win.pod.Open(ctx, "http:"+strconv.Itoa(n))
	if err != nil {
		g.drop(key)
		http.Error(w, "this pod does not answer", http.StatusBadGateway)
		return
	}
	defer ch.Close()

	var body []byte
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		body, _ = io.ReadAll(r.Body)
	}
	headers := map[string]string{}
	for k, v := range r.Header {
		if k == "Cookie" { // the token of this pod is not the peer's business
			continue
		}
		headers[k] = strings.Join(v, ", ")
	}
	head, _ := json.Marshal(map[string]any{
		"path": "/" + rest, "method": r.Method, "headers": headers, "body": string(body),
		"base": lan.PeerPrefix + key,
	})
	if err := ch.Write(head); err != nil {
		g.drop(key)
		return
	}
	b, err := ch.Read()
	if err != nil {
		g.drop(key)
		http.Error(w, "this pod does not answer", http.StatusBadGateway)
		return
	}
	var ans struct {
		Status  int               `json:"status"`
		Headers map[string]string `json:"headers"`
	}
	if json.Unmarshal(b, &ans) != nil {
		return
	}
	for k, v := range ans.Headers {
		w.Header().Set(k, v)
	}
	if ans.Status != 0 {
		w.WriteHeader(ans.Status)
	}
	for {
		b, err := ch.Read()
		if err != nil || len(b) == 0 {
			return
		}
		_, _ = w.Write(b)
	}
}

// serveWS bridges the WebSocket of a page to the rpc channel of its own window on the pod: the
// JSON protocol unchanged, the terminals and the streams inline as on any WebSocket.
func (g *RemoteGateway) serveWS(w http.ResponseWriter, r *http.Request, key string) {
	c, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	defer c.CloseNow()
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	pod, err := g.dial.Dial(ctx, key)
	if err != nil {
		_ = c.Close(websocket.StatusInternalError, "this pod does not answer")
		return
	}
	defer pod.Close()
	rpc := pod.RPC()
	defer rpc.Close()
	c.SetReadLimit(64 << 20)

	go func() {
		for {
			_, b, err := c.Read(ctx)
			if err != nil {
				return
			}
			if err := rpc.Write(b); err != nil {
				return
			}
		}
	}()
	for {
		b, err := rpc.Read()
		if err != nil {
			return
		}
		if err := c.Write(ctx, websocket.MessageText, b); err != nil {
			return
		}
	}
}

// window is the window one pod's fetches run on, opened on first use and kept idle.
func (g *RemoteGateway) window(ctx context.Context, key string) (*fetchWindow, error) {
	g.mu.Lock()
	if win := g.fetch[key]; win != nil {
		win.last = time.Now()
		g.mu.Unlock()
		return win, nil
	}
	g.mu.Unlock()
	pod, err := g.dial.Dial(ctx, key)
	if err != nil {
		return nil, err
	}
	rpc := pod.RPC()
	// The rpc channel of a window must stay open for the pod to serve its http channels; it
	// carries nothing else here.
	go func() {
		for {
			if _, err := rpc.Read(); err != nil {
				return
			}
		}
	}()
	g.mu.Lock()
	defer g.mu.Unlock()
	if win := g.fetch[key]; win != nil {
		g.mu.Unlock()
		pod.Close()
		return win, nil
	}
	win := &fetchWindow{pod: pod, last: time.Now()}
	g.fetch[key] = win
	go g.idle(key)
	return win, nil
}

// idle closes the window of a pod no page fetches anymore.
func (g *RemoteGateway) idle(key string) {
	for {
		time.Sleep(30 * time.Second)
		g.mu.Lock()
		win := g.fetch[key]
		if win == nil {
			g.mu.Unlock()
			return
		}
		if time.Since(win.last) > remoteIdle {
			delete(g.fetch, key)
			g.mu.Unlock()
			win.pod.Close()
			return
		}
		g.mu.Unlock()
	}
}

func (g *RemoteGateway) drop(key string) {
	g.mu.Lock()
	win := g.fetch[key]
	if win != nil {
		delete(g.fetch, key)
	}
	g.mu.Unlock()
	if win != nil {
		win.pod.Close()
	}
}
