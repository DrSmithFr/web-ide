package settingsync

import (
	"crypto/ecdh"
	"sync"

	"github.com/DrSmithFr/web-ide/pod/internal/i18n"
	"github.com/DrSmithFr/web-ide/pod/internal/identity"
	"github.com/DrSmithFr/web-ide/pod/internal/store"
)

// Hub is the sync identity of a pod: the X25519 key of its device, the key of the current epoch as
// the wrap an admin made for it, and the sources it syncs. A pod that does not know the phrase
// cannot derive the epoch key by itself: without a wrap it has no key, and it must not write.
type Hub struct {
	st      *store.Store
	tr      Transport
	ident   *identity.PodIdentity
	mu      sync.Mutex
	key     []byte
	epoch   int64
	sources map[string]*Sync
	flags   *Flags
	emit    func(s Status, name string, payload any)
	// other is called with a blob of a kind this pod does not sync (the project lists of the
	// other pods).
	other   func(Msg)
	lastErr string
}

// Status is what the page shows for one kind.
type Status struct {
	Kind      string     `json:"kind"`
	Enabled   bool       `json:"enabled"`
	HasKey    bool       `json:"hasKey"`
	Epoch     int64      `json:"epoch"`
	Seq       int64      `json:"seq"`
	LastSync  int64      `json:"lastSync"`
	Conflicts []Conflict `json:"conflicts"`
	// The last error the relay answered for this pod (a revoked device, a blob of another epoch).
	Error string `json:"error,omitempty"`
	// The device id of this pod, so the page can tell its own changes from the other ones.
	Device string `json:"device"`
}

// NewHub makes the sync identity of a paired pod.
func NewHub(st *store.Store, tr Transport, ident *identity.PodIdentity, flags *Flags) *Hub {
	return &Hub{st: st, tr: tr, ident: ident, flags: flags, sources: map[string]*Sync{}}
}

// SetEmitter is called with the events of the loop: a change applied from another pod, a conflict,
// the state of a source. The page shows them.
func (h *Hub) SetEmitter(fn func(s Status, name string, payload any)) { h.emit = fn }

// SetTransport gives the hub the link it sends its sync messages on. The link comes up later than
// the hub (the pod dials the relay), so the transport is set after the hub is built.
func (h *Hub) SetTransport(tr Transport) { h.tr = tr }

// Send sends one sync message on the link of the pod (nil transport: the link is down).
func (h *Hub) Send(m Msg) error {
	if h.tr == nil {
		return i18n.New("no link to the relay")
	}
	return h.tr.Send(m)
}

// SetOther is called with a blob the relay pushed for a kind this pod does not sync: the project
// lists of the other pods are read that way.
func (h *Hub) SetOther(fn func(Msg)) { h.other = fn }

// KeyFor is the sub-key of a kind under the epoch key this pod holds (nil: no wrap yet).
func (h *Hub) KeyFor(kind string) []byte { return h.kindKey(kind) }

// Epoch is the epoch of the key this pod holds.
func (h *Hub) Epoch() int64 { return h.currentEpoch() }

// HasKey reports whether this pod holds the key of the epoch (its own wrap arrived).
func (h *Hub) HasKey() bool { return h.hasKey() }

// Account is the account this pod is paired with.
func (h *Hub) Account() string { return h.ident.Account }

// Device is the device id of this pod.
func (h *Hub) Device() string { return h.ident.ID() }

// SetError keeps the last error the relay answered for this pod (a revoked device, a blob of
// another epoch): the page shows it next to the state of the sync.
func (h *Hub) SetError(err error) {
	h.mu.Lock()
	h.lastErr = ""
	if err != nil {
		h.lastErr = err.Error()
	}
	h.mu.Unlock()
	for _, s := range h.sources {
		// The payload goes to the page as JSON: an error value would be marshalled as its struct,
		// so the text is sent, not the error.
		s.event("error", err.Error())
	}
}

// AddSource registers one kind of data to sync.
func (h *Hub) AddSource(src Source) (*Sync, error) {
	s, err := newSync(h.st, src, h)
	if err != nil {
		return nil, err
	}
	h.sources[src.Kind()] = s
	return s, nil
}

// Source returns the loop of a kind (nil when the pod does not sync it).
func (h *Hub) Source(kind string) *Sync { return h.sources[kind] }

// Flags gives the sync switches (the "sync" object of the settings): settings sync is on by
// default, the vault is opt-in.
func (h *Hub) Flags() *Flags { return h.flags }

// Status lists the sources of the pod.
func (h *Hub) Status() []Status {
	var out []Status
	for _, s := range h.sources {
		out = append(out, s.Status())
	}
	return out
}

// Record is the X25519 key of this device, signed by its device key: the pod publishes it so an
// admin can wrap the key of the epoch for it. The key is derived from the device seed, so a pod
// rebuilds it after a restart without a new file on disk.
func (h *Hub) Record() (*identity.SyncKeyRecord, error) {
	x, err := h.x25519()
	if err != nil {
		return nil, err
	}
	r := &identity.SyncKeyRecord{Account: h.ident.Account, Device: h.ident.ID(), Key: identity.X25519ID(x.PublicKey())}
	r.Sig = identity.Sign(h.ident.Key(), []byte(r.Canonical()))
	return r, nil
}

// Publish sends the key of this device to the relay, then asks for the wrap made for it and for the
// stored state of every source. It is what a pod does when its link comes up.
func (h *Hub) Publish() error {
	if !h.ident.Paired() {
		return i18n.New("the pod is not paired")
	}
	rec, err := h.Record()
	if err != nil {
		return err
	}
	if err := h.tr.Send(Msg{Type: "syncKey", Account: h.ident.Account, Device: h.ident.ID(), SyncKey: rec}); err != nil {
		return err
	}
	if err := h.tr.Send(Msg{Type: "wrapGet", Account: h.ident.Account, Device: h.ident.ID()}); err != nil {
		return err
	}
	// A pod paired by an admin has no key until the wrap arrives: the pull is made when it does.
	if !h.hasKey() {
		return nil
	}
	return h.PullAll()
}

// PullAll asks the relay for the stored state of every source that is on.
func (h *Hub) PullAll() error {
	for _, s := range h.sources {
		if err := s.Pull(); err != nil {
			return err
		}
	}
	return nil
}

// SetWrap opens the wrap an admin made for this device: the only way a pod that does not know the
// phrase gets the key of the epoch. A wrap of another account, for another device, or that does not
// open with the key of this device is refused.
func (h *Hub) SetWrap(w *identity.WrapRecord) error {
	if w == nil {
		return i18n.New("no wrap")
	}
	if w.Account != h.ident.Account {
		return i18n.New("wrap of another account")
	}
	if w.Device != h.ident.ID() {
		return i18n.New("wrap made for another device")
	}
	if err := identity.VerifySig(w.Account, w.Sig, w.Canonical()); err != nil {
		return err
	}
	x, err := h.x25519()
	if err != nil {
		return err
	}
	shared, err := identity.Shared(x, w.Key)
	if err != nil {
		return err
	}
	plain, err := identity.Unseal(identity.WrapKey(shared), identity.WrapAAD(w.Account, w.Device, w.Epoch), w.Nonce, w.Data)
	if err != nil {
		return i18n.New("the wrap does not open with the key of this device")
	}
	h.mu.Lock()
	hadKey := h.key != nil
	rotated := h.epoch != w.Epoch
	h.key, h.epoch = plain, w.Epoch
	// The wrap answers the request that failed before it existed: the page must not keep showing
	// "no wrap" once the key is here.
	h.lastErr = ""
	h.mu.Unlock()
	// The page shows the state of each source: a wrap that arrives after it was opened must refresh
	// it, else the panel keeps saying "no sync key yet".
	for _, s := range h.sources {
		s.event("changed", nil)
	}
	// A pod that just got its key, or that moved to a new epoch, cannot read the stored state
	// before: it reads it now, otherwise its own changes would overwrite the other pods.
	if !hadKey || rotated {
		return h.PullAll()
	}
	return nil
}

// Handle runs one answer or one push of the relay.
func (h *Hub) Handle(m Msg) error {
	if m.Kind == "" {
		if m.Type == "wrap" {
			return h.SetWrap(m.Wrap)
		}
		return nil
	}
	if s := h.sources[m.Kind]; s != nil {
		return s.Handle(m)
	}
	if h.other != nil {
		h.other(m)
	}
	return nil
}

// PushAll writes every source that has something to send: what the pod does after a local change.
func (h *Hub) PushAll() error {
	for _, s := range h.sources {
		if err := s.Push(); err != nil {
			return err
		}
	}
	return nil
}

// revoked reports whether this pod is on the revocation list it checked itself. A revoked device
// cannot write: the relay refuses it, and the pod does not try.
func (h *Hub) revoked() bool {
	return h.ident.Revocations != nil && h.ident.Revocations.Has(h.ident.ID())
}

func (h *Hub) lastError() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.lastErr
}

func (h *Hub) hasKey() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.key != nil
}

func (h *Hub) kindKey(kind string) []byte {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.key == nil {
		return nil
	}
	return identity.KindKey(h.key, kind)
}

func (h *Hub) currentEpoch() int64 {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.epoch
}

func (h *Hub) x25519() (*ecdh.PrivateKey, error) {
	return identity.X25519FromSeed(identity.Derive(h.ident.Seed, identity.InfoX25519))
}

func (h *Hub) emitEvent(s Status, name string, payload any) {
	if h.emit == nil {
		return
	}
	h.emit(s, name, payload)
}
