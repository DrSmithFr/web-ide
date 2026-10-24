package tunnel

import "context"

// The routes a request between two pods takes (relay.md, *Services*): shown as a badge on both
// sides.
const (
	RouteLAN         = "lan"          // the LAN link, direct on the local network
	RouteRelayDirect = "relay-direct" // WebRTC set up by the relay, a direct or hole-punched path
	RouteRelayTURN   = "relay-turn"   // WebRTC copied by the TURN server of the relay
	RouteIdle        = "idle"         // nothing open for now
)

// Route is the way a connection goes: one of the Route* names, and the addresses of the pair
// (the tooltip of the badge).
type Route struct {
	Name  string `json:"name"`
	Local string `json:"local,omitempty"`
	Peer  string `json:"peer,omitempty"`
}

type routeKey struct{}

// WithRoute is ctx carrying the route of the transport a request came by, asked when needed
// (ICE may move a tunnel to another pair).
func WithRoute(ctx context.Context, f func() Route) context.Context {
	return context.WithValue(ctx, routeKey{}, f)
}

// RouteOf is the route a request came by; the zero Route when the transport told none.
func RouteOf(ctx context.Context) Route {
	if f, ok := ctx.Value(routeKey{}).(func() Route); ok {
		return f()
	}
	return Route{}
}
