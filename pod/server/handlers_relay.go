package server

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/coder/websocket"

	"antennae.space/ide/lib/i18n"
	"antennae.space/ide/lib/identity"
)

// RelayInfo is the state of the remote access, shown in Settings > Remote access.
type RelayInfo struct {
	// URL of the relay the pod registers with (empty: no relay).
	URL string `json:"url"`
	// DefaultURL is the hosted relay the page pre-fills when the pod has none.
	DefaultURL string `json:"defaultUrl"`
	PodID      string `json:"podId"`
	// Link the user opens from another network, with its token.
	Link string `json:"link"`
	// State of the link to the relay: off, connecting or connected.
	State string `json:"state"`
	// The monthly TURN quota the relay applies to this account (milestone 5): what it moved this
	// month, the limit (0: none, a self-hosted relay), whether the account is freed from it, and
	// whether the relayed path is closed. 0/0/false when the relay says nothing. The level of the
	// account (#87) is the badge of the page: its name, its end, the level of the sponsorship
	// alone and the VIP list.
	TurnUsed      int64  `json:"turnUsed"`
	TurnLimit     int64  `json:"turnLimit"`
	TurnSupporter bool   `json:"turnSupporter,omitempty"`
	TurnOver      bool   `json:"turnOver,omitempty"`
	TurnLevel     string `json:"turnLevel,omitempty"`
	TurnUntil     int64  `json:"turnUntil,omitempty"`
	TurnSponsor   string `json:"turnSponsor,omitempty"`
	TurnVIP       bool   `json:"turnVip,omitempty"`
	TurnTeam      string `json:"turnTeam,omitempty"`
}

func (s *Server) registerRelay() {
	s.handle("relay.status", func(ctx context.Context, c *Client, p json.RawMessage) (any, error) {
		if s.RelayStatus == nil {
			return nil, i18n.New("this pod has no relay")
		}
		return s.RelayStatus(), nil
	})
	// Settings > Remote access turns the link on or off: the URL of the relay, empty for off.
	s.handle("relay.set", func(ctx context.Context, c *Client, p json.RawMessage) (any, error) {
		if s.RelaySet == nil {
			return nil, i18n.New("this pod cannot change its relay")
		}
		var a struct {
			URL string `json:"url"`
		}
		if err := json.Unmarshal(p, &a); err != nil {
			return nil, err
		}
		return s.RelaySet(a.URL)
	})
}

// The identity module and the BIP39 wordlist are served here too, under the same paths the
// relay uses. The app runs on the pod origin (localhost or the tailnet), and it imports the
// same file the bootstrap page loads from the relay: one copy, so the browser and the pod
// cannot disagree on the bytes a signature covers.
func (s *Server) serveIdentityModule(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	_, _ = w.Write(identity.ModuleJS())
}

func (s *Server) serveWordlist(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write(identity.WordlistBytes())
}

// serveIdentityLink proxies the pairing and admin link of the relay: the app on the pod origin
// reaches it through the pod, as it reaches everything else. The pod only carries the frames,
// it cannot approve a pairing or sign a revocation list — it does not hold the account key.
func (s *Server) serveIdentityLink(w http.ResponseWriter, r *http.Request) {
	// Without a relay, the pod answers the panel itself from what it knows on the LAN.
	if s.RelayURL == "" && s.LAN != nil {
		s.LAN.ServeIdentity(w, r)
		return
	}
	if s.RelayURL == "" {
		http.Error(w, i18n.T("en", "this pod has no relay"), http.StatusNotFound)
		return
	}
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	up, _, err := websocket.Dial(ctx, s.RelayURL+"/_relay/identity", nil)
	if err != nil {
		http.Error(w, i18n.T("en", "the relay is not reachable"), http.StatusBadGateway)
		return
	}
	defer up.CloseNow()
	conn, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	defer conn.CloseNow()
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			_, b, err := up.Read(ctx)
			if err != nil {
				return
			}
			if err := conn.Write(ctx, websocket.MessageText, b); err != nil {
				return
			}
		}
	}()
	for {
		_, b, err := conn.Read(ctx)
		if err != nil {
			break
		}
		if err := up.Write(ctx, websocket.MessageText, b); err != nil {
			break
		}
	}
	<-done
}
