package server

import (
	"context"
	"encoding/json"

	"github.com/DrSmithFr/web-ide/pod/internal/i18n"
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
