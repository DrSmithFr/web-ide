package server

import (
	"context"
	"encoding/json"

	"github.com/DrSmithFr/web-ide/pod/internal/i18n"
	"github.com/DrSmithFr/web-ide/pod/internal/identity"
)

// Pods is the view of the other pods of the account (pod/internal/multipod): the home page shows
// one Projects block per pod, and Settings > Remote access says which pod of the cluster opens the
// others.
type Pods interface {
	// List is the merged view: the pods of the LAN, the pods the relay reports, and the pods whose
	// encrypted list the relay cached.
	List() any
	// Preferred is the switch the user set for this pod, and SetPreferred tells it to the relay.
	Preferred() bool
	SetPreferred(on bool) error
	// Gateway is the pod of the account the browser should open first ("" when the relay says
	// nothing), and Cluster the cluster this pod belongs to.
	Gateway() string
	Cluster() string
	// Own is the device key of this pod: the home page lists it among the pods.
	Own() string
	// Update carries a signed update request to the pod it names and answers what that pod said
	// (running, not_service, refused).
	Update(ctx context.Context, req *identity.UpdateRequest) (any, error)
	// CanUpdate says whether this pod runs a release version: the only case where it may offer
	// another pod to install it.
	CanUpdate() bool
	// Power carries a signed power request (wake, shutdown, reboot, restart) and answers what the
	// pod that acted on it said; CanPower says whether this machine lets its pod power it off.
	Power(ctx context.Context, req *identity.PowerRequest) (any, error)
	CanPower() bool
	// SignPower signs a power request with the key of this pod, for a window on its own origin.
	SignPower(req *identity.PowerRequest) error
}

func (s *Server) registerPods() {
	s.handle("pods.list", func(ctx context.Context, c *Client, p json.RawMessage) (any, error) {
		if s.Pods == nil {
			return nil, i18n.New("the pod is not paired")
		}
		return s.Pods.List(), nil
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
	// Update this pod: the page signs the request with the device key of the browser; this pod only
	// carries it, the pod it names checks it and runs it.
	s.handle("pods.update", func(ctx context.Context, c *Client, p json.RawMessage) (any, error) {
		if s.Pods == nil {
			return nil, i18n.New("the pod is not paired")
		}
		var req identity.UpdateRequest
		if err := json.Unmarshal(p, &req); err != nil {
			return nil, err
		}
		req.V = 1
		return s.Pods.Update(ctx, &req)
	})
	// Power: signed by the page like an update, checked and run by the pod it is for (or, for a wake,
	// by an awake pod of the network of the machine).
	s.handle("pods.power", func(ctx context.Context, c *Client, p json.RawMessage) (any, error) {
		if s.Pods == nil {
			return nil, i18n.New("the pod is not paired")
		}
		var req identity.PowerRequest
		if err := json.Unmarshal(p, &req); err != nil {
			return nil, err
		}
		req.V = 1
		// No passkey on the LAN: a window on the origin of the pod sends the action unsigned and
		// the pod signs it. A window of the relay must sign it with the key of its browser.
		if req.Sig == "" {
			if !c.local {
				return nil, i18n.New("the request must be signed by this browser")
			}
			if err := s.Pods.SignPower(&req); err != nil {
				return nil, err
			}
		}
		return s.Pods.Power(ctx, &req)
	})
	s.handle("pods.self", func(ctx context.Context, c *Client, p json.RawMessage) (any, error) {
		if s.Pods == nil {
			return nil, i18n.New("the pod is not paired")
		}
		out := map[string]any{ // "site" on the wire: the cluster (GLOSSARY.md)
			"site": s.Pods.Cluster(), "gateway": s.Pods.Gateway(), "preferred": s.Pods.Preferred(),
			"canUpdate": s.Pods.CanUpdate(), "power": s.Pods.CanPower(), "local": c.local}
		if own := s.Pods.Own(); own != "" {
			out["key"], out["fingerprint"] = own, identity.Fingerprint(own)
		}
		return out, nil
	})
}

// PodsChanged tells the windows that the pods of the account changed.
func (s *Server) PodsChanged() {
	if s.Pods != nil {
		s.broadcast("pods.changed", s.Pods.List(), nil)
	}
}
