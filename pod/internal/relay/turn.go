package relay

import (
	"crypto/rand"
	"errors"
	"net"
	"strconv"
	"sync"
	"time"

	"github.com/pion/logging"
	turnserver "github.com/pion/turn/v5"
)

// Turn is the embedded Pion TURN/STUN server. It listens on one UDP and one TCP port and
// serves STUN binding and TURN allocations from both. Clients are told to reach it on those
// ports (and on TLS 443 when the relay is served over HTTPS). The pod never sees TURN
// credentials: they are minted only for a registered online pod.
type Turn struct {
	store  *Store
	secret []byte
	cred   string
	logger logging.LoggerFactory
	ice    []string

	gen    *turnserver.RelayAddressGeneratorStatic
	srv    *turnserver.Server
	udp    *net.UDPConn
	tcp    net.Listener

	mu     sync.Mutex
	active map[string]bool // in-flight short-lived credential usernames
}

// TurnConfig picks the ports and addresses the TURN server uses.
type TurnConfig struct {
	BindHost    string // where to bind the control listeners ("" = the advertised IP)
	AdvertiseIP string // IP given to clients (the machine IP, or 127.0.0.1 in e2e)
	Port        int    // STUN/TURN control port (0 = 3478)
}

// NewTurn starts the embedded TURN/STUN server. It binds the control listeners and
// allocates relay sockets on the advertised address.
func NewTurn(st *Store, cfg TurnConfig) (*Turn, error) {
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return nil, err
	}
	addr := cfg.AdvertiseIP
	if addr == "" {
		addr = "127.0.0.1"
	}
	if cfg.Port == 0 {
		cfg.Port = 3478
	}
	bind := cfg.BindHost
	if bind == "" {
		bind = addr
	}
	gen := &turnserver.RelayAddressGeneratorStatic{
		RelayAddress: net.ParseIP(addr),
		Address:      addr,
	}
	t := &Turn{
		store:  st,
		secret: secret,
		cred:   "web-ide",
		logger: logging.NewDefaultLoggerFactory(),
		gen:    gen,
		active: map[string]bool{},
		ice: []string{
			"stun:" + net.JoinHostPort(addr, strconv.Itoa(cfg.Port)),
			"turn:" + net.JoinHostPort(addr, strconv.Itoa(cfg.Port)) + "?transport=udp",
			"turn:" + net.JoinHostPort(addr, strconv.Itoa(cfg.Port)) + "?transport=tcp",
		},
	}
	udp, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP(bind), Port: cfg.Port})
	if err != nil {
		return nil, err
	}
	tcp, err := net.Listen("tcp", net.JoinHostPort(bind, strconv.Itoa(cfg.Port)))
	if err != nil {
		udp.Close()
		return nil, err
	}
	t.udp = udp
	t.tcp = tcp
	srv, err := turnserver.NewServer(turnserver.ServerConfig{
		Realm:         t.cred,
		LoggerFactory: t.logger,
		PacketConnConfigs: []turnserver.PacketConnConfig{
			{PacketConn: udp, RelayAddressGenerator: gen, PermissionHandler: turnserver.DefaultPermissionHandler},
		},
		ListenerConfigs: []turnserver.ListenerConfig{
			{Listener: tcp, RelayAddressGenerator: gen, PermissionHandler: turnserver.DefaultPermissionHandler},
		},
		AuthHandler: turnserver.LongTermTURNRESTAuthHandler(string(secret), nil),
		EventHandler: turnserver.EventHandler{
			OnAllocationCreated: t.onAllocation,
			OnAllocationDeleted: t.onDeallocate,
		},
	})
	if err != nil {
		udp.Close()
		tcp.Close()
		return nil, err
	}
	t.srv = srv
	return t, nil
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
	if t.udp != nil {
		_ = t.udp.Close()
	}
	if t.tcp != nil {
		_ = t.tcp.Close()
	}
	return nil
}
