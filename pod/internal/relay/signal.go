package relay

import (
	"context"
	"encoding/json"
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
}

// Server routes signaling between the pods (one persistent link each) and the browsers
// that open links to them, by session id, and serves the bootstrap page, the Service
// Worker and the embedded TURN/STUN. The pod token never crosses it: the token is
// proved inside the DataChannel, bound to both DTLS fingerprints, so the relay cannot
// MITM.
type Server struct {
	store    *Store
	turn     *Turn
	mu       sync.Mutex
	pods     map[string]*podConn      // online pods, by pod id
	browsers map[string]*browserConn  // active browsers, by session id
	links    map[*identityConn]bool   // live pairing/admin links
	codes    map[string]*identityConn // pairing code → the link that created it
	joiners  map[string]*identityConn // pairing code → the link that posted its key to it
}

// podConn is the pod's persistent link: register (pod id + secret), then carry the
// WebRTC signaling of all its sessions (answer, candidates, bye), tagged by session.
type podConn struct {
	id, name string
	account  string // the account the pod is paired with (empty: token mode)
	device   string // the device id the pod published its sync key under (empty: not published yet)
	ws       *websocket.Conn
	mu       sync.Mutex
}

func (pc *podConn) send(m sigMsg) error {
	pc.mu.Lock()
	defer pc.mu.Unlock()
	b, _ := json.Marshal(m)
	return pc.ws.Write(context.Background(), websocket.MessageText, b)
}

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
		store:    st,
		turn:     turn,
		pods:     map[string]*podConn{},
		browsers: map[string]*browserConn{},
		links:    map[*identityConn]bool{},
		codes:    map[string]*identityConn{},
		joiners:  map[string]*identityConn{},
	}
}

func (s *Server) iceMessage(podID, session string) sigMsg {
	user, pass, _ := s.turn.Creds(podID, turnTTL)
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
				delete(s.browsers, sid)
			}
		}
		s.mu.Unlock()
		s.store.Offline(pc.id)
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
			// A paired pod sends its certificate: it must verify against the account it
			// claims, which only a device holding the account key can sign.
			if m.Cert != nil {
				if err := s.store.StoreDevice(m.Cert); err != nil {
					pc.send(sigMsg{Type: "error", Message: err.Error()})
					return
				}
			}
			s.mu.Lock()
			s.pods[m.ID] = pc
			s.mu.Unlock()
			s.store.Online(m.ID)
			list, err := s.store.Revocations(m.Account)
			if err != nil {
				pc.send(sigMsg{Type: "error", Message: err.Error()})
				return
			}
			pc.send(sigMsg{Type: "ready", Account: m.Account, List: list})
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
		case "answer":
			if bc := s.browserOf(m.Session); bc != nil {
				bc.send(sigMsg{Type: "answer", Session: m.Session, SDP: m.SDP})
			}
		case "candidate":
			if bc := s.browserOf(m.Session); bc != nil {
				bc.send(sigMsg{Type: "candidate", Session: m.Session, Candidate: m.Candidate})
			}
		case "bye":
			if bc := s.browserOf(m.Session); bc != nil {
				bc.send(sigMsg{Type: "bye", Session: m.Session})
			}
			s.mu.Lock()
			delete(s.browsers, m.Session)
			s.mu.Unlock()
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
