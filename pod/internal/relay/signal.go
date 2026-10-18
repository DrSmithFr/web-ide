package relay

import (
	"context"
	"encoding/json"
	"io/fs"
	"net/http"
	"sync"
	"time"

	"github.com/coder/websocket"

	"github.com/DrSmithFr/web-ide/pod/internal/identity"
)

// turnTTL is the validity of a short-lived TURN credential: short enough that a leaked one
// dies soon and the monthly quota is re-checked often. TURN re-checks the expiry on every
// Refresh and CreatePermission, so a longer session asks fresh ones before it ends (an "ice"
// message of the browser) and restarts ICE with them (#86).
const turnTTL = time.Hour

// sigMsg is one signaling message between the relay and a client (pod or browser).
// Every message carries the session id: a pod may serve several browsers at once, each
// in its own session.
type sigMsg struct {
	Type       string          `json:"type"`
	Session    string          `json:"session,omitempty"`
	URLs       []string        `json:"urls,omitempty"`
	Username   string          `json:"username,omitempty"`
	Credential string          `json:"credential,omitempty"`
	TTL        int64           `json:"ttl,omitempty"` // seconds the credential is valid from now (#86): no clock to agree on
	SDP        string          `json:"sdp,omitempty"`
	Candidate  json.RawMessage `json:"candidate,omitempty"`
	// Pod is the pod a session of the identity link signals to (#75): its "signal" message opens
	// the session, as /_relay/signal?pod= does.
	Pod     string                `json:"pod,omitempty"`
	ID      string                `json:"id,omitempty"`
	Secret  string                `json:"secret,omitempty"`
	Name    string                `json:"name,omitempty"`
	Account string                `json:"account,omitempty"`
	Device  string                `json:"device,omitempty"`
	Cert    *identity.Cert        `json:"cert,omitempty"`
	List    *identity.Revocations `json:"list,omitempty"` // pushed to the pods of an account
	// Sync (milestone 4): the pod publishes its X25519 key, reads the wrap of the epoch key made
	// for it, and exchanges encrypted blobs. The relay cannot read a blob, it only checks the
	// signature against the device that wrote it.
	SyncKey *identity.SyncKeyRecord `json:"syncKey,omitempty"`
	Wrap    *identity.WrapRecord    `json:"wrap,omitempty"`
	Blob    *identity.BlobRecord    `json:"blob,omitempty"`
	Kind    string                  `json:"kind,omitempty"`
	Updated int64                   `json:"updated,omitempty"` // time the stored blob or wrap was written
	Message string                  `json:"message,omitempty"`
	// Clusters (milestone 3): register carries the pod version, its multi-pod protocol and the
	// preferred switch, `site` the device keys of its LAN peers, `challenge`/`proof` the nonce of
	// the proof of possession, and `pods`/`gateway` what the relay sees of the account.
	Prove     bool      `json:"prove,omitempty"`
	Nonce     string    `json:"nonce,omitempty"`
	Sig       string    `json:"sig,omitempty"`
	Version   string    `json:"version,omitempty"`
	Build     string    `json:"build,omitempty"` // the fingerprint of the pod's front end (#73)
	Protocol  int       `json:"protocol,omitempty"`
	Preferred bool      `json:"preferred,omitempty"`
	Renew     bool      `json:"renew,omitempty"` // register: the pod answers an offer on a live session (#86)
	Peers     []string  `json:"peers,omitempty"`
	Cluster   string    `json:"site,omitempty"` // "site" on the wire: the cluster (GLOSSARY.md)
	Gateway   string    `json:"gateway,omitempty"`
	Pods      []PodInfo `json:"pods,omitempty"`
	// Update this pod (#60): the relay is a plain carrier of a signed update request between two
	// pods of the same account. req is the id of the hop the relay handed out, echoed on the answer.
	Update *identity.UpdateRequest `json:"update,omitempty"`
	// Power (relay.md, *Power*): a signed power request, carried like an update. A wake is for a
	// machine that sleeps: it goes to every other online pod of the account, and the first that
	// sent the magic packet answers.
	Power   *identity.PowerRequest `json:"power,omitempty"`
	Req     string                 `json:"req,omitempty"`
	Status  string                 `json:"status,omitempty"`
	Command string                 `json:"command,omitempty"`
	// Quota (milestone 5): the TURN month of the account, carried on the ice message (when the
	// credentials were refused, the relayed path is closed but the direct paths stay open) and
	// on the ready message of a pod (Settings > Remote access shows it).
	Quota *QuotaState `json:"quota,omitempty"`
}

// Server routes signaling between the pods (one persistent link each) and the browsers
// that open links to them, by session id, and serves the bootstrap page, the Service
// Worker and the embedded TURN/STUN. The pod token never crosses it: the token is
// proved inside the DataChannel, bound to both DTLS fingerprints, so the relay cannot
// MITM.
type Server struct {
	store          *Store
	turn           *Turn
	mu             sync.Mutex
	pods           map[string]*podConn      // online pods, by pod id
	browsers       map[string]*browserConn  // active browsers, by session id
	links          map[*identityConn]bool   // live pairing/admin links
	codes          map[string]*identityConn // pairing code → the link that created it
	joiners        map[string]*identityConn // pairing code → the link that posted its key to it
	since          map[string]time.Time     // device key → when its link came up (clusters.go)
	gateways       map[string]string        // cluster id → the device key elected gateway
	clusterOf      map[string]string        // device key → the cluster it was last seen in
	clusterAccount map[string]string        // cluster id → the account it belongs to
	clusterText    map[string]string        // account → the last clusters pushed
	clusterPods    map[string][]PodInfo     // account → the pods as last computed (clusters.go)
	grace          map[string]*graceEntry   // device key → a gateway kept after an unclean drop
	updateHops     map[string]*updateHop    // an update request being carried, by the id of the hop
	// App is the front end of the pod: the relay serves its manifest and icons (static.go).
	App fs.FS
	// Push sends the Web Push notifications of the pods (push.go); nil: off.
	Push *Push
	// GitHub links the accounts to a GitHub login and holds the sponsor list, which lifts the
	// TURN quota (github.go); nil: off.
	GitHub *GitHub
	// Version is what the status page reports (the relay subcommand).
	Version string
	// startedAt is when this relay came up (status.go).
	startedAt time.Time

	// The WebSocket links in flight (shutdown.go): the HTTP server forgets a hijacked connection,
	// so Close finds them here.
	sockMu sync.Mutex
	socks  map[*websocket.Conn]bool
	live   sync.WaitGroup
}

// podConn is the pod's persistent link: register (pod id + secret), then carry the
// WebRTC signaling of all its sessions (answer, candidates, bye), tagged by session.
type podConn struct {
	id, name string
	account  string // the account the pod is paired with (empty: token mode)
	device   string // the device id the pod published its sync key under (empty: not published yet)
	// certified is the account when the pod registered with a certificate of it that the relay
	// checked, not revoked: the only account it may send push notifications to.
	certified string
	// The cluster presence (clusters.go): the device key the link proved possession of (empty: an old
	// pod in token mode, which gets no presence), what it reported about its LAN peers, when its
	// link came up, and whether it ended cleanly (a bye).
	key       string
	version   string
	build     string // the fingerprint of its front end (#73)
	protocol  int    // the multi-pod protocol the pod told (0: one older than #60, or none told)
	preferred bool
	renew     bool // it answers an offer on a live session: its tunnels may renew their credentials (#86)
	peers     []string
	clean     bool
	ws        *websocket.Conn
	mu        sync.Mutex
}

func (pc *podConn) send(m sigMsg) error {
	pc.mu.Lock()
	defer pc.mu.Unlock()
	b, _ := json.Marshal(m)
	return pc.ws.Write(context.Background(), websocket.MessageText, b)
}

// updateHop is an update request the relay is carrying: who asked, with which id of its own, and
// when. The relay is a plain carrier (relay.md, *Multi-pod*): it checks that both ends are pods of
// the same account and that the target is online; the pod the request names checks the signature.
// The id of the hop is the relay's, not the requester's, so another pod cannot answer a request it
// did not send.
type updateHop struct {
	pc  *podConn
	req string
	at  time.Time
	// fan is the wake this hop is one branch of (nil for a request to one pod).
	fan *wakeFan
}

// wakeFan is a wake asked of every other pod of the account: the first "sent" goes back, and when
// every pod refused, the refusal does.
type wakeFan struct {
	pending int
	done    bool
}

// openHop records a request carried for pc and returns the id of the hop. The caller holds s.mu.
func (s *Server) openHop(pc *podConn, req string, fan *wakeFan) string {
	hop := relayNonce()
	if s.updateHops == nil {
		s.updateHops = map[string]*updateHop{}
	}
	for id, h := range s.updateHops {
		if time.Since(h.at) > updateHopTTL {
			delete(s.updateHops, id)
		}
	}
	s.updateHops[hop] = &updateHop{pc: pc, req: req, at: time.Now(), fan: fan}
	return hop
}

// updateHopTTL is how long a carried request stays open: the pod answers within the wait of the one
// that asked (relayclient.UpdateWait), an answer after that reaches nothing.
const updateHopTTL = 30 * time.Second

// browserConn is one active browser signaling to a pod, in one session. It rides its own
// WebSocket (/_relay/signal), or the identity link of its page (#75): write and end are that
// link's.
type browserConn struct {
	podID   string
	session string
	mu      sync.Mutex
	write   func([]byte) error
	end     func() // a fresh session took its place: its own WebSocket is closed
}

func (bc *browserConn) send(m sigMsg) error {
	bc.mu.Lock()
	defer bc.mu.Unlock()
	b, _ := json.Marshal(m)
	return bc.write(b)
}

// openBrowser takes the session of a browser — a fresh one replaces the previous — and gives it
// its ICE servers, with fresh credentials.
func (s *Server) openBrowser(bc *browserConn) {
	s.mu.Lock()
	if prev := s.browsers[bc.session]; prev != nil && prev != bc && prev.end != nil {
		prev.end()
	}
	s.browsers[bc.session] = bc
	s.mu.Unlock()
	bc.send(s.iceMessage(bc.podID, bc.session))
}

// dropBrowser forgets a session, unless a fresh one took its place.
func (s *Server) dropBrowser(bc *browserConn) {
	s.mu.Lock()
	if cur := s.browsers[bc.session]; cur == bc {
		delete(s.browsers, bc.session)
	}
	s.mu.Unlock()
}

// browserSignal carries one message of a browser session to its pod, whichever link it came on.
func (s *Server) browserSignal(bc *browserConn, m sigMsg) {
	podID, session := bc.podID, bc.session
	switch m.Type {
	case "ice":
		// The credentials of a long session are about to end (#86): fresh ones, under the quota
		// as the first ones were. The browser restarts ICE with them; its offer brings the pod
		// fresh ones too.
		bc.send(s.iceMessage(podID, session))
	case "offer":
		// A pod the store still lists but whose link is gone (it re-dials) cannot answer:
		// the browser is told, rather than left waiting for an offer that was dropped.
		if !s.podOf(podID, func(pc *podConn) {
			pc.send(s.iceMessage(podID, session))
			pc.send(sigMsg{Type: "offer", Session: session, SDP: m.SDP})
		}) {
			bc.send(sigMsg{Type: "error", Session: session, Message: "the pod is not reachable"})
		}
	case "candidate":
		s.podOf(podID, func(pc *podConn) {
			pc.send(sigMsg{Type: "candidate", Session: session, Candidate: m.Candidate})
		})
	case "bye":
		s.podOf(podID, func(pc *podConn) { pc.send(sigMsg{Type: "bye", Session: session}) })
		s.dropBrowser(bc)
	}
}

// New builds a relay Server over the given store and TURN server.
func New(st *Store, turn *Turn) *Server {
	return &Server{
		store:          st,
		turn:           turn,
		pods:           map[string]*podConn{},
		browsers:       map[string]*browserConn{},
		links:          map[*identityConn]bool{},
		codes:          map[string]*identityConn{},
		joiners:        map[string]*identityConn{},
		since:          map[string]time.Time{},
		gateways:       map[string]string{},
		clusterOf:      map[string]string{},
		clusterAccount: map[string]string{},
		clusterText:    map[string]string{},
		clusterPods:    map[string][]PodInfo{},
		grace:          map[string]*graceEntry{},
		startedAt:      time.Now(),
	}
}

// credKey is whose TURN traffic a session of this pod counts under: the account, when the pod
// is paired (a browser dialing it belongs to the same account, and the quota of #29 is per
// account), the pod id otherwise.
func (s *Server) credKey(podID string) string {
	s.mu.Lock()
	pc := s.pods[podID]
	s.mu.Unlock()
	if pc != nil && pc.account != "" {
		return pc.account
	}
	return podID
}

// quotaState is the TURN month of a key, or nothing when this relay has no TURN server.
func (s *Server) quotaState(key string) *QuotaState {
	if s.turn == nil {
		return nil
	}
	return s.turn.QuotaState(key)
}

// iceMessage carries the ICE servers of a session and the credentials to use them. When the
// account is over its monthly quota, no credential is given and the quota travels with the
// URLs: the direct and STUN candidates still work, only the relayed path is closed, and the
// page can say why.
func (s *Server) iceMessage(podID, session string) sigMsg {
	key := s.credKey(podID)
	if s.turn == nil {
		return sigMsg{Type: "ice", Session: session}
	}
	user, pass, err := s.turn.Creds(podID, key, s.turn.TTL())
	m := sigMsg{Type: "ice", Session: session, URLs: s.turn.ICE(), Username: user, Credential: pass}
	// The ttl tells the tunnel to renew them: only with a pod that answers the new offer on its
	// live session (an older one would take it for a new tunnel and break the one in use).
	if exp, ok := credExpiry(user); ok && s.podRenews(podID) {
		m.TTL = exp - time.Now().Unix()
	}
	if err == errQuota {
		m.Quota = s.turn.QuotaState(key)
		m.Message = "quota"
	}
	return m
}

// podRenews reports whether the link of a pod said it answers an offer on a live session.
func (s *Server) podRenews(podID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	pc := s.pods[podID]
	return pc != nil && pc.renew
}

// wsReadLimit is the largest frame a link may send (#72). Signaling frames are a few hundred
// bytes; the largest thing a link ever carries is a sync blob, capped at 64 KiB of base64
// (syncstore.go). Without a limit one client can make the relay hold a frame of its choosing.
const wsReadLimit = 1 << 20

// servePod is the pod's persistent link: register (pod id + secret), then carry its
// WebRTC signaling, routed by session id.
func (s *Server) servePod(w http.ResponseWriter, r *http.Request) {
	ws, done, err := s.accept(w, r)
	if err != nil {
		return
	}
	defer done()
	ws.SetReadLimit(wsReadLimit)
	pc := &podConn{ws: ws}
	defer func() {
		s.mu.Lock()
		if s.pods[pc.id] == pc {
			delete(s.pods, pc.id)
		}
		for sid, bc := range s.browsers {
			if bc.podID == pc.id {
				// The window on the other side of that pod must know it is gone: it asks for
				// another gateway rather than waiting on a tunnel that will never answer.
				bc.send(sigMsg{Type: "bye", Session: sid})
				delete(s.browsers, sid)
			}
		}
		s.podLeaves(pc)
		s.mu.Unlock()
		s.store.Offline(pc.id)
		s.recomputeClusters(pc.account)
		_ = ws.CloseNow()
	}()
	for {
		_, b, err := ws.Read(context.Background())
		if err != nil {
			return
		}
		var m sigMsg
		if err := json.Unmarshal(b, &m); err != nil {
			continue
		}
		switch m.Type {
		case "register":
			if err := s.store.Register(m.ID, m.Secret, m.Name); err != nil {
				pc.send(sigMsg{Type: "error", Message: err.Error()})
				return
			}
			pc.id, pc.name, pc.account = m.ID, m.Name, m.Account
			pc.version, pc.build, pc.preferred, pc.protocol, pc.renew = m.Version, m.Build, m.Preferred, m.Protocol, m.Renew
			// A paired pod sends its certificate: it must verify against the account it
			// claims, which only a device holding the account key can sign.
			if m.Cert != nil {
				if err := s.store.StoreDevice(m.Cert); err != nil {
					pc.send(sigMsg{Type: "error", Message: err.Error()})
					return
				}
				list, err := s.store.Revocations(m.Account)
				if err != nil {
					pc.send(sigMsg{Type: "error", Message: err.Error()})
					return
				}
				if m.Cert.Account != m.Account || m.Cert.Kind != identity.KindPod || list.Has(m.Cert.Key) {
					pc.send(sigMsg{Type: "error", Message: identity.ErrForeignAccount.Error()})
					return
				}
				// A pod that announces the proof gets a nonce it must sign with the key its
				// certificate names: a copied certificate is refused. An old pod that does not
				// announce it stays in token mode, with no presence and no gateway role.
				if m.Prove {
					nonce := relayNonce()
					if err := pc.send(sigMsg{Type: "challenge", Nonce: nonce}); err != nil {
						return
					}
					if err := s.proofOfPossession(ws, r.Host, m.Cert, nonce, nil); err != nil {
						pc.send(sigMsg{Type: "error", Message: err.Error()})
						return
					}
					pc.key, pc.certified = m.Cert.Key, m.Account
				} else {
					pc.certified = m.Account
				}
			}
			s.mu.Lock()
			s.pods[m.ID] = pc
			if pc.key != "" {
				s.podArrives(pc)
			}
			s.mu.Unlock()
			s.store.Online(m.ID)
			s.store.TouchAccount(m.Account)
			list, err := s.store.Revocations(m.Account)
			if err != nil {
				pc.send(sigMsg{Type: "error", Message: err.Error()})
				return
			}
			pc.send(sigMsg{Type: "ready", Account: m.Account, List: list, Quota: s.quotaState(s.credKey(m.ID))})
			if pc.key != "" && !s.recomputeClusters(m.Account) {
				s.sendClusters(m.Account, nil, pc)
			}
		case "site": // "site" on the wire: the cluster (GLOSSARY.md)
			s.mu.Lock()
			s.setCluster(pc, m.Peers)
			s.mu.Unlock()
			s.recomputeClusters(pc.account)
		case "preferred":
			// Settings > Remote access: the user says which pod of the cluster opens the others.
			s.mu.Lock()
			pc.preferred = m.Preferred
			s.mu.Unlock()
			s.recomputeClusters(pc.account)
		case "bye":
			if m.Session == "" {
				// The pod is leaving on its own: a clean goodbye. Its gateway role moves at once
				// and its "since" is reset, unlike a link that dropped.
				pc.mu.Lock()
				pc.clean = true
				pc.mu.Unlock()
				return
			}
			// The pod closed one tunnel (its ICE failed, a refused handshake): the browser is
			// told, else a page whose path died without a word waits on it (#86, a renewal over
			// the quota leaves a TURN-only tunnel no path).
			s.mu.Lock()
			bc := s.browsers[m.Session]
			s.mu.Unlock()
			if bc != nil && bc.podID == pc.id {
				bc.send(sigMsg{Type: "bye", Session: m.Session})
				s.dropBrowser(bc)
			}
		case "revocations":
			// A pod holds a newer list than the relay (made offline by the pair command, or
			// received from a pod of the LAN): kept only when signed by the account of the pod
			// with a higher seq, then pushed to the other devices.
			if m.List == nil || pc.account == "" || m.List.Account != pc.account {
				continue
			}
			if kept, err := s.store.SaveRevocations(m.List); err == nil && kept {
				s.pushRevocations(pc.account, m.List, nil)
			}
		case "push.send":
			s.pushFromPod(pc, b)
		case "update":
			// Carried unchanged to the pod the request names, online on a link of the same account.
			// What the request is worth is decided by that pod, not here.
			if m.Update == nil || m.Device == "" || pc.certified == "" {
				continue
			}
			s.mu.Lock()
			var target *podConn
			for _, p := range s.pods {
				if p.key != "" && p.key == m.Device && p.certified == pc.certified {
					target = p
					break
				}
			}
			hop := ""
			if target != nil {
				hop = s.openHop(pc, m.Req, nil)
			}
			s.mu.Unlock()
			if target == nil {
				_ = pc.send(sigMsg{Type: "update.done", Req: m.Req, Status: "refused", Message: "the pod is not online at the relay"})
				continue
			}
			_ = target.send(sigMsg{Type: "update", Device: m.Device, Req: hop, Update: m.Update})
		case "update.done":
			s.mu.Lock()
			h := s.updateHops[m.Req]
			if h != nil {
				delete(s.updateHops, m.Req)
			}
			s.mu.Unlock()
			if h == nil || time.Since(h.at) > updateHopTTL {
				continue
			}
			_ = h.pc.send(sigMsg{Type: "update.done", Req: h.req, Status: m.Status, Message: m.Message, Command: m.Command})
		case "power":
			s.carryPower(pc, m)
		case "power.done":
			s.powerDone(m)
		case "answer":
			if bc := s.browserOf(m.Session); bc != nil {
				bc.send(sigMsg{Type: "answer", Session: m.Session, SDP: m.SDP})
			}
		case "candidate":
			if bc := s.browserOf(m.Session); bc != nil {
				bc.send(sigMsg{Type: "candidate", Session: m.Session, Candidate: m.Candidate})
			}
		case "error":
			// The pod could not answer the offer: the browser is told, rather than left waiting.
			if bc := s.browserOf(m.Session); bc != nil {
				bc.send(sigMsg{Type: "error", Session: m.Session, Message: m.Message})
			}
		case "syncKey", "wrapGet", "blobGet", "blobPut":
			// The sync messages of a paired pod. The relay stores ciphertext only: it checks the
			// signature against the device that wrote it, it cannot read a value.
			if pc.account == "" {
				pc.send(sigMsg{Type: "error", Message: "the pod is not paired"})
				continue
			}
			s.handlePodSync(pc, m)
		}
	}
}

// serveSignal is a browser's signaling link to a pod (query "pod" = the pod id), in one
// session: it carries the offer, candidates and bye, and receives the answer,
// candidates, ice and bye from the pod, all tagged with the session.
func (s *Server) serveSignal(w http.ResponseWriter, r *http.Request) {
	podID := r.URL.Query().Get("pod")
	session := r.URL.Query().Get("session")
	if !s.store.OnlinePod(podID) || session == "" {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":"no such session"}`))
		return
	}
	ws, done, err := s.accept(w, r)
	if err != nil {
		return
	}
	defer done()
	ws.SetReadLimit(wsReadLimit)
	bc := &browserConn{podID: podID, session: session,
		write: func(b []byte) error { return ws.Write(context.Background(), websocket.MessageText, b) },
		end:   func() { _ = ws.CloseNow() }}
	defer func() {
		s.dropBrowser(bc)
		_ = ws.CloseNow()
	}()
	// Fresh credentials, then the offer to the pod.
	s.openBrowser(bc)
	for {
		_, b, err := ws.Read(context.Background())
		if err != nil {
			return
		}
		var m sigMsg
		if err := json.Unmarshal(b, &m); err != nil {
			continue
		}
		s.browserSignal(bc, m)
	}
}

// podOf runs fn on the live link of a pod, and reports whether there was one.
func (s *Server) podOf(podID string, fn func(*podConn)) bool {
	s.mu.Lock()
	pc := s.pods[podID]
	s.mu.Unlock()
	if pc == nil {
		return false
	}
	fn(pc)
	return true
}

func (s *Server) browserOf(session string) *browserConn {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.browsers[session]
}
