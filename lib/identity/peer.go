package identity

import "context"

// peerKey holds, in the context of a request, the certificate of the device that proved who it
// is on the transport (a LAN link, a WebRTC tunnel): the pod serving it knows who asks.
type peerKey struct{}

// WithPeer is ctx carrying the certificate of the peer that proved itself (nil: none).
func WithPeer(ctx context.Context, c *Cert) context.Context {
	if c == nil {
		return ctx
	}
	return context.WithValue(ctx, peerKey{}, c)
}

// PeerOf is the certificate of the peer a request comes from, nil when it proved nothing (a
// window of the pod's own origin, a token).
func PeerOf(ctx context.Context) *Cert {
	c, _ := ctx.Value(peerKey{}).(*Cert)
	return c
}
