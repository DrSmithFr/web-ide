package lan

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"time"

	"github.com/coder/websocket"
)

// GatewayPath answers, under the path of a peer, which pod serves it: the page of a peer
// shows a link back to the home page of the gateway. The pod itself answers {} there.
const GatewayPath = "/_lan/gateway"

// PeerPrefix is the start of the path of a peer on its gateway pod: /_lan/pod/<device key>.
// The page of a peer runs under it, on the origin of the gateway: one origin for the
// installed app, and the browser sends the token cookie of the gateway, which checks it.
const PeerPrefix = "/_lan/pod/"

// BaseHeader tells a peer the path its page runs under (server.ServeTrusted).
const BaseHeader = "X-Webide-Base"

// ServeGateway forwards a request under the path of a peer to that peer, over the link we
// dialed, with our TLS certificate, which the peer trusts. The server checked the token.
// base is the path the peer's page runs under for the browser: the gateway origin by default,
// the relay origin (/pod/<key>) when the request came through the tunnel of a browser there.
func (m *Manager) ServeGateway(w http.ResponseWriter, r *http.Request, base string) {
	key, rest, _ := strings.Cut(strings.TrimPrefix(r.URL.Path, PeerPrefix), "/")
	if base == "" {
		base = PeerPrefix + key
	}
	if rest == "" && !strings.HasSuffix(r.URL.Path, "/") {
		http.Redirect(w, r, base+"/", http.StatusFound)
		return
	}
	if "/"+rest == GatewayPath {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"name": m.cfg.Name})
		return
	}
	m.mu.Lock()
	var addr, fp string
	if p := m.peers[key]; p != nil && p.out != nil {
		addr, fp = p.out.addr, p.out.serverFP
	}
	m.mu.Unlock()
	if addr == "" {
		// Not a pod of the local network: this pod may still reach it, pod to pod through the
		// relay (pod/internal/multipod.RemoteGateway).
		if m.cfg.Remote != nil {
			m.cfg.Remote.ServeHTTP(w, r)
			return
		}
		http.Error(w, "this pod is offline", http.StatusBadGateway)
		return
	}
	target := &url.URL{Scheme: "https", Host: addr}
	rp := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(target)
			pr.Out.URL.Path = "/" + rest
			pr.Out.URL.RawPath = ""
			// The peer checks the WebSocket origin against the host the browser used.
			pr.Out.Host = pr.In.Host
			pr.Out.Header.Set(BaseHeader, base)
			// The token of this pod is not the peer's business: the peer trusts our
			// certificate instead.
			pr.Out.Header.Del("Cookie")
		},
		Transport:     m.transport(fp),
		FlushInterval: -1,
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			http.Error(w, "this pod does not answer", http.StatusBadGateway)
		},
	}
	rp.ServeHTTP(w, r)
}

// transport reaches a peer with our TLS certificate, and accepts only the certificate the
// handshake bound: a machine that took the peer's address meanwhile is refused.
func (m *Manager) transport(fp string) *http.Transport {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.transports == nil {
		m.transports = map[string]*http.Transport{}
	}
	if t := m.transports[fp]; t != nil {
		return t
	}
	t := &http.Transport{
		TLSClientConfig: &tls.Config{
			Certificates:       []tls.Certificate{m.cert},
			InsecureSkipVerify: true, // pinned below
			MinVersion:         tls.VersionTLS13,
			VerifyConnection: func(cs tls.ConnectionState) error {
				if leafFingerprint(&cs) != fp {
					return errors.New("lan: the pod changed its certificate")
				}
				return nil
			},
		},
		MaxIdleConnsPerHost: 8,
		IdleConnTimeout:     90 * time.Second,
	}
	m.transports[fp] = t
	return t
}

// watchProjects keeps the project list of a peer for the home page: a WebSocket of the
// protocol through the link, projects.list again on each projects.changed.
func (m *Manager) watchProjects(l *link) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		<-l.done
		cancel()
	}()
	for ctx.Err() == nil {
		if err := m.readProjects(ctx, l); err != nil && ctx.Err() == nil {
			select {
			case <-ctx.Done():
			case <-time.After(3 * time.Second):
			}
		}
	}
}

func (m *Manager) readProjects(ctx context.Context, l *link) error {
	c, _, err := websocket.Dial(ctx, "wss://"+l.addr+"/ws", &websocket.DialOptions{HTTPClient: &http.Client{Transport: m.transport(l.serverFP)}})
	if err != nil {
		return err
	}
	defer c.CloseNow()
	c.SetReadLimit(16 << 20)
	id := 0
	ask := func() error {
		id++
		b, _ := json.Marshal(map[string]any{"id": id, "method": "projects.list", "params": map[string]any{}})
		return c.Write(ctx, websocket.MessageText, b)
	}
	if err := ask(); err != nil {
		return err
	}
	for {
		_, b, err := c.Read(ctx)
		if err != nil {
			return err
		}
		var msg struct {
			ID     int             `json:"id"`
			Result json.RawMessage `json:"result"`
			Event  string          `json:"event"`
		}
		if json.Unmarshal(b, &msg) != nil {
			continue
		}
		switch {
		case msg.Event == "projects.changed":
			if err := ask(); err != nil {
				return err
			}
		case msg.ID == id && msg.Result != nil:
			m.mu.Lock()
			if p := m.peers[l.peer.Key]; p != nil {
				p.projects = msg.Result
			}
			m.mu.Unlock()
			m.changed()
		}
	}
}
