package settingsync

import (
	"github.com/DrSmithFr/web-ide/pod/internal/identity"
)

// Msg is one sync message between a pod and the relay: the published X25519 key of the device, the
// wrap of the epoch key made for it, and the encrypted blobs. The relay cannot read a blob, it only
// checks the signature against the device that wrote it.
type Msg struct {
	Type    string                  `json:"type"`
	Account string                  `json:"account,omitempty"`
	Device  string                  `json:"device,omitempty"`
	Kind    string                  `json:"kind,omitempty"`
	SyncKey *identity.SyncKeyRecord `json:"syncKey,omitempty"`
	Wrap    *identity.WrapRecord    `json:"wrap,omitempty"`
	Blob    *identity.BlobRecord    `json:"blob,omitempty"`
	Updated int64                   `json:"updated,omitempty"` // time the stored blob was written
	Message string                  `json:"message,omitempty"` // stored or older
}

// Transport is the link a pod sends its sync messages on (its link to the relay).
type Transport interface {
	Send(m Msg) error
}
