package multipod

import (
	"context"
	"errors"

	"github.com/DrSmithFr/web-ide/lib/identity"
	"github.com/DrSmithFr/web-ide/lib/tunnel"
)

// LANRouter reaches a pod of this pod's cluster over its LAN link (pod/lan.OpenTunnel).
type LANRouter interface {
	OpenTunnel(ctx context.Context, key, window, label string, via *identity.Cert, fpBrowser, fpPod string) (tunnel.MsgConn, error)
}

// Route opens the channels of another pod of the account, for a browser this pod serves as its
// gateway (relay.md, *Multi-pod*): a pod of this pod's cluster over the LAN link, a pod of another
// cluster pod to pod over WebRTC. It answers relayclient.PeerRoute.
type Route struct {
	lan LANRouter
}

// NewRoute makes the router of a pod. lan is nil when the pod has no LAN link.
func NewRoute(l LANRouter) *Route { return &Route{lan: l} }

// Open opens one channel of the pod named by its device key, for the window of a browser this
// pod authenticated: window is the id of its tunnel here, via its certificate, and fpBrowser /
// fpPod the DTLS fingerprints of that tunnel.
func (r *Route) Open(ctx context.Context, key, window, label string, via *identity.Cert, fpBrowser, fpPod string) (tunnel.MsgConn, error) {
	if r.lan != nil {
		return r.lan.OpenTunnel(ctx, key, window, label, via, fpBrowser, fpPod)
	}
	return nil, errors.New("multipod: this pod reaches no other pod")
}
