package lan

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"time"

	"github.com/coder/websocket"
)

// GatewayPath answers, on a gateway port, which pod serves it: the page of a peer shows a
// link back to the home page of the gateway. The pod itself answers {} there.
const GatewayPath = "/_lan/gateway"

// gateway is the local port of one peer: a reverse proxy to its LAN port. The page of the
// peer is served at the root of its own origin, so its paths need no prefix, and the
// browser sends it the token cookie of the pod (a cookie belongs to a host, not a port).
type gateway struct {
	port int
	srv  *http.Server
}

func (g *gateway) close() { _ = g.srv.Close() }

// startGateway opens the gateway port of a peer, the same port as in the last run when it
// is free.
func (m *Manager) startGateway(key string) {
	m.mu.Lock()
	p := m.peers[key]
	if p == nil || p.gw != nil {
		m.mu.Unlock()
		return
	}
	port := m.ports[key]
	m.mu.Unlock()

	// The host of the pod address: loopback, or every interface when the pod is open to
	// the network (the token cookie still guards the port).
	host := m.cfg.GatewayHost
	ln, err := net.Listen("tcp", net.JoinHostPort(host, strconv.Itoa(port)))
	if err != nil && port != 0 {
		ln, err = net.Listen("tcp", net.JoinHostPort(host, "0"))
	}
	if err != nil {
		log.Printf("lan: gateway of %s: %v", key, err)
		return
	}
	g := &gateway{port: ln.Addr().(*net.TCPAddr).Port}
	g.srv = &http.Server{Handler: m.gatewayHandler(key), ReadHeaderTimeout: 10 * time.Second}
	m.mu.Lock()
	if p.gw != nil {
		m.mu.Unlock()
		_ = ln.Close()
		return
	}
	p.gw = g
	if m.ports[key] != g.port {
		m.ports[key] = g.port
		m.savePorts()
	}
	m.mu.Unlock()
	go func() { _ = g.srv.Serve(ln) }()
}

// gatewayHandler checks the request as the pod checks its own page, then forwards it to the
// peer over the link we dialed, with our TLS certificate, which the peer trusts.
func (m *Manager) gatewayHandler(key string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if m.cfg.Allow != nil && !m.cfg.Allow(r) {
			http.Error(w, "open the IDE on its own address first: this port serves another pod through it", http.StatusUnauthorized)
			return
		}
		if r.URL.Path == GatewayPath {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"name": m.cfg.Name, "port": m.cfg.HomePort})
			return
		}
		m.mu.Lock()
		var addr, fp string
		if p := m.peers[key]; p != nil && p.out != nil {
			addr, fp = p.out.addr, p.out.serverFP
		}
		m.mu.Unlock()
		if addr == "" {
			http.Error(w, "this pod is offline", http.StatusBadGateway)
			return
		}
		target := &url.URL{Scheme: "https", Host: addr}
		rp := &httputil.ReverseProxy{
			Rewrite: func(pr *httputil.ProxyRequest) {
				pr.SetURL(target)
				// The peer checks the WebSocket origin against the host the browser used.
				pr.Out.Host = pr.In.Host
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
	})
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
