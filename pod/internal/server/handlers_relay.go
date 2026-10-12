package server

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/coder/websocket"

	"github.com/DrSmithFr/web-ide/pod/internal/i18n"
	"github.com/DrSmithFr/web-ide/pod/internal/identity"
)

// RelayInfo is the state of the remote access, shown in Settings > Remote access.
type RelayInfo struct {
	// URL of the relay the pod registers with (empty: no relay).
	URL   string `json:"url"`
	PodID string `json:"podId"`
	// Link the user opens from another network, with its token.
	Link string `json:"link"`
	// State of the link to the relay: off, connecting or connected.
	State string `json:"state"`
}

func (s *Server) registerRelay() {
	s.handle("relay.status", func(ctx context.Context, c *Client, p json.RawMessage) (any, error) {
		if s.RelayStatus == nil {
			return nil, i18n.New("this pod has no relay")
		}
		return s.RelayStatus(), nil
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
