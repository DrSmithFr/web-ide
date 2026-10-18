package relay

import (
	"crypto/rand"
	"errors"
	"fmt"
	"log"
	"net"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
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
	allocs int             // allocations open right now (#72: one of them takes one relay port)
	usage  *usageRegistry
	quota  int64 // bytes per month per key (0: no limit)
	backer int64 // the quota of a Backer (#87)
	level  func(key string) AccountLevel
	stop   chan struct{}
}

// errQuota: the key reached its monthly TURN quota and is not freed from it. Only the relayed
// path is refused: the direct and STUN paths stay open.
var errQuota = errors.New("quota")

// QuotaState is what the relay knows of the TURN month of a key: the bytes it moved, the
// monthly limit (0: none), the level of the account (#87) and whether it is over. It travels to
// the pod (Settings > Remote access) and to the bootstrap page. Supporter (no quota) stays for
// the pods that read only it; Sponsor and VIP are the sources of the level, for the badge.
type QuotaState struct {
	Used      int64  `json:"used"`
	Limit     int64  `json:"limit"`
	Supporter bool   `json:"supporter,omitempty"`
	Level     string `json:"level,omitempty"`
	Until     int64  `json:"until,omitempty"`
	Sponsor   string `json:"sponsor,omitempty"`
	VIP       bool   `json:"vip,omitempty"`
	Over      bool   `json:"over,omitempty"`
}

// levelOf reads the level of a key (free without a hook: a relay with no levels).
func (t *Turn) levelOf(key string) AccountLevel {
	if t.level == nil || key == "" {
		return AccountLevel{}
	}
	return t.level(key)
}

// limit is the monthly quota of a level: -quota for free, -quota-backer (twice -quota unset) for a
// Backer, none above. A relay with no -quota has no limit at any level.
func (t *Turn) limit(lv Level) int64 {
	switch {
	case t.quota <= 0 || lv >= LevelSupporter:
		return 0
	case lv == LevelBacker && t.backer > 0:
		return t.backer
	case lv == LevelBacker:
		return 2 * t.quota
	}
	return t.quota
}

// used is what a key moved this month: what the base already holds, plus what its live
// allocations are relaying right now. A quota is therefore felt as it is crossed, not a flush
// later.
func (t *Turn) used(key string) int64 {
	if key == "" {
		return 0
	}
	n := t.usage.total(key)
	if stored, err := t.store.TurnUsage(key, usageMonth(time.Now())); err == nil {
		n += stored
	}
	return n
}

// QuotaState reads the month of a key. An account over its quota is told so before it dials,
// not after a failed tunnel.
func (t *Turn) QuotaState(key string) *QuotaState {
	al := t.levelOf(key)
	st := &QuotaState{Limit: t.limit(al.Level), Supporter: al.Level >= LevelSupporter,
		Level: al.Level.wire(), Until: al.Until, Sponsor: al.Sponsor.wire(), VIP: al.VIP}
	st.Used = t.used(key)
	st.Over = st.Limit > 0 && st.Used >= st.Limit
	return st
}

// usageRegistry holds the bytes relayed by the live allocations, keyed by the key the TURN
// credential names (the account of a paired device, the pod id otherwise). The counters are
// atomic — a relayed packet only adds to one number — and flushed to the base once a minute,
// aggregated per key and month.
type usageRegistry struct {
	mu      sync.Mutex
	entries map[string]*usageCount
}

type usageCount struct{ in, out atomic.Int64 }

func newUsageRegistry() *usageRegistry {
	return &usageRegistry{entries: map[string]*usageCount{}}
}

// count returns the counter of a key, creating it on the first allocation.
func (r *usageRegistry) count(key string) *usageCount {
	if key == "" {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	c, ok := r.entries[key]
	if !ok {
		c = &usageCount{}
		r.entries[key] = c
	}
	return c
}

// drain takes the counters accumulated since the last flush and resets them: the base keeps
// one row per key and month, whatever the number of allocations behind it.
func (r *usageRegistry) drain() map[string][2]int64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := map[string][2]int64{}
	for key, c := range r.entries {
		in, sent := c.in.Swap(0), c.out.Swap(0)
		if in != 0 || sent != 0 {
			out[key] = [2]int64{in, sent}
		}
	}
	return out
}

// total is what a key has relayed since the last flush, without resetting it.
func (r *usageRegistry) total(key string) int64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	c, ok := r.entries[key]
	if !ok {
		return 0
	}
	return c.in.Load() + c.out.Load()
}

// release takes the bytes one key moved since the last flush, and resets it: an allocation that
// ends writes its traffic to the base at once.
func (r *usageRegistry) release(key string) ([2]int64, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	c, ok := r.entries[key]
	if !ok {
		return [2]int64{}, false
	}
	in, sent := c.in.Swap(0), c.out.Swap(0)
	if in == 0 && sent == 0 {
		return [2]int64{}, false
	}
	return [2]int64{in, sent}, true
}

// usageFlushEvery is how often the live counters reach the base: coarse enough that a relay
// writes a handful of rows a minute, fine enough that a quota is not exceeded by more than a
// minute of traffic. An allocation that ends is written at once: the next credential asked by
// that key is checked against what it really moved.
const usageFlushEvery = time.Minute

// flushUsage writes what the allocations relayed since last time into the base.
func (t *Turn) flushUsage() {
	for key, bytes := range t.usage.drain() {
		if err := t.store.AddUsage(key, usageMonth(time.Now()), bytes[0], bytes[1]); err != nil {
			log.Printf("relay: usage of %s: %v", key, err)
		}
	}
}

// TurnConfig picks the ports and addresses the TURN server uses.
type TurnConfig struct {
	BindHost    string // where to bind the control listeners ("" = the advertised IP)
	AdvertiseIP string // IPv4 given to clients (the machine IP, or 127.0.0.1 in e2e)
	// AdvertiseIPv6: the IPv6 given to clients, when the machine has a global one. Without it
	// the relay is reachable on IPv4 only, and an IPv6 client falls back to the IPv4 address.
	AdvertiseIPv6 string
	// Domain: when the relay has one, the ICE URLs name it alone, so the client reaches STUN and
	// TURN wherever the domain points, with one allocation per transport (see iceURLs).
	Domain  string
	Port    int // STUN/TURN control port (0 = 3478)
	TLSPort int // TLS port of the relay, for TURN over TLS (0 = none)
	// TLSListener serves TURN over TLS (RFC 8489) on the connections the demuxer routes to
	// it, usually on the HTTPS port.
	TLSListener net.Listener
	// MinPort/MaxPort: the range the relay sockets of the allocations are bound in. Unset: any
	// free port, which is what a machine with no firewall to declare wants.
	MinPort, MaxPort uint16
	// Quota: the bytes one key (an account, or a pod while it is unpaired) may move through
	// TURN in a month. 0: no limit, which is what a self-hosted relay wants.
	Quota int64
	// QuotaBacker: the quota of a Backer (0: twice Quota).
	QuotaBacker int64
	// Level reads the level of a key (level.go), which sets its quota. nil: everyone is free.
	Level func(key string) AccountLevel
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
	usage := newUsageRegistry()
	for _, ip := range addrs {
		family := "4"
		if ip.To4() == nil {
			family = "6"
		}
		gens[family] = &turnGenerator{ip: ip, minPort: cfg.MinPort, maxPort: cfg.MaxPort, usage: usage}
	}
	t := &Turn{
		store:  st,
		secret: secret,
		cred:   "web-ide",
		logger: logging.NewDefaultLoggerFactory(),
		gens:   gens,
		active: map[string]bool{},
		usage:  usage,
		quota:  cfg.Quota,
		backer: cfg.QuotaBacker,
		level:  cfg.Level,
		stop:   make(chan struct{}),
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
	go t.flushLoop()
	return t, nil
}

// flushLoop writes the live byte counters to the base once a minute: the base holds one row per
// key and month, not one per allocation.
func (t *Turn) flushLoop() {
	ticker := time.NewTicker(usageFlushEvery)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			t.flushUsage()
		case <-t.stop:
			return
		}
	}
}

// iceURLs are the STUN and TURN URLs the clients are told to reach. Each TURN URL costs an
// allocation (a relay port) on each end of a tunnel, taken while ICE gathers, so the list is kept
// short (#72). With a domain, the domain alone: TURN over UDP, and TURN over TLS on the TLS port
// as the fallback of a network that blocks UDP — it passes where TCP 3478 is blocked too. With
// no domain, one name per address of the machine, over UDP and TCP (turns: there only when a
// TLS side exists, a certificate rarely names an address).
func iceURLs(cfg TurnConfig, addrs []net.IP) []string {
	port := strconv.Itoa(cfg.Port)
	tls := cfg.TLSListener != nil && cfg.TLSPort != 0
	if cfg.Domain != "" {
		urls := []string{"stun:" + net.JoinHostPort(cfg.Domain, port),
			"turn:" + net.JoinHostPort(cfg.Domain, port) + "?transport=udp"}
		if tls {
			return append(urls, "turns:"+net.JoinHostPort(cfg.Domain, strconv.Itoa(cfg.TLSPort))+"?transport=tcp")
		}
		return append(urls, "turn:"+net.JoinHostPort(cfg.Domain, port)+"?transport=tcp")
	}
	var urls []string
	for _, ip := range addrs {
		n := ip.String()
		urls = append(urls, "stun:"+net.JoinHostPort(n, port),
			"turn:"+net.JoinHostPort(n, port)+"?transport=udp",
			"turn:"+net.JoinHostPort(n, port)+"?transport=tcp")
	}
	if tls {
		tlsPort := strconv.Itoa(cfg.TLSPort)
		for _, ip := range addrs {
			urls = append(urls, "turns:"+net.JoinHostPort(ip.String(), tlsPort)+"?transport=tcp")
		}
	}
	return urls
}

// turnGenerator allocates the relay socket of an allocation on one address of the relay, in a
// port range when the relay was given one (-turn-ports) so a firewall can name fewer ports. It
// wraps what it allocates with the byte counter of the key the credential names, which is how
// the relay counts the TURN traffic of an account (see usageRegistry).
type turnGenerator struct {
	ip       net.IP
	minPort  uint16
	maxPort  uint16
	usage    *usageRegistry
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
	conn, addr, err := g.generator().AllocatePacketConn(conf)
	if err != nil {
		return nil, nil, err
	}
	if c := g.usage.count(conf.UserID); c != nil {
		conn = &countedPacketConn{PacketConn: conn, usage: c}
	}
	return conn, addr, nil
}

func (g *turnGenerator) AllocateListener(conf turnserver.AllocateListenerConfig) (net.Listener, net.Addr, error) {
	ln, addr, err := g.generator().AllocateListener(conf)
	if err != nil {
		return nil, nil, err
	}
	if c := g.usage.count(conf.UserID); c != nil {
		ln = &countedListener{Listener: ln, usage: c}
	}
	return ln, addr, nil
}

func (g *turnGenerator) AllocateConn(conf turnserver.AllocateConnConfig) (net.Conn, error) {
	conn, err := g.generator().AllocateConn(conf)
	if err != nil {
		return nil, err
	}
	if c := g.usage.count(conf.UserID); c != nil {
		conn = &countedConn{Conn: conn, usage: c}
	}
	return conn, nil
}

// countedPacketConn counts the bytes one UDP relay socket moved. The counters are atomic: a
// relayed packet costs one add, no lock.
type countedPacketConn struct {
	net.PacketConn
	usage *usageCount
}

func (c *countedPacketConn) ReadFrom(p []byte) (int, net.Addr, error) {
	n, addr, err := c.PacketConn.ReadFrom(p)
	c.usage.in.Add(int64(n))
	return n, addr, err
}

func (c *countedPacketConn) WriteTo(p []byte, addr net.Addr) (int, error) {
	n, err := c.PacketConn.WriteTo(p, addr)
	c.usage.out.Add(int64(n))
	return n, err
}

// countedListener counts the bytes of the TCP relay sockets it accepts, and of the outbound
// connections of a TURN Connect (RFC 6062).
type countedListener struct {
	net.Listener
	usage *usageCount
}

func (l *countedListener) Accept() (net.Conn, error) {
	c, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	return &countedConn{Conn: c, usage: l.usage}, nil
}

type countedConn struct {
	net.Conn
	usage *usageCount
}

func (c *countedConn) Read(b []byte) (int, error) {
	n, err := c.Conn.Read(b)
	c.usage.in.Add(int64(n))
	return n, err
}

func (c *countedConn) Write(b []byte) (int, error) {
	n, err := c.Conn.Write(b)
	c.usage.out.Add(int64(n))
	return n, err
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
	t.allocs++
	t.mu.Unlock()
	// The byte counter of that allocation lives in the registry, under the key the credential
	// names; the flush writes it to the base.
	t.usage.count(userID)
}

func (t *Turn) onDeallocate(src, dst net.Addr, protocol, userID, realm string) {
	// The allocation counts as open until its bytes are written: Close waits for that.
	defer func() {
		t.mu.Lock()
		if t.allocs > 0 {
			t.allocs--
		}
		t.mu.Unlock()
	}()
	t.mu.Lock()
	delete(t.active, userID)
	t.mu.Unlock()
	// The allocation is over: its bytes go to the base now, so the quota a key is held to is
	// what it moved, not what it moved a minute ago.
	if key := usageKeyOf(userID); key != "" {
		if bytes, ok := t.usage.release(key); ok {
			if err := t.store.AddUsage(key, usageMonth(time.Now()), bytes[0], bytes[1]); err != nil {
				log.Printf("relay: usage of %s: %v", key, err)
			}
		}
	}
}

// usageKeyOf is the key an allocation belongs to, read back from the username of the credential
// that opened it (<expiry>:<key>).
func usageKeyOf(userID string) string {
	if _, key, ok := strings.Cut(userID, ":"); ok {
		return key
	}
	return userID
}

// ICE returns the STUN and TURN URLs the clients are told to reach (no credentials).
func (t *Turn) ICE() []string { return t.ice }

// Creds issues short-lived TURN credentials for a session of an online pod. The username is
// <expiry>:<key>, the key naming whose traffic the allocations count: the account of a paired
// pod or browser, the pod id otherwise (a browser gets the account of the pod it dials). The
// password is the HMAC-SHA1 long-term credential over the shared secret. It fails for a pod
// that is not registered online.
func (t *Turn) Creds(podID, key string, ttl time.Duration) (user, pass string, err error) {
	if !t.store.OnlinePod(podID) {
		return "", "", errors.New("relay: no TURN credentials for an offline pod")
	}
	if key == "" {
		key = podID
	}
	// The monthly quota of the hosted relay (-quota), by level: a Backer has more, a Supporter
	// none, and a self-hosted relay has none at all (Quota 0). Refusing here only closes the
	// relayed path — the browser and the pod still try their direct and STUN candidates.
	if limit := t.limit(t.levelOf(key).Level); limit > 0 && t.used(key) >= limit {
		return "", "", errQuota
	}
	user, pass, err = turnserver.GenerateLongTermTURNRESTCredentials(string(t.secret), key, ttl)
	if err != nil {
		return "", "", err
	}
	t.mu.Lock()
	t.active[user] = true
	t.mu.Unlock()
	return user, pass, nil
}

// closeWait bounds how long Close waits for the allocations to report their end.
const closeWait = 5 * time.Second

// waitAllocations waits until no allocation is open, or the delay is over.
func (t *Turn) waitAllocations(d time.Duration) {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		t.mu.Lock()
		n := t.allocs
		t.mu.Unlock()
		if n == 0 {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// Close stops the TURN server, its listeners, and the flush of the byte counters (what was
// relayed is written before the relay stops).
func (t *Turn) Close() error {
	if t.stop != nil {
		select {
		case <-t.stop:
		default:
			close(t.stop)
		}
	}
	if t.srv != nil {
		_ = t.srv.Close()
		// Closing the sockets ends the allocations on goroutines of Pion's: each one reports
		// OnAllocationDeleted, which writes its bytes to the base. Wait for them, so nothing
		// writes once Close has returned (the base may be closed, or its folder removed, right
		// after).
		t.waitAllocations(closeWait)
	}
	for _, udp := range t.udp {
		_ = udp.Close()
	}
	for _, ln := range t.tcp {
		_ = ln.Close()
	}
	if t.usage != nil {
		t.flushUsage()
	}
	return nil
}
