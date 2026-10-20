package relay

import (
	"net/http"

	"github.com/coder/websocket"
)

// accept upgrades a link and registers it: the handler that owns it calls done when it returns,
// after its own clean-up (the store writes of a link that ends). A WebSocket is hijacked, so the
// HTTP server neither waits for its handler nor closes it: Close does.
func (s *Server) accept(w http.ResponseWriter, r *http.Request) (*websocket.Conn, func(), error) {
	s.live.Add(1)
	ws, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
	if err != nil {
		s.live.Done()
		return nil, nil, err
	}
	s.sockMu.Lock()
	if s.socks == nil {
		s.socks = map[*websocket.Conn]bool{}
	}
	s.socks[ws] = true
	s.sockMu.Unlock()
	return ws, func() {
		s.sockMu.Lock()
		delete(s.socks, ws)
		s.sockMu.Unlock()
		s.live.Done()
	}, nil
}

// Close ends the links still open and waits for their handlers to finish, so that nothing
// writes to the store once it has returned. Stop the HTTP server first: no new link comes in.
func (s *Server) Close() {
	s.sockMu.Lock()
	for ws := range s.socks {
		_ = ws.CloseNow()
	}
	s.sockMu.Unlock()
	s.live.Wait()
}
