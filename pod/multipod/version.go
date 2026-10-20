package multipod

// Protocol is the version of the multi-pod contract: what has to match between the pods of one
// account for them to work together — the messages of the LAN control link ($info, $blob,
// $syncKey, $update), the format of the project list blob, /_lan/tunnel and the @key routing.
// It is not the app version (git describe, server.Version), which may well differ from one pod to
// another, nor the handshake version (tunnel.Version), which the tunnel checks on its own.
//
// A pod older than #58 tells none: it counts as protocol 0, the oldest one.
const Protocol = 1

// What the home page shows for a pod that does not speak this protocol: an older one is outdated
// (Update this pod), a newer one means this pod is the one to update.
const (
	MismatchOutdated = "outdated"
	MismatchNewer    = "newer"
)

// Mismatch names what this pod makes of a pod speaking other: "" when they agree.
func Mismatch(own, other int) string {
	switch {
	case other < own:
		return MismatchOutdated
	case other > own:
		return MismatchNewer
	}
	return ""
}
