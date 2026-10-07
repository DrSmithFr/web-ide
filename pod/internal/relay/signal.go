package relay

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"github.com/coder/websocket"
)

// sigMsg is one signaling message between the relay and a client (pod or browser).
type sigMsg struct {
	Type       string          `json:"type"`
	URLs       []string        `json:"urls,omitempty"`
	Username   string          `json:"username,omitempty"`
	Credential string          `json:"credential,omitempty"`
	SDP        string          `json:"sdp,omitempty"`
	Candidate  json.RawMessage `json:"candidate,omitempty"`
	ID         string          `json:"id,omitempty"`
	Secret     string          `json:"secret,omitempty"`
	Name       string          `json:"name,omitempty"`
	Message    string          `json:"message,omitempty"`
}

// Server routes signaling between the pod (one persistent link) and the browsers that
// open a link to it, and serves the bootstrap page, the Service Worker and the
// embedded TURN/STUN. The pod token never crosses it: the token is proved inside the
// DataChannel, bound to both DTLS fingerprints, so the relay cannot MITM.
type Server struct {
	store  *Store
	turn   *Turn
	mu     sync.Mutex
	pods   map[string]*podConn
	browsers map[string]*browserConn
}

// podConn is the pod's persistent link.
type podConn struct {
	id, name string
	ws       *websocket.Conn
	mu       sync.Mutex
}

func (pc *podConn) send(m sigMsg) error {
	pc.mu.Lock()
	defer pc.mu.Unlock()
	b, _ := json.Marshal(m)
	return pc.ws.Write(context.Background(), websocket.MessageText, b)
}

// browserConn is one active browser signaling to a pod.
type browserConn struct {
	podID string
	ws    *websocket.Conn
	mu    sync.Mutex
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
	}
}

func (s *Server) iceMessage(podID string) sigMsg {
	user, pass, _ := s.turn.Creds(podID, 5*time.Minute)
	return sigMsg{Type: "ice", URLs: s.turn.ICE(), Username: user, Credential: pass}
}

// servePod is the pod's persistent link: register (id + secret), then carry its
// WebRTC signaling (answer, candidates, bye).
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
		delete(s.browsers, pc.id)
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
			pc.id, pc.name = m.ID, m.Name
			s.mu.Lock()
			s.pods[m.ID] = pc
			s.mu.Unlock()
			s.store.Online(m.ID)
			pc.send(sigMsg{Type: "ready"})
		case "answer":
			if bc := s.browserOf(pc.id); bc != nil {
				bc.send(sigMsg{Type: "answer", SDP: m.SDP})
			}
		case "candidate":
			if bc := s.browserOf(pc.id); bc != nil {
				bc.send(sigMsg{Type: "candidate", Candidate: m.Candidate})
			}
		case "bye":
			if bc := s.browserOf(pc.id); bc != nil {
				bc.send(sigMsg{Type: "bye"})
			}
			s.mu.Lock()
			delete(s.browsers, pc.id)
			s.mu.Unlock()
		}
	}
}

// serveSignal is a browser's signaling link to a pod (query "pod" = the pod id).
func (s *Server) serveSignal(w http.ResponseWriter, r *http.Request) {
	podID := r.URL.Query().Get("pod")
	if !s.store.OnlinePod(podID) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":"this pod is not online"}`))
		return
	}
	ws, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
	if err != nil {
		return
	}
	bc := &browserConn{podID: podID, ws: ws}
	defer func() {
		s.mu.Lock()
		if s.browsers[podID] == bc {
			delete(s.browsers, podID)
		}
		s.mu.Unlock()
		_ = ws.CloseNow()
	}()
	// Fresh credentials, then the offer to the pod.
	bc.send(s.iceMessage(podID))
	s.mu.Lock()
	s.browsers[podID] = bc
	s.mu.Unlock()
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
				pc.send(s.iceMessage(podID))
				pc.send(sigMsg{Type: "offer", SDP: m.SDP})
			})
		case "candidate":
			s.podOf(podID, func(pc *podConn) {
				pc.send(sigMsg{Type: "candidate", Candidate: m.Candidate})
			})
		case "bye":
			s.podOf(podID, func(pc *podConn) { pc.send(sigMsg{Type: "bye"}) })
			s.mu.Lock()
			if s.browsers[podID] == bc {
				delete(s.browsers, podID)
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

func (s *Server) browserOf(podID string) *browserConn {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.browsers[podID]
}
