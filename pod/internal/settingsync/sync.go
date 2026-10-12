package settingsync

import (
	"encoding/json"
	"sync"
	"time"

	"github.com/DrSmithFr/web-ide/pod/internal/i18n"
	"github.com/DrSmithFr/web-ide/pod/internal/identity"
	"github.com/DrSmithFr/web-ide/pod/internal/store"
)

// Sync is the loop of one kind for one pod. It keeps three states: the base (the last state the
// relay confirmed), its own view (the base plus the changes made here), and the state read from the
// relay. It writes on each local change and merges what arrives.
type Sync struct {
	st  *store.Store
	src Source
	hub *Hub

	mu           sync.Mutex
	base         State
	local        State
	pendingState State
	seq          int64 // the seq the relay holds for this kind
	pending      *identity.BlobRecord
	conflicts    []Conflict
	lastSync     int64
}

func newSync(st *store.Store, src Source, h *Hub) (*Sync, error) {
	s := &Sync{st: st, src: src, hub: h}
	// The base is the last state this pod synced, kept between restarts: a change made while the
	// pod was offline is still seen as a local change, not as a remote one.
	s.base = NewState(h.ident.ID())
	if err := st.ReadJSON(s.file("base"), &s.base); err != nil && !store.IsNotExist(err) {
		return nil, err
	}
	s.local = copyOf(s.base, h.ident.ID())
	s.local.Apply(src.Read(), src.MapFields())
	if err := st.ReadJSON(s.file("conflicts"), &s.conflicts); err != nil && !store.IsNotExist(err) {
		return nil, err
	}
	return s, nil
}

func (s *Sync) file(name string) string { return "sync/" + name + "-" + s.src.Kind() + ".json" }

// Pull asks the relay for the stored state of the kind.
func (s *Sync) Pull() error {
	if !s.src.Enabled() {
		return nil
	}
	if !s.hub.hasKey() {
		return i18n.New("no sync key yet")
	}
	return s.hub.tr.Send(Msg{Type: "blobGet", Account: s.hub.ident.Account, Device: s.hub.ident.ID(), Kind: s.src.Kind()})
}

// Push writes the local view with the next seq (compare-and-swap). A pod with no sync key yet never
// writes: it cannot read the blob either, and writing would overwrite the state of the other pods.
// A write that did not land is retried with the same seq.
func (s *Sync) Push() error {
	if !s.src.Enabled() {
		return nil
	}
	if !s.hub.hasKey() {
		return i18n.New("no sync key yet")
	}
	s.mu.Lock()
	s.local.Apply(s.src.Read(), s.src.MapFields())
	// Nothing to send while the local view is the state the relay already confirmed.
	if sameState(s.local, s.base) && s.pending == nil {
		s.mu.Unlock()
		return nil
	}
	rec, err := s.record(s.local, identity.OpPut)
	if err != nil {
		s.mu.Unlock()
		return err
	}
	s.pending, s.pendingState = rec, copyOf(s.local, s.hub.ident.ID())
	s.mu.Unlock()
	return s.hub.tr.Send(Msg{Type: "blobPut", Account: s.hub.ident.Account, Device: s.hub.ident.ID(), Kind: s.src.Kind(), Blob: rec})
}

// push sends a state the pod has merged (its own changes plus the ones it took from the relay): the
// other pods must see the changes this pod kept.
func (s *Sync) push(st State) error {
	if !s.hub.hasKey() {
		return i18n.New("no sync key yet")
	}
	s.mu.Lock()
	rec, err := s.record(st, identity.OpPut)
	if err != nil {
		s.mu.Unlock()
		return err
	}
	s.pending, s.pendingState = rec, copyOf(st, s.hub.ident.ID())
	s.mu.Unlock()
	return s.hub.tr.Send(Msg{Type: "blobPut", Account: s.hub.ident.Account, Device: s.hub.ident.ID(), Kind: s.src.Kind(), Blob: rec})
}

// Delete drops the blob of the kind (the vault switched off). It is a write like the others: the
// next seq, signed by the device key.
func (s *Sync) Delete() error {
	if !s.hub.hasKey() {
		return i18n.New("no sync key yet")
	}
	s.mu.Lock()
	rec, err := s.record(NewState(s.hub.ident.ID()), identity.OpDel)
	if err != nil {
		s.mu.Unlock()
		return err
	}
	s.pending, s.pendingState = rec, NewState(s.hub.ident.ID())
	s.mu.Unlock()
	return s.hub.tr.Send(Msg{Type: "blobPut", Account: s.hub.ident.Account, Device: s.hub.ident.ID(), Kind: s.src.Kind(), Blob: rec})
}

// record builds and signs one write. The device and the op are part of the bytes the signature
// covers, so a write cannot be attributed to another device or replayed as a deletion.
func (s *Sync) record(st State, op string) (*identity.BlobRecord, error) {
	next := s.seq + 1
	if s.pending != nil {
		next = s.pending.Seq
	}
	plain, err := json.Marshal(st)
	if err != nil {
		return nil, err
	}
	key := s.hub.kindKey(s.src.Kind())
	if key == nil {
		return nil, i18n.New("no sync key yet")
	}
	epoch := s.hub.currentEpoch()
	nonce, data, err := identity.Seal(key, identity.BlobAAD(s.hub.ident.Account, s.src.Kind(), epoch, next), plain)
	if err != nil {
		return nil, err
	}
	rec := &identity.BlobRecord{Account: s.hub.ident.Account, Kind: s.src.Kind(), Epoch: epoch, Seq: next,
		Device: s.hub.ident.ID(), Op: op, Nonce: nonce, Data: data}
	rec.Sig = identity.Sign(s.hub.ident.Key(), []byte(rec.Canonical()))
	return rec, nil
}

// Handle runs one answer or one push of the relay for this kind.
func (s *Sync) Handle(m Msg) error {
	if m.Type != "blob" {
		return nil
	}
	s.mu.Lock()
	switch m.Message {
	case "stored":
		// The write landed: the state we sent becomes the base.
		if m.Blob != nil && s.pending != nil && m.Blob.Seq == s.pending.Seq {
			s.seq = m.Blob.Seq
			s.base = copyOf(s.pendingState, s.hub.ident.ID())
			s.pending = nil
			s.lastSync = time.Now().Unix()
			s.persist()
		}
		s.mu.Unlock()
		return nil
	case "older":
		// The write did not land (another pod wrote first): read the stored state, merge, retry.
		s.pending = nil
		s.mu.Unlock()
		if err := s.Pull(); err != nil {
			return err
		}
		return s.Push()
	}
	s.mu.Unlock()
	return s.apply(m.Blob, m.Updated)
}

// apply merges a state read from the relay with the local view, writes the result and sends the
// merged state back: the other pods must see the changes this pod kept.
func (s *Sync) apply(rec *identity.BlobRecord, updated int64) error {
	if rec == nil {
		// Nothing stored yet: this pod is the first to write.
		return s.Push()
	}
	if rec.Epoch != s.hub.currentEpoch() {
		// A blob of an older epoch cannot be read with the key of this one: the pod keeps its own
		// view and writes it back under the current key, so the other pods can read it again.
		s.mu.Lock()
		own := copyOf(s.local, s.hub.ident.ID())
		s.mu.Unlock()
		return s.push(own)
	}
	key := s.hub.kindKey(s.src.Kind())
	if key == nil {
		return i18n.New("no sync key yet")
	}
	plain, err := identity.Unseal(key, identity.BlobAAD(rec.Account, rec.Kind, rec.Epoch, rec.Seq), rec.Nonce, rec.Data)
	if err != nil {
		return err
	}
	s.mu.Lock()
	if rec.Op == identity.OpDel {
		s.base, s.local = NewState(s.hub.ident.ID()), NewState(s.hub.ident.ID())
		s.seq, s.pending = rec.Seq, nil
		s.conflicts = nil
		s.lastSync = updated
		s.persist()
		s.mu.Unlock()
		if err := s.src.Clear("sync from " + rec.Device); err != nil {
			return err
		}
		s.event("changed", nil)
		return nil
	}
	var remote State
	if err := json.Unmarshal(plain, &remote); err != nil {
		return err
	}
	merged, conflicts := Merge(s.base, s.local, remote)
	own := !sameState(merged, remote)
	// base and local hold the same values after a merge, but they must stay separate objects: a
	// later local change must not move the base with it.
	s.base, s.local = copyOf(merged, s.hub.ident.ID()), copyOf(merged, s.hub.ident.ID())
	s.seq, s.pending = rec.Seq, nil
	s.conflicts = append(s.conflicts, conflicts...)
	s.lastSync = updated
	if s.lastSync == 0 {
		s.lastSync = time.Now().Unix()
	}
	s.persist()
	content := s.local.Project(s.src.MapFields())
	s.mu.Unlock()

	if err := s.src.Write(content, "sync from "+remote.Device); err != nil {
		return err
	}
	if own {
		if err := s.push(merged); err != nil {
			return err
		}
	}
	for _, c := range conflicts {
		s.event("conflict", c)
	}
	s.event("changed", content)
	return nil
}

// Resolve records the choice the user made for a conflict: a normal write, with the clock of this
// device, so the other pods take it like any other change.
func (s *Sync) Resolve(path string, value json.RawMessage) error {
	s.mu.Lock()
	s.local.Clock++
	s.local.Fields[path] = Field{Value: value, Rev: s.local.Clock, Dev: s.local.Device}
	var rest []Conflict
	for _, c := range s.conflicts {
		if c.Path != path {
			rest = append(rest, c)
		}
	}
	s.conflicts = rest
	s.persist()
	s.mu.Unlock()
	if err := s.src.Write(s.Project(), "choice for "+path); err != nil {
		return err
	}
	return s.Push()
}

// Project is the merged content of the kind, as the source stores it.
func (s *Sync) Project() json.RawMessage {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.local.Project(s.src.MapFields())
}

// Conflicts lists the entries changed on both sides that the user has not chosen yet. A nil slice
// would reach the page as null, so it always returns at least an empty one.
func (s *Sync) Conflicts() []Conflict {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Conflict{}, s.conflicts...)
}

func (s *Sync) Status() Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	return Status{Kind: s.src.Kind(), Enabled: s.src.Enabled(), HasKey: s.hub.hasKey(), Epoch: s.hub.currentEpoch(),
		Seq: s.seq, LastSync: s.lastSync, Conflicts: append([]Conflict{}, s.conflicts...),
		Device: s.hub.ident.ID(), Error: s.hub.lastError()}
}

func (s *Sync) persist() {
	_ = s.st.WriteJSON(s.file("base"), s.local)
	_ = s.st.WriteJSON(s.file("conflicts"), s.conflicts)
}

func (s *Sync) event(name string, payload any) {
	s.hub.emitEvent(s.Status(), name, payload)
}
