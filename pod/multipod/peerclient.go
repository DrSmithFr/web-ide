package multipod

import (
	"context"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"

	"antennae.space/ide/lib/tunnel"
)

// PeerClient reaches the routes of the pods (/_peer/<plugin>/, relay.md *Services*) on another pod
// of the account: http://<device key>/_peer/… The route is chosen per request: the LAN when the
// pod is a peer this pod dialed (TLS pinned, our certificate), else pod to pod through the relay,
// one tunnel per pod whose HTTP/1.1 connections are conn:<n> channels. A tunnel opens at the first
// request and closes once it has had no connection for Idle; the LAN link costs nothing.
type PeerClient struct {
	// LAN gives the way to a pod on the LAN (lan.Manager.PeerTransport); nil: no LAN.
	LAN func(key string) (addr string, t *http.Transport, ok bool)
	// Idle is how long a tunnel stays with no connection (10 minutes by default).
	Idle time.Duration

	dial Dialer

	mu      sync.Mutex
	tunnels map[string]*peerTunnel
	stop    chan struct{}
}

// peerTunnel is the tunnel to one pod: its HTTP transport dials conn:<n> channels on it.
type peerTunnel struct {
	pod  RemotePod
	tr   *http.Transport
	open int       // connections open on it
	last time.Time // the last connection opened or closed
	n    int
}

// NewPeerClient makes the client; dial reaches a pod through the relay (nil: the LAN only).
func NewPeerClient(dial Dialer) *PeerClient {
	return &PeerClient{dial: dial, Idle: 10 * time.Minute, tunnels: map[string]*peerTunnel{}}
}

// RoundTrip sends a request to the pod whose device key is req.URL.Host.
func (c *PeerClient) RoundTrip(req *http.Request) (*http.Response, error) {
	key := req.URL.Host
	if c.LAN != nil {
		if addr, t, ok := c.LAN(key); ok {
			out := req.Clone(req.Context())
			out.URL.Scheme, out.URL.Host, out.Host = "https", addr, addr
			return t.RoundTrip(out)
		}
	}
	pt, err := c.tunnel(req.Context(), key)
	if err != nil {
		return nil, err
	}
	resp, err := pt.tr.RoundTrip(req)
	if err != nil {
		c.drop(key, pt) // a tunnel that broke: the next request dials again
	}
	return resp, err
}

// Route is the way a request to the pod at key goes now: the LAN, the open tunnel (direct or
// through TURN), or idle.
func (c *PeerClient) Route(key string) tunnel.Route {
	if c.LAN != nil {
		if addr, _, ok := c.LAN(key); ok {
			return tunnel.Route{Name: tunnel.RouteLAN, Peer: addr}
		}
	}
	c.mu.Lock()
	pt := c.tunnels[key]
	c.mu.Unlock()
	if pt == nil {
		return tunnel.Route{Name: tunnel.RouteIdle}
	}
	if r, ok := pt.pod.(interface{ Route() tunnel.Route }); ok {
		return r.Route()
	}
	return tunnel.Route{Name: tunnel.RouteRelayDirect}
}

// Close closes every tunnel.
func (c *PeerClient) Close() {
	c.mu.Lock()
	tunnels := c.tunnels
	c.tunnels = map[string]*peerTunnel{}
	if c.stop != nil {
		close(c.stop)
		c.stop = nil
	}
	c.mu.Unlock()
	for _, pt := range tunnels {
		pt.close()
	}
}

func (c *PeerClient) tunnel(ctx context.Context, key string) (*peerTunnel, error) {
	c.mu.Lock()
	if pt := c.tunnels[key]; pt != nil {
		c.mu.Unlock()
		return pt, nil
	}
	c.mu.Unlock()
	if c.dial == nil {
		return nil, errOffline
	}
	pod, err := c.dial.Dial(ctx, key)
	if err != nil {
		return nil, err
	}
	pt := &peerTunnel{pod: pod, last: time.Now()}
	pt.tr = &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			c.mu.Lock()
			pt.n++
			label := "conn:" + strconv.Itoa(pt.n)
			c.mu.Unlock()
			ch, err := pod.Open(ctx, label)
			if err != nil {
				return nil, err
			}
			c.mu.Lock()
			pt.open++
			pt.last = time.Now()
			c.mu.Unlock()
			return &countedConn{Conn: tunnel.NetConn(ch), done: func() {
				c.mu.Lock()
				pt.open--
				pt.last = time.Now()
				c.mu.Unlock()
			}}, nil
		},
		// An idle connection closes soon, so the tunnel can count as idle.
		IdleConnTimeout:     min(30*time.Second, c.Idle/2),
		MaxIdleConnsPerHost: 4,
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if other := c.tunnels[key]; other != nil { // dialed twice at once: keep one
		go pt.close()
		return other, nil
	}
	c.tunnels[key] = pt
	if c.stop == nil {
		c.stop = make(chan struct{})
		go c.janitor(c.stop)
	}
	return pt, nil
}

// janitor closes the tunnels with no connection for Idle.
func (c *PeerClient) janitor(stop chan struct{}) {
	t := time.NewTicker(max(c.Idle/4, 100*time.Millisecond))
	defer t.Stop()
	for {
		select {
		case <-stop:
			return
		case <-t.C:
		}
		c.mu.Lock()
		var idle []*peerTunnel
		for key, pt := range c.tunnels {
			if pt.open == 0 && time.Since(pt.last) >= c.Idle {
				idle = append(idle, pt)
				delete(c.tunnels, key)
			}
		}
		c.mu.Unlock()
		for _, pt := range idle {
			pt.close()
		}
	}
}

func (c *PeerClient) drop(key string, pt *peerTunnel) {
	c.mu.Lock()
	if c.tunnels[key] == pt {
		delete(c.tunnels, key)
	}
	c.mu.Unlock()
	pt.close()
}

func (pt *peerTunnel) close() {
	pt.tr.CloseIdleConnections()
	pt.pod.Close()
}

// countedConn tells when it closes.
type countedConn struct {
	net.Conn
	once sync.Once
	done func()
}

func (c *countedConn) Close() error {
	c.once.Do(c.done)
	return c.Conn.Close()
}

type offlineError struct{}

func (offlineError) Error() string { return "the pod is offline" }

var errOffline = offlineError{}
