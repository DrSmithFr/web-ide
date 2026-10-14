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

// turnTTL is the validity of a short-lived TURN credential: long enough for a whole
// session (Pion re-checks the expiry on every Refresh, a short one would kill a
// relayed tunnel mid-way), short enough that a leaked one dies soon.
const turnTTL = 12 * time.Hour

// sigMsg is one signaling message between the relay and a client (pod or browser).
// Every message carries the session id: a pod may serve several browsers at once, each
// in its own session.
type sigMsg struct {
	Type       string                `json:"type"`
	Session    string                `json:"session,omitempty"`
	URLs       []string              `json:"urls,omitempty"`
	Username   string                `json:"username,omitempty"`
	Credential string                `json:"credential,omitempty"`
	SDP        string                `json:"sdp,omitempty"`
	Candidate  json.RawMessage       `json:"candidate,omitempty"`
	ID         string                `json:"id,omitempty"`
	Secret     string                `json:"secret,omitempty"`
	Name       string                `json:"name,omitempty"`
	Account    string                `json:"account,omitempty"`
	Device     string                `json:"device,omitempty"`
	Cert       *identity.Cert        `json:"cert,omitempty"`
	List       *identity.Revocations `json:"list,omitempty"` // pushed to the pods of an account
	// Sync (milestone 4): the pod publishes its X25519 key, reads the wrap of the epoch key made
	// for it, and exchanges encrypted blobs. The relay cannot read a blob, it only checks the
	// signature against the device that wrote it.
	SyncKey *identity.SyncKeyRecord `json:"syncKey,omitempty"`
	Wrap    *identity.WrapRecord    `json:"wrap,omitempty"`
	Blob    *identity.BlobRecord    `json:"blob,omitempty"`
	Kind    string                  `json:"kind,omitempty"`
	Updated int64                   `json:"updated,omitempty"` // time the stored blob or wrap was written
	Message string                  `json:"message,omitempty"`
	// Sites (milestone 3): register carries the pod version, its multi-pod protocol and the
	// preferred switch, `site` the device keys of its LAN peers, `challenge`/`proof` the nonce of
	// the proof of possession, and `pods`/`gateway` what the relay sees of the account.
	Prove     bool      `json:"prove,omitempty"`
	Nonce     string    `json:"nonce,omitempty"`
	Sig       string    `json:"sig,omitempty"`
	Version   string    `json:"version,omitempty"`
	Protocol  int       `json:"protocol,omitempty"`
	Preferred bool      `json:"preferred,omitempty"`
	Peers     []string  `json:"peers,omitempty"`
	Site      string    `json:"site,omitempty"`
	Gateway   string    `json:"gateway,omitempty"`
	Pods      []PodInfo `json:"pods,omitempty"`
	// Update this pod (#60): the relay is a plain carrier of a signed update request between two
	// pods of the same account. req is the id of the hop the relay handed out, echoed on the answer.
	Update  *identity.UpdateRequest `json:"update,omitempty"`
	Req     string                  `json:"req,omitempty"`
	Status  string                  `json:"status,omitempty"`
	Command string                  `json:"command,omitempty"`
}

// Server routes signaling between the pods (one persistent link each) and the browsers
// that open links to them, by session id, and serves the bootstrap page, the Service
// Worker and the embedded TURN/STUN. The pod token never crosses it: the token is
// proved inside the DataChannel, bound to both DTLS fingerprints, so the relay cannot
// MITM.
type Server struct {
	store       *Store
	turn        *Turn
	mu          sync.Mutex
	pods        map[string]*podConn      // online pods, by pod id
	browsers    map[string]*browserConn  // active browsers, by session id
	links       map[*identityConn]bool   // live pairing/admin links
	codes       map[string]*identityConn // pairing code → the link that created it
	joiners     map[string]*identityConn // pairing code → the link that posted its key to it
	since       map[string]time.Time     // device key → when its link came up (sites.go)
	gateways    map[string]string        // site id → the device key elected gateway
	siteOf      map[string]string        // device key → the site it was last seen in
	siteAccount map[string]string        // site id → the account it belongs to
	siteText    map[string]string        // account → the last sites pushed
	sitePods    map[string][]PodInfo     // account → the pods as last computed (sites.go)
	grace       map[string]*graceEntry   // device key → a gateway kept after an unclean drop
	updateHops  map[string]*updateHop    // an update request being carried, by the id of the hop
	// App is the front end of the pod: the relay serves its manifest and icons (static.go).
	App fs.FS
	// Push sends the Web Push notifications of the pods (push.go); nil: off.
	Push *Push
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
	// The site presence (sites.go): the device key the link proved possession of (empty: an old
	// pod in token mode, which gets no presence), what it reported about its LAN peers, when its
	// link came up, and whether it ended cleanly (a bye).
	key       string
	version   string
	protocol  int // the multi-pod protocol the pod told (0: one older than #60, or none told)
	preferred bool
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
}

// updateHopTTL is how long a carried request stays open: the pod answers within the wait of the one
// that asked (relayclient.UpdateWait), an answer after that reaches nothing.
const updateHopTTL = 30 * time.Second

// browserConn is one active browser signaling to a pod, in one session.
type browserConn struct {
	podID   string
	session string
	ws      *websocket.Conn
	mu      sync.Mutex
}

func (bc *browserConn) send(m sigMsg) error {
	bc.mu.Lock()
	defer bc.mu.Unlock()
	b, _ := json.Marshal(m)
	return bc.ws.Write(context.Background(), websocket.MessageText, b)
}

// New builds a relay Server over the given store and TURN server.
func New(st *Store, turn *Turn) *Server {
	return &Server{
		store:       st,
		turn:        turn,
		pods:        map[string]*podConn{},
		browsers:    map[string]*browserConn{},
		links:       map[*identityConn]bool{},
		codes:       map[string]*identityConn{},
		joiners:     map[string]*identityConn{},
		since:       map[string]time.Time{},
		gateways:    map[string]string{},
		siteOf:      map[string]string{},
		siteAccount: map[string]string{},
		siteText:    map[string]string{},
		sitePods:    map[string][]PodInfo{},
		grace:       map[string]*graceEntry{},
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

func (s *Server) iceMessage(podID, session string) sigMsg {
	user, pass, _ := s.turn.Creds(podID, s.credKey(podID), turnTTL)
	return sigMsg{Type: "ice", Session: session, URLs: s.turn.ICE(), Username: user, Credential: pass}
}

// servePod is the pod's persistent link: register (pod id + secret), then carry its
// WebRTC signaling, routed by session id.
func (s *Server) servePod(w http.ResponseWriter, r *http.Request) {
	ws, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
	if err != nil {
		return
	}
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
		s.recomputeSites(pc.account)
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
			pc.version, pc.preferred, pc.protocol = m.Version, m.Preferred, m.Protocol
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
					if err := s.proofOfPossession(ws, r.Host, m.Cert, nonce); err != nil {
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
			list, err := s.store.Revocations(m.Account)
			if err != nil {
				pc.send(sigMsg{Type: "error", Message: err.Error()})
				return
			}
			pc.send(sigMsg{Type: "ready", Account: m.Account, List: list})
			if pc.key != "" && !s.recomputeSites(m.Account) {
				s.sendSites(m.Account, nil, pc)
			}
		case "site":
			s.mu.Lock()
			s.setSite(pc, m.Peers)
			s.mu.Unlock()
			s.recomputeSites(pc.account)
		case "preferred":
			// Settings > Remote access: the user says which pod of the site opens the others.
			s.mu.Lock()
			pc.preferred = m.Preferred
			s.mu.Unlock()
			s.recomputeSites(pc.account)
		case "bye":
			if m.Session == "" {
				// The pod is leaving on its own: a clean goodbye. Its gateway role moves at once
				// and its "since" is reset, unlike a link that dropped.
				pc.mu.Lock()
				pc.clean = true
				pc.mu.Unlock()
				return
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
				hop = relayNonce()
				if s.updateHops == nil {
					s.updateHops = map[string]*updateHop{}
				}
				for id, h := range s.updateHops {
					if time.Since(h.at) > updateHopTTL {
						delete(s.updateHops, id)
					}
				}
				s.updateHops[hop] = &updateHop{pc: pc, req: m.Req, at: time.Now()}
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
	ws, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
	if err != nil {
		return
	}
	bc := &browserConn{podID: podID, session: session, ws: ws}
	defer func() {
		s.mu.Lock()
		if cur := s.browsers[session]; cur == bc {
			delete(s.browsers, session)
		}
		s.mu.Unlock()
		_ = ws.CloseNow()
	}()
	// A fresh session replaces the previous one on this socket: close it.
	s.mu.Lock()
	if prev := s.browsers[session]; prev != nil && prev != bc {
		_ = prev.ws.CloseNow()
	}
	s.browsers[session] = bc
	s.mu.Unlock()
	// Fresh credentials, then the offer to the pod.
	bc.send(s.iceMessage(podID, session))
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
		case "offer":
			s.podOf(podID, func(pc *podConn) {
				pc.send(s.iceMessage(podID, session))
				pc.send(sigMsg{Type: "offer", Session: session, SDP: m.SDP})
			})
		case "candidate":
			s.podOf(podID, func(pc *podConn) {
				pc.send(sigMsg{Type: "candidate", Session: session, Candidate: m.Candidate})
			})
		case "bye":
			s.podOf(podID, func(pc *podConn) { pc.send(sigMsg{Type: "bye", Session: session}) })
			s.mu.Lock()
			if cur := s.browsers[session]; cur == bc {
				delete(s.browsers, session)
			}
			s.mu.Unlock()
		}
	}
}

func (s *Server) podOf(podID string, fn func(*podConn)) {
	s.mu.Lock()
	pc := s.pods[podID]
	s.mu.Unlock()
	if pc != nil {
		fn(pc)
	}
}

func (s *Server) browserOf(session string) *browserConn {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.browsers[session]
}
