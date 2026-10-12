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
	emit    func(s Status, name string, payload any)
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
}

// NewHub makes the sync identity of a paired pod.
func NewHub(st *store.Store, tr Transport, ident *identity.PodIdentity) *Hub {
	return &Hub{st: st, tr: tr, ident: ident, sources: map[string]*Sync{}}
}

// SetEmitter is called with the events of the loop: a change applied from another pod, a conflict,
// the state of a source. The page shows them.
func (h *Hub) SetEmitter(fn func(s Status, name string, payload any)) { h.emit = fn }

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
	rotated := h.epoch != w.Epoch
	h.key, h.epoch = plain, w.Epoch
	h.mu.Unlock()
	// A rotation moves the epoch: the stored blobs are encrypted with the new key, so every source
	// must be read again.
	if rotated {
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
	return nil
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
