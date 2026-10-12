package server

import (
	"context"
	"encoding/json"

	"github.com/DrSmithFr/web-ide/pod/internal/i18n"
)

// Settings sync: what the page shows in Settings > Remote access > Sync — the state of each kind,
// the conflicts it must resolve, and the vault switch. The pods do the merge; the relay only stores
// ciphertext, so a conflict is answered by the pod that owns the setting.
func (s *Server) registerSync() {
	// The hub is built after the handlers, in main: check it at call time, like the LAN handlers.
	s.handle("sync.status", func(ctx context.Context, c *Client, p json.RawMessage) (any, error) {
		if s.Sync == nil {
			return nil, i18n.New("the pod is not paired")
		}
		return s.Sync.Status(), nil
	})

	// The choice the user made for a conflict: a normal write, recorded in the history like any
	// other change, and sent to the other pods.
	s.handle("sync.resolve", func(ctx context.Context, c *Client, p json.RawMessage) (any, error) {
		a, err := bind[struct {
			Kind  string
			Path  string
			Value json.RawMessage
		}](p)
		if err != nil {
			return nil, err
		}
		if s.Sync == nil {
			return nil, i18n.New("the pod is not paired")
		}
		src := s.Sync.Source(a.Kind)
		if src == nil {
			return nil, i18n.Errorf("nothing synced under %q", a.Kind)
		}
		if err := src.Resolve(a.Path, a.Value); err != nil {
			return nil, err
		}
		return src.Status(), nil
	})

	// A sync switch (settings or vault). Switching one off deletes its blob: the pod stops sharing
	// what it holds, and the other pods keep what they already read.
	s.handle("sync.switch", func(ctx context.Context, c *Client, p json.RawMessage) (any, error) {
		a, err := bind[struct {
			Kind string
			On   bool
		}](p)
		if err != nil {
			return nil, err
		}
		if s.Sync == nil {
			return nil, i18n.New("the pod is not paired")
		}
		if err := s.Sync.Flags().Set(a.Kind, a.On); err != nil {
			return nil, err
		}
		src := s.Sync.Source(a.Kind)
		if src == nil {
			return nil, i18n.Errorf("nothing synced under %q", a.Kind)
		}
		if !a.On {
			if err := src.Delete(); err != nil {
				return nil, err
			}
		} else if err := src.Push(); err != nil {
			return nil, err
		}
		return src.Status(), nil
	})
}
