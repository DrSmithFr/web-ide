package relay

import (
	"crypto/rand"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/pion/logging"
	turnserver "github.com/pion/turn/v5"
)

// Turn is the embedded Pion TURN/STUN server. It listens on one UDP and one TCP port per
// address of the machine (IPv4, and IPv6 when it has a global one) and serves STUN binding
// and TURN allocations from both. Clients are told to reach it on those ports (and on TLS 443
// when the relay is served over HTTPS). The pod never sees TURN credentials: they are minted
// only for a registered online pod.
type Turn struct {
	store  *Store
	secret []byte
	cred   string
	logger logging.LoggerFactory
	ice    []string

	gens map[string]turnserver.RelayAddressGenerator // by family: "4" or "6"
	srv  *turnserver.Server
	udp  []*net.UDPConn
	tcp  []net.Listener

	mu     sync.Mutex
	active map[string]bool // in-flight short-lived credential usernames
}

// TurnConfig picks the ports and addresses the TURN server uses.
type TurnConfig struct {
	BindHost    string // where to bind the control listeners ("" = the advertised IP)
	AdvertiseIP string // IPv4 given to clients (the machine IP, or 127.0.0.1 in e2e)
	// AdvertiseIPv6: the IPv6 given to clients, when the machine has a global one. Without it
	// the relay is reachable on IPv4 only, and an IPv6 client falls back to the IPv4 address.
	AdvertiseIPv6 string
	// Domain: when the relay has one, the ICE URLs name it instead of the addresses, so the
	// client reaches STUN and TURN wherever the domain points (the addresses stay the fallback
	// for a client that cannot resolve it).
	Domain  string
	Port    int // STUN/TURN control port (0 = 3478)
	TLSPort int // TLS port of the relay, for TURN over TLS (0 = none)
	// TLSListener serves TURN over TLS (RFC 8489) on the connections the demuxer routes to
	// it, usually on the HTTPS port.
	TLSListener net.Listener
	// MinPort/MaxPort: the range the relay sockets of the allocations are bound in. Unset: any
	// free port, which is what a machine with no firewall to declare wants.
	MinPort, MaxPort uint16
}

// NewTurn starts the embedded TURN/STUN server. It binds the control listeners on every
// address of the machine the relay is advertised at, and allocates the relay sockets there.
func NewTurn(st *Store, cfg TurnConfig) (*Turn, error) {
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return nil, err
	}
	if cfg.Port == 0 {
		cfg.Port = 3478
	}
	// One address per family, the IPv4 first: what the ICE URLs name and what the relay sockets
	// of the allocations are bound to.
	var addrs []net.IP
	if ip := net.ParseIP(cfg.AdvertiseIP); ip != nil && ip.To4() != nil {
		addrs = append(addrs, ip)
	}
	if ip := net.ParseIP(cfg.AdvertiseIPv6); ip != nil && ip.To4() == nil {
		addrs = append(addrs, ip)
	}
	if len(addrs) == 0 {
		addrs = []net.IP{net.ParseIP("127.0.0.1")}
	}
	gens := map[string]turnserver.RelayAddressGenerator{}
	for _, ip := range addrs {
		family := "4"
		if ip.To4() == nil {
			family = "6"
		}
		gens[family] = &turnGenerator{ip: ip, minPort: cfg.MinPort, maxPort: cfg.MaxPort}
	}
	t := &Turn{
		store:  st,
		secret: secret,
		cred:   "web-ide",
		logger: logging.NewDefaultLoggerFactory(),
		gens:   gens,
		active: map[string]bool{},
	}
	t.ice = iceURLs(cfg, addrs)

	listeners := []turnserver.ListenerConfig{}
	for _, ip := range addrs {
		bind := ip.String()
		if cfg.BindHost != "" {
			bind = cfg.BindHost
		}
		udp, err := net.ListenUDP("udp", &net.UDPAddr{IP: ip, Port: cfg.Port})
		if err != nil {
			t.Close()
			return nil, err
		}
		tcp, err := net.Listen("tcp", net.JoinHostPort(bind, strconv.Itoa(cfg.Port)))
		if err != nil {
			t.Close()
			return nil, err
		}
		t.udp = append(t.udp, udp)
		t.tcp = append(t.tcp, tcp)
		family := "4"
		if ip.To4() == nil {
			family = "6"
		}
		gen := gens[family]
		listeners = append(listeners, turnserver.ListenerConfig{
			Listener: tcp, RelayAddressGenerator: gen, PermissionHandler: turnserver.DefaultPermissionHandler,
		})
	}
	if cfg.TLSListener != nil {
		// TURN over TLS arrives on the listener of the HTTPS port, whichever family the client
		// used: the allocation is made on the address of the family the client asked for.
		listeners = append(listeners, turnserver.ListenerConfig{
			Listener: cfg.TLSListener, RelayAddressGenerator: &familyGenerator{gens}, PermissionHandler: turnserver.DefaultPermissionHandler,
		})
	}
	var conns []turnserver.PacketConnConfig
	for _, udp := range t.udp {
		family := "4"
		if udp.LocalAddr().(*net.UDPAddr).IP.To4() == nil {
			family = "6"
		}
		conns = append(conns, turnserver.PacketConnConfig{
			PacketConn: udp, RelayAddressGenerator: gens[family], PermissionHandler: turnserver.DefaultPermissionHandler,
		})
	}
	srv, err := turnserver.NewServer(turnserver.ServerConfig{
		Realm:             t.cred,
		LoggerFactory:     t.logger,
		PacketConnConfigs: conns,
		ListenerConfigs:   listeners,
		AuthHandler:       turnserver.LongTermTURNRESTAuthHandler(string(secret), nil),
		EventHandler: turnserver.EventHandler{
			OnAllocationCreated: t.onAllocation,
			OnAllocationDeleted: t.onDeallocate,
		},
	})
	if err != nil {
		t.Close()
		return nil, err
	}
	t.srv = srv
	return t, nil
}

// iceURLs are the STUN and TURN URLs the clients are told to reach: one per address of the
// machine, and the domain when the relay has one (turns: on the TLS port, the scheme of TURN
// over TLS).
func iceURLs(cfg TurnConfig, addrs []net.IP) []string {
	var urls []string
	names := make([]string, 0, len(addrs)+1)
	if cfg.Domain != "" {
		names = append(names, cfg.Domain)
	}
	for _, ip := range addrs {
		names = append(names, ip.String())
	}
	port := strconv.Itoa(cfg.Port)
	for _, n := range names {
		urls = append(urls, "stun:"+net.JoinHostPort(n, port),
			"turn:"+net.JoinHostPort(n, port)+"?transport=udp",
			"turn:"+net.JoinHostPort(n, port)+"?transport=tcp")
	}
	if cfg.TLSListener != nil && cfg.TLSPort != 0 {
		tlsPort := strconv.Itoa(cfg.TLSPort)
		for _, n := range names {
			urls = append(urls, "turns:"+net.JoinHostPort(n, tlsPort)+"?transport=tcp")
		}
	}
	return urls
}

// turnGenerator allocates the relay socket of an allocation on one address of the relay, in a
// port range when the relay was given one (-turn-ports) so a firewall can name fewer ports.
type turnGenerator struct {
	ip       net.IP
	minPort  uint16
	maxPort  uint16
	inner    turnserver.RelayAddressGenerator
	innerSet bool
}

func (g *turnGenerator) Validate() error { return g.generator().Validate() }

func (g *turnGenerator) generator() turnserver.RelayAddressGenerator {
	if !g.innerSet {
		if g.minPort != 0 && g.maxPort >= g.minPort {
			g.inner = &turnserver.RelayAddressGeneratorPortRange{
				RelayAddress: g.ip, Address: g.ip.String(), MinPort: g.minPort, MaxPort: g.maxPort,
			}
		} else {
			g.inner = &turnserver.RelayAddressGeneratorStatic{RelayAddress: g.ip, Address: g.ip.String()}
		}
		g.innerSet = true
	}
	return g.inner
}

func (g *turnGenerator) AllocatePacketConn(conf turnserver.AllocateListenerConfig) (net.PacketConn, net.Addr, error) {
	return g.generator().AllocatePacketConn(conf)
}

func (g *turnGenerator) AllocateListener(conf turnserver.AllocateListenerConfig) (net.Listener, net.Addr, error) {
	return g.generator().AllocateListener(conf)
}

func (g *turnGenerator) AllocateConn(conf turnserver.AllocateConnConfig) (net.Conn, error) {
	return g.generator().AllocateConn(conf)
}

// familyGenerator serves the TLS listener, which carries both families: it hands each
// allocation to the generator of the family the client asked for (the network of the
// allocation, udp6 / tcp6 for an IPv6 client).
type familyGenerator struct {
	gens map[string]turnserver.RelayAddressGenerator
}

func (f *familyGenerator) pick(network string) (turnserver.RelayAddressGenerator, error) {
	family := "4"
	if strings.HasSuffix(network, "6") {
		family = "6"
	}
	gen, ok := f.gens[family]
	if !ok {
		// The relay has no address of that family: an IPv6-only client cannot be served an
		// IPv4 relay address it would not reach, so the allocation is refused.
		return nil, fmt.Errorf("relay: no %s address to allocate a TURN relay on", family)
	}
	return gen, nil
}

func (f *familyGenerator) Validate() error {
	for _, g := range f.gens {
		if err := g.Validate(); err != nil {
			return err
		}
	}
	return nil
}

func (f *familyGenerator) AllocatePacketConn(conf turnserver.AllocateListenerConfig) (net.PacketConn, net.Addr, error) {
	gen, err := f.pick(conf.Network)
	if err != nil {
		return nil, nil, err
	}
	return gen.AllocatePacketConn(conf)
}

func (f *familyGenerator) AllocateListener(conf turnserver.AllocateListenerConfig) (net.Listener, net.Addr, error) {
	gen, err := f.pick(conf.Network)
	if err != nil {
		return nil, nil, err
	}
	return gen.AllocateListener(conf)
}

func (f *familyGenerator) AllocateConn(conf turnserver.AllocateConnConfig) (net.Conn, error) {
	gen, err := f.pick(conf.Network)
	if err != nil {
		return nil, err
	}
	return gen.AllocateConn(conf)
}

func (t *Turn) onAllocation(src, dst net.Addr, protocol, userID, realm string, relayAddr net.Addr, requestedPort int) {
	t.mu.Lock()
	t.active[userID] = true
	t.mu.Unlock()
	if userID != "" {
		_ = t.store.Usage(userID, 0, 0)
	}
}

func (t *Turn) onDeallocate(src, dst net.Addr, protocol, userID, realm string) {
	t.mu.Lock()
	delete(t.active, userID)
	t.mu.Unlock()
}

// ICE returns the STUN and TURN URLs the clients are told to reach (no credentials).
func (t *Turn) ICE() []string { return t.ice }

// Creds issues short-lived TURN credentials (username and password) for an online pod.
// The username is a timestamp plus the pod id; the password is the HMAC-SHA1 long-term
// credential over the shared secret. It fails for a pod that is not registered online.
func (t *Turn) Creds(podID string, ttl time.Duration) (user, pass string, err error) {
	if !t.store.OnlinePod(podID) {
		return "", "", errors.New("relay: no TURN credentials for an offline pod")
	}
	user, pass, err = turnserver.GenerateLongTermTURNRESTCredentials(string(t.secret), podID, ttl)
	if err != nil {
		return "", "", err
	}
	t.mu.Lock()
	t.active[user] = true
	t.mu.Unlock()
	return user, pass, nil
}

// Close stops the TURN server and its listeners.
func (t *Turn) Close() error {
	if t.srv != nil {
		_ = t.srv.Close()
	}
	for _, udp := range t.udp {
		_ = udp.Close()
	}
	for _, ln := range t.tcp {
		_ = ln.Close()
	}
	return nil
}
