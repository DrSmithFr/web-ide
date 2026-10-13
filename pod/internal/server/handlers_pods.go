package server

import (
	"context"
	"encoding/json"

	"github.com/DrSmithFr/web-ide/pod/internal/i18n"
)

// Pods is the view of the other pods of the account (pod/internal/multipod): the home page shows
// one Projects block per pod, and Settings > Remote access says which pod of the site opens the
// others.
type Pods interface {
	// List is the merged view: the pods of the LAN, the pods the relay reports, and the pods whose
	// encrypted list the relay cached.
	List() any
	// Preferred is the switch the user set for this pod, and SetPreferred tells it to the relay.
	Preferred() bool
	SetPreferred(on bool) error
	// Gateway is the pod of the account the browser should open first ("" when the relay says
	// nothing), and Site the site this pod belongs to.
	Gateway() string
	Site() string
}

func (s *Server) registerPods() {
	s.handle("pods.list", func(ctx context.Context, c *Client, p json.RawMessage) (any, error) {
		if s.Pods == nil {
			return nil, i18n.New("the pod is not paired")
		}
		return s.Pods.List(), nil
	})
	s.handle("pods.self", func(ctx context.Context, c *Client, p json.RawMessage) (any, error) {
		if s.Pods == nil {
			return nil, i18n.New("the pod is not paired")
		}
		return map[string]any{"site": s.Pods.Site(), "gateway": s.Pods.Gateway(), "preferred": s.Pods.Preferred()}, nil
	})
	s.handle("pods.preferred", func(ctx context.Context, c *Client, p json.RawMessage) (any, error) {
		a, err := bind[struct{ On bool }](p)
		if err != nil {
			return nil, err
		}
		if s.Pods == nil {
			return nil, i18n.New("the pod is not paired")
		}
		if err := s.Pods.SetPreferred(a.On); err != nil {
			return nil, err
		}
		return map[string]any{"preferred": s.Pods.Preferred()}, nil
	})
}

// PodsChanged tells the windows that the pods of the account changed.
func (s *Server) PodsChanged() {
	if s.Pods != nil {
		s.broadcast("pods.changed", s.Pods.List(), nil)
	}
}
