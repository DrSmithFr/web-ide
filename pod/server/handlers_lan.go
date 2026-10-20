package server

import (
	"context"
	"encoding/json"
	"net/http"
	"regexp"

	"github.com/DrSmithFr/web-ide/lib/i18n"
)

// LAN is the LAN link of the pod (pod/lan), seen from the page: the pods of the
// account found on the local network, and the addresses typed by hand.
type LAN interface {
	// View is the state shown in Settings > Remote access and on the home page.
	View() any
	SetManual(addrs []string) error
	// ServeIdentity answers the Members panel when the pod has no relay.
	ServeIdentity(w http.ResponseWriter, r *http.Request)
	// ServeGateway serves the page of a peer under peerPrefix. base is the path the peer's page
	// runs under for the browser ("" : peerPrefix + the device key, the plain gateway origin).
	ServeGateway(w http.ResponseWriter, r *http.Request, base string)
}

// peerPrefix is the path of the pages of the peers on this pod (lan.PeerPrefix).
const peerPrefix = "/_lan/pod/"

// baseKey holds, in the context of a request of a peer, the path its page runs under on
// the gateway pod (lan.BaseHeader).
type baseKey struct{}

var basePattern = regexp.MustCompile(`^(/_lan/pod|/pod)/[A-Za-z0-9_-]+$`)

// baseOf is the path a page runs under on the origin the browser used, taken from the context a
// trusted request carries (ServeTrusted, serveHTTPChannel).
func baseOf(ctx context.Context) string {
	base, _ := ctx.Value(baseKey{}).(string)
	return base
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
// as a request of this machine, like a window that came through the tunnel. Its page runs
// under the path the gateway tells.
func (s *Server) ServeTrusted(w http.ResponseWriter, r *http.Request) {
	r.Header.Set("Cookie", cookieName+"="+s.Token)
	r.RemoteAddr = "127.0.0.1:0"
	if base := r.Header.Get("X-Webide-Base"); basePattern.MatchString(base) {
		r = r.WithContext(context.WithValue(r.Context(), baseKey{}, base))
	}
	s.ServeHTTP(w, r)
}
