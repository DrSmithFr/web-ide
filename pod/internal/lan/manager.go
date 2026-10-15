package lan

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"log"
	"net"
	"net/http"
	"os"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/coder/websocket"

	"github.com/DrSmithFr/web-ide/pod/internal/i18n"
	"github.com/DrSmithFr/web-ide/pod/internal/identity"
	"github.com/DrSmithFr/web-ide/pod/internal/tunnel"
	"github.com/DrSmithFr/web-ide/pod/internal/update"
)

// DefaultAddr is the LAN listen address when config.json sets none.
const DefaultAddr = ":4436"

// Config of the LAN link of a pod.
type Config struct {
	Dir      string // the data folder: lan.crt, lan.key
	Addr     string // the listen address of the LAN port (":4436")
	Identity *identity.PodIdentity
	Name     string // told to the peers (the host name)
	Version  string // the pod version, told to the peers
	Build    string // the fingerprint of its front end, told to a bridged window (#73)
	Protocol int    // the multi-pod protocol of this pod (multipod.Protocol), told to the peers
	// Handler serves a request of a trusted peer as a request of the machine itself
	// (server.ServeTrusted): the peer proved it is a device of the account.
	Handler http.Handler
	// ServeTunnel runs the protocol of a window a gateway of the site bridged to this pod: its
	// channels arrive on the LAN port (/_lan/tunnel), the rpc one having passed the handshake of
	// the browser the gateway vouches for.
	ServeTunnel func(ctx context.Context, rpc tunnel.MsgConn, chans <-chan tunnel.LabeledConn)
	// Remote serves the page of a pod this pod reaches through the relay rather than the LAN
	// (pod/internal/multipod.RemoteGateway): the same /_lan/pod/ path, over WebRTC.
	Remote http.Handler
	// Peers are the addresses typed by hand (host:port), for a network where mDNS fails.
	Peers []string
	// SavePeers stores the addresses typed by hand (config.json).
	SavePeers func([]string) error
	// Resolver finds and announces the pods by mDNS; nil: the addresses typed only.
	Resolver Resolver
	// OnChange is called when the peers change (the page refreshes its blocks).
	OnChange func()
	// OnRevocations is called with each newer revocation list the pod holds, whatever
	// brought it (the relay link passes it on).
	OnRevocations func(*identity.Revocations)
	// OnBlob, OnWrap and OnSyncKey are called with a sync record a peer passed on: a pod with no
	// link to the relay publishes its project list and reads its wrap through a peer of its site,
	// which forwards them on its own link. The relay checks the original signature, so this adds
	// no trust.
	OnBlob func(*identity.BlobRecord)
	OnWrap func(*identity.WrapRecord)
	OnSync func(*identity.SyncKeyRecord)
	// OnUpdate runs an update request a peer of the site carried to this pod ($update): the pod
	// checks it itself, whatever brought it.
	OnUpdate func(*identity.UpdateRequest) update.Answer
	// Tick is the pace of the dial loop (default 2 s); Browse runs every 5 ticks.
	Tick time.Duration
}

// Manager runs the LAN link of a pod: its listener, the control links with the peers, the
// discovery and the gateway to the peers (ServeGateway).
type Manager struct {
	cfg    Config
	cert   tls.Certificate
	fp     string // the fingerprint of our TLS certificate
	limits *tunnel.RateLimiter
	ctx    context.Context
	port   int

	mu        sync.Mutex
	listen    string // the address actually listened on
	listenErr string
	listening bool
	manual    []string
	cands     map[string]*candidate         // by address
	peers     map[string]*peer              // by device key
	trusted   map[string]*trust             // by TLS fingerprint of a peer
	joins     map[string]*joinReq           // joining pods, by address
	tunnels   map[string]*tunnelWindow      // windows bridged by a gateway, by window id + peer TLS fp
	browsers  map[string]*identity.Cert     // certificates of the browsers of the Devices panel
	idLinks   map[*idLink]bool              // the open links of the Devices panel
	updates   map[string]chan update.Answer // update requests this pod sent, by request id
	seq       int64                         // the seq of the last revocation list passed on
	announced string                        // the account announced by mDNS
	stopAnn   func()
	// transports reach the peers, by the TLS fingerprint they pin.
	transports map[string]*http.Transport
}

// candidate is an address a pod may answer at: typed by hand, found by mDNS, or given by a
// pod that dialed us.
type candidate struct {
	sources map[string]bool // manual, mdns, inbound
	next    time.Time
	fails   int
	err     string
	self    bool
	dialing bool
	key     string // the device key of the pod that answered there
}

// peer is a pod of the account seen on the LAN during this run.
type peer struct {
	cert     *identity.Cert
	name     string
	version  string
	protocol int // the multi-pod protocol it told in its $info (0: older than #60)
	links    map[*link]bool
	out      *link // the link we dialed: the gateway goes through it
	projects json.RawMessage
}

// trust counts the control links up with a peer TLS certificate.
type trust struct {
	cert  *identity.Cert
	links int
}

// New prepares the LAN link; Start runs it.
func New(cfg Config) (*Manager, error) {
	if cfg.Addr == "" {
		cfg.Addr = DefaultAddr
	}
	if cfg.Tick == 0 {
		cfg.Tick = 2 * time.Second
	}
	cert, err := LoadCert(cfg.Dir)
	if err != nil {
		return nil, err
	}
	m := &Manager{
		cfg: cfg, cert: cert, fp: Fingerprint(cert.Certificate[0]),
		limits: tunnel.NewRateLimiter(20, time.Minute),
		cands:  map[string]*candidate{}, peers: map[string]*peer{}, trusted: map[string]*trust{}, joins: map[string]*joinReq{},
		tunnels:  map[string]*tunnelWindow{},
		browsers: map[string]*identity.Cert{}, idLinks: map[*idLink]bool{},
		updates: map[string]chan update.Answer{},
	}
	for _, a := range cfg.Peers {
		m.addCandidate(a, "manual")
	}
	m.manual = append([]string{}, cfg.Peers...)
	return m, nil
}

// SetResolver sets the discovery before Start (mDNS on a real network).
func (m *Manager) SetResolver(r Resolver) { m.cfg.Resolver = r }

// Start runs the LAN link until ctx ends. The LAN port opens once the pod is paired (at
// once, or when the pair command pairs it later): an unpaired pod has nothing to offer the
// network. A port already taken is reported in Settings.
func (m *Manager) Start(ctx context.Context) {
	m.ctx = ctx
	if m.cfg.Identity.Paired() {
		m.listenTLS(ctx)
	}
	go m.loop(ctx)
}

// listenTLS opens the LAN port, once.
func (m *Manager) listenTLS(ctx context.Context) {
	m.mu.Lock()
	if m.listening {
		m.mu.Unlock()
		return
	}
	m.listening = true
	m.mu.Unlock()
	ln, err := tls.Listen("tcp", m.cfg.Addr, &tls.Config{
		Certificates: []tls.Certificate{m.cert},
		ClientAuth:   tls.RequireAnyClientCert, // checked by the handshake, not by a CA
		MinVersion:   tls.VersionTLS13,
		NextProtos:   []string{"http/1.1"}, // WebSockets need HTTP/1.1
	})
	m.mu.Lock()
	if err != nil {
		m.listenErr = err.Error()
		log.Printf("lan: %v", err)
	} else {
		m.listen = ln.Addr().String()
		m.port = ln.Addr().(*net.TCPAddr).Port
	}
	m.mu.Unlock()
	m.changed()
	if ln == nil {
		return
	}
	// The TLS errors of the pods that dial their own announcement are not worth a log line.
	srv := &http.Server{Handler: http.HandlerFunc(m.serveHTTP), ReadHeaderTimeout: 10 * time.Second, ErrorLog: quiet}
	go func() { _ = srv.Serve(ln) }()
	go func() {
		<-ctx.Done()
		_ = srv.Close()
	}()
}

// Port is the LAN port listened on (0 when the listener failed).
func (m *Manager) Port() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.port
}

func (m *Manager) loop(ctx context.Context) {
	t := time.NewTicker(m.cfg.Tick)
	defer t.Stop()
	for n := 0; ; n++ {
		m.refresh()
		if m.cfg.Resolver != nil && n%5 == 0 && m.cfg.Identity.Paired() {
			go m.browse(ctx)
		}
		m.dialDue(ctx)
		select {
		case <-ctx.Done():
			m.closeAll()
			return
		case <-t.C:
		}
	}
}

// refresh reads the identity again when the pair command changed it, announces the pod
// once it is paired, and drops the peers a new revocation list names.
func (m *Manager) refresh() {
	if _, err := m.cfg.Identity.Refresh(); err != nil {
		log.Printf("lan: identity: %v", err)
	}
	account, _, rev := m.cfg.Identity.State()
	if account != "" {
		m.listenTLS(m.ctx)
	}
	// A newer list, from the pair command, the relay or a peer: drop the devices it names
	// and pass it on.
	m.mu.Lock()
	newer := rev != nil && rev.Seq > m.seq
	if newer {
		m.seq = rev.Seq
	}
	m.mu.Unlock()
	if newer {
		m.recheck()
		m.spread(rev, nil)
		if m.cfg.OnRevocations != nil {
			m.cfg.OnRevocations(rev)
		}
	}
	m.mu.Lock()
	port := m.port
	announce := m.cfg.Resolver != nil && account != "" && port != 0 && m.announced != account
	m.mu.Unlock()
	if !announce {
		return
	}
	stop, err := m.cfg.Resolver.Announce(port, Record{Pod: shortID(m.cfg.Identity.ID()), Account: AccountTag(account)})
	if err != nil {
		log.Printf("lan: mDNS: %v", err)
		return
	}
	m.mu.Lock()
	if m.stopAnn != nil {
		m.stopAnn()
	}
	m.stopAnn, m.announced = stop, account
	m.mu.Unlock()
}

// browse asks mDNS for the pods of the account and adds them as candidates.
func (m *Manager) browse(ctx context.Context) {
	account, _, _ := m.cfg.Identity.State()
	found, err := m.cfg.Resolver.Browse(ctx)
	if err != nil {
		return
	}
	tag := AccountTag(account)
	for _, f := range found {
		switch {
		case f.Join != "":
			go m.probeJoin(ctx, f.Addr)
		case f.Account == tag:
			m.addCandidate(f.Addr, "mdns")
		}
	}
}

func (m *Manager) addCandidate(addr, source string) {
	if _, _, err := net.SplitHostPort(addr); err != nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	c := m.cands[addr]
	if c == nil {
		c = &candidate{sources: map[string]bool{}}
		m.cands[addr] = c
	}
	c.sources[source] = true
}

// dialDue dials the candidates that are due: not this pod, not a peer already reached, and
// past their backoff.
func (m *Manager) dialDue(ctx context.Context) {
	if !m.cfg.Identity.Paired() {
		return
	}
	now := time.Now()
	m.mu.Lock()
	var due []string
	for addr, c := range m.cands {
		if c.self || c.dialing || now.Before(c.next) {
			continue
		}
		if p := m.peers[c.key]; c.key != "" && p != nil && p.out != nil {
			continue
		}
		c.dialing = true
		due = append(due, addr)
	}
	m.mu.Unlock()
	for _, addr := range due {
		go m.connect(ctx, addr)
	}
}

// connect dials one address and runs the link until it ends.
func (m *Manager) connect(ctx context.Context, addr string) {
	l, err := m.dial(ctx, addr)
	m.mu.Lock()
	c := m.cands[addr]
	if c == nil {
		c = &candidate{sources: map[string]bool{}}
	}
	c.dialing = false
	if err != nil {
		c.self = errors.Is(err, errSelf)
		c.fails++
		c.err = errorCode(err)
		c.next = time.Now().Add(backoff(c.fails))
		manual := c.sources["manual"]
		m.mu.Unlock()
		// An address typed by hand may be a pod waiting to be paired.
		if manual && c.err == "unreachable" {
			m.probeJoin(ctx, addr)
		}
		m.changed()
		return
	}
	c.fails, c.err, c.key = 0, "", l.peer.Key
	if p := m.peers[l.peer.Key]; p != nil && p.out != nil {
		// Another address of a pod already reached (mDNS gives each of its IPs).
		m.mu.Unlock()
		l.close()
		return
	}
	m.mu.Unlock()
	m.run(l, l.serverFP)
	m.mu.Lock()
	if c := m.cands[addr]; c != nil {
		c.next = time.Now().Add(m.cfg.Tick)
	}
	m.mu.Unlock()
}

// backoff is the wait before dialing again an address that failed.
func backoff(fails int) time.Duration {
	d := time.Second << min(fails, 6)
	return min(d, time.Minute)
}

// errorCode is the short reason shown in Settings for an address that failed.
func errorCode(err error) string {
	switch {
	case errors.Is(err, tunnel.ErrVersion):
		return tunnel.CodeVersion
	case errors.Is(err, tunnel.ErrProof):
		return tunnel.CodeProof
	case errors.Is(err, identity.ErrForeignAccount):
		return tunnel.CodeForeign
	case errors.Is(err, identity.ErrRevoked):
		return tunnel.CodeRevoked
	case errors.Is(err, errSelf):
		return "self"
	}
	var ne net.Error
	if errors.As(err, &ne) || errors.Is(err, os.ErrDeadlineExceeded) {
		return "unreachable"
	}
	if s := err.Error(); len(s) > 0 {
		for _, code := range []string{tunnel.CodeUnpaired, tunnel.CodeForeign, tunnel.CodeRevoked, tunnel.CodeProof, tunnel.CodeCert, tunnel.CodeVersion} {
			if s == "tunnel: "+code {
				return code
			}
		}
	}
	return "unreachable"
}

// serveHTTP is the LAN port: the control link, and the requests of the trusted peers.
func (m *Manager) serveHTTP(w http.ResponseWriter, r *http.Request) {
	fp := leafFingerprint(r.TLS)
	if r.URL.Path == helloPath {
		m.serveHello(w, r, fp)
		return
	}
	m.mu.Lock()
	t := m.trusted[fp]
	m.mu.Unlock()
	if t == nil || m.cfg.Identity.PeerError(t.cert) != nil {
		http.Error(w, "not a device of this account", http.StatusForbidden)
		return
	}
	if r.URL.Path == TunnelPath {
		m.serveTunnel(w, r, fp)
		return
	}
	m.cfg.Handler.ServeHTTP(w, r)
}

// serveHello runs the pod side of the handshake for a pod that dialed us.
func (m *Manager) serveHello(w http.ResponseWriter, r *http.Request, clientFP string) {
	c, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	h := &tunnel.Handshake{
		Identity: m.cfg.Identity,
		// The token mode is not for pods: an unguessable token makes it fail.
		Token:     nonce() + nonce(),
		FPBrowser: clientFP,
		FPPod:     m.fp,
		Limits:    m.limits,
	}
	if err := h.Run(&wsConn{c: c, ctx: r.Context()}); err != nil || h.PeerCert == nil {
		c.CloseNow()
		return
	}
	_, own, _ := m.cfg.Identity.State()
	if h.PeerCert.Kind != identity.KindPod || own == nil || h.PeerCert.Key == own.Key {
		c.CloseNow()
		return
	}
	l := newLink(c, h.PeerCert)
	l.inbound = true
	l.remoteHost, _, _ = net.SplitHostPort(r.RemoteAddr)
	m.run(l, clientFP)
}

// dial reaches a pod at addr and runs the browser side of the handshake: the TLS
// fingerprints of both sides are bound to the signatures, so a machine in the middle that
// shows its own certificate makes a signature fail.
func (m *Manager) dial(ctx context.Context, addr string) (*link, error) {
	var serverFP string
	tr := &http.Transport{TLSClientConfig: &tls.Config{
		Certificates:       []tls.Certificate{m.cert},
		InsecureSkipVerify: true, // trust comes from the handshake
		MinVersion:         tls.VersionTLS13,
		VerifyConnection: func(cs tls.ConnectionState) error {
			serverFP = leafFingerprint(&cs)
			if serverFP == m.fp {
				return errSelf
			}
			return nil
		},
	}}
	defer tr.CloseIdleConnections()
	dctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	c, _, err := websocket.Dial(dctx, "wss://"+addr+helloPath, &websocket.DialOptions{HTTPClient: &http.Client{Transport: tr}})
	if err != nil {
		return nil, err
	}
	account, own, _ := m.cfg.Identity.State()
	timer := time.AfterFunc(10*time.Second, func() { c.CloseNow() })
	peerCert, err := tunnel.RunBrowserCert(&wsConn{c: c, ctx: context.Background()}, m.cfg.Identity.Key(), own, account, m.fp, serverFP, nonce())
	timer.Stop()
	if err == nil {
		err = m.cfg.Identity.PeerError(peerCert) // the revocation list
	}
	if err == nil && (peerCert.Kind != identity.KindPod || peerCert.Key == own.Key) {
		err = errSelf
	}
	if err != nil {
		c.CloseNow()
		return nil, err
	}
	l := newLink(c, peerCert)
	l.addr, l.serverFP = addr, serverFP
	return l, nil
}

// run keeps a control link up: it trusts the peer certificate meanwhile, tells who we are,
// passes the revocation lists on and drops the link when the peer stops answering.
func (m *Manager) run(l *link, peerFP string) {
	m.attach(l, peerFP)
	defer m.detach(l, peerFP)
	_, _, rev := m.cfg.Identity.State()
	_ = l.send("$info", info{Name: m.cfg.Name, Version: m.cfg.Version, Protocol: m.cfg.Protocol, Port: m.Port()})
	if rev != nil {
		_ = l.send("$revocations", rev)
	}
	go func() {
		t := time.NewTicker(15 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-l.done:
				return
			case <-t.C:
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				err := l.conn.c.Ping(ctx)
				cancel()
				if err != nil {
					l.close()
					return
				}
			}
		}
	}()
	for {
		b, err := l.conn.Read()
		if err != nil {
			l.close()
			return
		}
		var msg message
		if json.Unmarshal(b, &msg) != nil {
			continue
		}
		switch msg.Method {
		case "$info":
			var i info
			if json.Unmarshal(msg.Params, &i) == nil {
				m.setInfo(l, i)
			}
		case "$revocations":
			var r identity.Revocations
			if json.Unmarshal(msg.Params, &r) != nil {
				continue
			}
			if newer, err := m.cfg.Identity.Apply(&r); err == nil && newer {
				m.recheck()
				m.spread(&r, l)
			}
		case "$blob":
			var b identity.BlobRecord
			if json.Unmarshal(msg.Params, &b) == nil && m.cfg.OnBlob != nil {
				m.cfg.OnBlob(&b)
			}
		case "$wrap":
			var w identity.WrapRecord
			if json.Unmarshal(msg.Params, &w) == nil {
				// The wrap is sealed for its device: a peer that is not it only passes it on.
				if w.Device == m.cfg.Identity.ID() {
					if m.cfg.OnWrap != nil {
						m.cfg.OnWrap(&w)
					}
					continue
				}
				m.spreadWrap(&w, l)
			}
		case "$syncKey":
			var k identity.SyncKeyRecord
			if json.Unmarshal(msg.Params, &k) == nil && m.cfg.OnSync != nil {
				m.cfg.OnSync(&k)
			}
		case "$update":
			// A peer of the site carried an update request for this pod. The pod checks it itself:
			// a peer is a device of the account, not an authority.
			var u updateMsg
			if json.Unmarshal(msg.Params, &u) == nil && m.cfg.OnUpdate != nil {
				ans := m.cfg.OnUpdate(u.Update)
				_ = l.send("$update.done", updateDone{Req: u.Req, Status: ans.Status, Message: ans.Message, Command: ans.Command})
			}
		case "$update.done":
			var d updateDone
			if json.Unmarshal(msg.Params, &d) == nil {
				m.mu.Lock()
				ch := m.updates[d.Req]
				m.mu.Unlock()
				if ch != nil {
					select {
					case ch <- update.Answer{Status: d.Status, Message: d.Message, Command: d.Command}:
					default:
					}
				}
			}
		}
	}
}

// attach records a link that passed the handshake.
func (m *Manager) attach(l *link, peerFP string) {
	m.mu.Lock()
	t := m.trusted[peerFP]
	if t == nil {
		t = &trust{cert: l.peer}
		m.trusted[peerFP] = t
	}
	t.links++
	p := m.peers[l.peer.Key]
	if p == nil {
		p = &peer{links: map[*link]bool{}}
		m.peers[l.peer.Key] = p
	}
	p.cert = l.peer
	if p.name == "" {
		p.name = l.peer.Name
	}
	p.links[l] = true
	start := false
	if !l.inbound && p.out == nil {
		p.out = l
		start = true
	}
	m.mu.Unlock()
	if start {
		go m.watchProjects(l)
	}
	m.changed()
}

// detach forgets a link that ended.
func (m *Manager) detach(l *link, peerFP string) {
	m.mu.Lock()
	if t := m.trusted[peerFP]; t != nil {
		if t.links--; t.links <= 0 {
			delete(m.trusted, peerFP)
		}
	}
	if p := m.peers[l.peer.Key]; p != nil {
		delete(p.links, l)
		if p.out == l {
			p.out = nil
		}
	}
	m.mu.Unlock()
	m.changed()
}

// setInfo records what a peer told about itself; a peer that dialed us gives the port to
// dial it back.
func (m *Manager) setInfo(l *link, i info) {
	m.mu.Lock()
	if p := m.peers[l.peer.Key]; p != nil {
		if i.Name != "" {
			p.name = i.Name
		}
		p.version, p.protocol = i.Version, i.Protocol
	}
	m.mu.Unlock()
	if l.inbound && i.Port > 0 && l.remoteHost != "" {
		m.addCandidate(net.JoinHostPort(l.remoteHost, strconv.Itoa(i.Port)), "inbound")
	}
	m.changed()
}

// recheck drops the links of the peers that are no longer devices of the account (a new
// revocation list, or the pod paired with another account).
func (m *Manager) recheck() {
	m.mu.Lock()
	var drop []*link
	for _, p := range m.peers {
		if m.cfg.Identity.PeerError(p.cert) == nil {
			continue
		}
		for l := range p.links {
			drop = append(drop, l)
		}
	}
	m.mu.Unlock()
	for _, l := range drop {
		l.close()
	}
}

// PublishBlob hands a sealed blob to a peer of the site: a pod with no link to the relay publishes
// its project list this way, and the peer puts it on its own link unchanged. The relay checks the
// signature of the device that wrote it, so the hop adds no trust.
func (m *Manager) PublishBlob(b *identity.BlobRecord) error {
	l := m.publishLink()
	if l == nil {
		return errors.New("lan: no peer of the account to publish through")
	}
	return l.send("$blob", b)
}

// PublishSyncKey hands the X25519 key of this device to a peer, so an admin can wrap the epoch key
// for a pod that has no link of its own.
func (m *Manager) PublishSyncKey(k *identity.SyncKeyRecord) error {
	l := m.publishLink()
	if l == nil {
		return errors.New("lan: no peer of the account to publish through")
	}
	return l.send("$syncKey", k)
}

// UpdateWait is how long this pod waits for the answer of an update request it carried: a pod
// older than #60 knows no $update and never answers, and the page then shows the command to run
// by hand.
const UpdateWait = 10 * time.Second

// SendUpdate carries a signed update request to a peer of the site over its control link and waits
// for its answer. The peer runs the checks itself: what this pod adds is the route, not the trust.
func (m *Manager) SendUpdate(ctx context.Context, key string, req *identity.UpdateRequest) (*update.Answer, error) {
	m.mu.Lock()
	var l *link
	if p := m.peers[key]; p != nil && p.out != nil && !p.out.closed() {
		l = p.out
	}
	m.mu.Unlock()
	if l == nil {
		return nil, errors.New("lan: this pod is not a peer of the local network")
	}
	id := nonce()
	ch := make(chan update.Answer, 1)
	m.mu.Lock()
	if m.updates == nil {
		m.updates = map[string]chan update.Answer{}
	}
	m.updates[id] = ch
	m.mu.Unlock()
	defer func() {
		m.mu.Lock()
		delete(m.updates, id)
		m.mu.Unlock()
	}()
	if err := l.send("$update", updateMsg{Req: id, Update: req}); err != nil {
		return nil, err
	}
	select {
	case a := <-ch:
		return &a, nil
	case <-time.After(UpdateWait):
		// No answer: the pod on the other side is older than this ticket. The user runs the command.
		return &update.Answer{Status: update.StatusNotService, Command: update.Command(req.Version)}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// PassWrap forwards a wrap the relay pushed for a pod of this site that has no link of its own: it
// is the only way that pod can read the epoch key.
func (m *Manager) PassWrap(w *identity.WrapRecord) error {
	m.mu.Lock()
	var links []*link
	for _, p := range m.peers {
		for l := range p.links {
			links = append(links, l)
		}
	}
	m.mu.Unlock()
	if len(links) == 0 {
		return errors.New("lan: no peer of the account to pass a wrap to")
	}
	var err error
	for _, l := range links {
		if e := l.send("$wrap", w); e != nil {
			err = e
		}
	}
	return err
}

// publishLink is a link we dialed (the one the gateway would use for the peer's page), or any open
// control link.
func (m *Manager) publishLink() *link {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, p := range m.peers {
		if p.out != nil && !p.out.closed() {
			return p.out
		}
	}
	for _, p := range m.peers {
		for l := range p.links {
			if !l.closed() {
				return l
			}
		}
	}
	return nil
}

// spreadWrap passes a wrap for another device on to the other peers: the pod it is for reaches the
// relay through one of them, so the key comes back the same way.
func (m *Manager) spreadWrap(w *identity.WrapRecord, from *link) {
	m.mu.Lock()
	var links []*link
	for _, p := range m.peers {
		for l := range p.links {
			if l != from {
				links = append(links, l)
			}
		}
	}
	m.mu.Unlock()
	for _, l := range links {
		_ = l.send("$wrap", w)
	}
}

// spread passes a newer revocation list on to the other peers.
func (m *Manager) spread(r *identity.Revocations, from *link) {
	m.mu.Lock()
	var links []*link
	for _, p := range m.peers {
		for l := range p.links {
			if l != from {
				links = append(links, l)
			}
		}
	}
	m.mu.Unlock()
	for _, l := range links {
		_ = l.send("$revocations", r)
	}
}

func (m *Manager) closeAll() {
	m.mu.Lock()
	var links []*link
	for _, p := range m.peers {
		for l := range p.links {
			links = append(links, l)
		}
	}
	if m.stopAnn != nil {
		m.stopAnn()
	}
	m.mu.Unlock()
	for _, l := range links {
		l.close()
	}
}

func (m *Manager) changed() {
	if m.cfg.OnChange != nil {
		m.cfg.OnChange()
	}
}

// ---------- the view of the page ----------

// PeerView is a pod of the account on the LAN, as Settings and the home page show it.
type PeerView struct {
	ID          string `json:"id"` // the device key
	Name        string `json:"name"`
	Fingerprint string `json:"fingerprint"`
	Version     string `json:"version"`
	// Protocol is the multi-pod protocol the peer told (0: a pod older than #60).
	Protocol int    `json:"protocol"`
	Addr     string `json:"addr"`
	Online   bool   `json:"online"`
	// Path is where the page of the peer is served through this pod (empty: offline).
	Path     string          `json:"path"`
	Projects json.RawMessage `json:"projects,omitempty"`
}

// AddrView is an address that did not lead to a pod of the account.
type AddrView struct {
	Addr   string `json:"addr"`
	Manual bool   `json:"manual"`
	Error  string `json:"error"`
}

// Status is the state of the LAN link, for Settings > Remote access and the home page.
type Status struct {
	Listen string     `json:"listen"`
	Error  string     `json:"error,omitempty"`
	Paired bool       `json:"paired"`
	Peers  []PeerView `json:"peers"`
	Manual []string   `json:"manual"`
	Failed []AddrView `json:"failed"`
}

// Status returns the state of the LAN link.
func (m *Manager) Status() Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	st := Status{Listen: m.listen, Error: m.listenErr, Paired: m.cfg.Identity.Paired(),
		Peers: []PeerView{}, Manual: append([]string{}, m.manual...), Failed: []AddrView{}}
	for key, p := range m.peers {
		// A revoked pod is gone, not offline.
		if m.cfg.Identity.PeerError(p.cert) != nil {
			continue
		}
		v := PeerView{ID: key, Name: p.name, Fingerprint: identity.Fingerprint(key), Version: p.version, Protocol: p.protocol, Online: p.out != nil, Projects: p.projects}
		if p.out != nil {
			v.Addr = p.out.addr
		}
		if p.out != nil {
			v.Path = PeerPrefix + key
		}
		st.Peers = append(st.Peers, v)
	}
	sort.Slice(st.Peers, func(i, j int) bool { return st.Peers[i].Name < st.Peers[j].Name })
	for addr, c := range m.cands {
		if c.err != "" && !c.self && m.joins[addr] == nil {
			st.Failed = append(st.Failed, AddrView{Addr: addr, Manual: c.sources["manual"], Error: c.err})
		}
	}
	sort.Slice(st.Failed, func(i, j int) bool { return st.Failed[i].Addr < st.Failed[j].Addr })
	return st
}

// SetManual replaces the addresses typed by hand and stores them.
func (m *Manager) SetManual(addrs []string) error {
	clean := []string{}
	for _, a := range addrs {
		if a == "" {
			continue
		}
		if _, _, err := net.SplitHostPort(a); err != nil {
			return i18n.Errorf("%s is not an address host:port", a)
		}
		clean = append(clean, a)
	}
	if m.cfg.SavePeers != nil {
		if err := m.cfg.SavePeers(clean); err != nil {
			return err
		}
	}
	m.mu.Lock()
	for addr, c := range m.cands {
		delete(c.sources, "manual")
		if len(c.sources) == 0 && !c.dialing {
			delete(m.cands, addr)
		}
	}
	m.manual = clean
	m.mu.Unlock()
	for _, a := range clean {
		m.addCandidate(a, "manual")
	}
	m.changed()
	return nil
}

// View is Status for the page (server.LAN).
func (m *Manager) View() any { return m.Status() }
