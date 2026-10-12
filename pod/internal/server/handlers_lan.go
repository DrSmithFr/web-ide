package server

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/DrSmithFr/web-ide/pod/internal/i18n"
)

// LAN is the LAN link of the pod (pod/internal/lan), seen from the page: the pods of the
// account found on the local network, and the addresses typed by hand.
type LAN interface {
	// View is the state shown in Settings > Remote access and on the home page.
	View() any
	SetManual(addrs []string) error
	// ServeIdentity answers the Devices panel when the pod has no relay.
	ServeIdentity(w http.ResponseWriter, r *http.Request)
}

func (s *Server) registerLAN() {
	s.handle("lan.status", func(ctx context.Context, c *Client, p json.RawMessage) (any, error) {
		if s.LAN == nil {
			return nil, i18n.New("the LAN link is off")
		}
		return s.LAN.View(), nil
	})
	s.handle("lan.setPeers", func(ctx context.Context, c *Client, p json.RawMessage) (any, error) {
		var req struct {
			Peers []string `json:"peers"`
		}
		if err := json.Unmarshal(p, &req); err != nil {
			return nil, err
		}
		if s.LAN == nil {
			return nil, i18n.New("the LAN link is off")
		}
		if err := s.LAN.SetManual(req.Peers); err != nil {
			return nil, err
		}
		return s.LAN.View(), nil
	})
}

// LANChanged tells the windows that the pods of the LAN changed.
func (s *Server) LANChanged() {
	if s.LAN != nil {
		s.broadcast("lan.changed", s.LAN.View(), nil)
	}
}

// ServeTrusted serves a request of a pod of the account that proved who it is on the LAN:
// as a request of this machine, like a window that came through the tunnel.
func (s *Server) ServeTrusted(w http.ResponseWriter, r *http.Request) {
	r.Header.Set("Cookie", cookieName+"="+s.Token)
	r.RemoteAddr = "127.0.0.1:0"
	s.ServeHTTP(w, r)
}

// LANAllowed checks a request on a gateway port of the LAN as the pod checks its own page.
func (s *Server) LANAllowed(r *http.Request) bool {
	return (s.AllowRemote || loopback(r)) && s.authorized(r)
}
