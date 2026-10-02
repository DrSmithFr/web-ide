// Package sessions stores the active context of each project (open files, cursors,
// consoles, split layout) in ~/.web-ide/sessions/<project>.json.
package sessions

import (
	"encoding/json"
	"sync"
	"time"

	"webide/pod/internal/store"
)

type Sessions struct {
	mu      sync.Mutex
	st      *store.Store
	data    map[string]json.RawMessage
	pending map[string]*time.Timer
}

func New(st *store.Store) *Sessions {
	return &Sessions{st: st, data: map[string]json.RawMessage{}, pending: map[string]*time.Timer{}}
}

func name(id string) string { return "sessions/" + id + ".json" }

func (s *Sessions) Get(id string) json.RawMessage {
	s.mu.Lock()
	defer s.mu.Unlock()
	if d, ok := s.data[id]; ok {
		return d
	}
	var raw json.RawMessage
	if err := s.st.ReadJSON(name(id), &raw); err != nil {
		raw = json.RawMessage(`null`)
	}
	s.data[id] = raw
	return raw
}

// Put replaces the session in memory and writes it to disk after a short delay.
func (s *Sessions) Put(id string, raw json.RawMessage) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data[id] = raw
	if t, ok := s.pending[id]; ok {
		t.Stop()
	}
	s.pending[id] = time.AfterFunc(500*time.Millisecond, func() { s.flush(id) })
}

func (s *Sessions) flush(id string) {
	s.mu.Lock()
	raw := s.data[id]
	delete(s.pending, id)
	s.mu.Unlock()
	_ = s.st.WriteJSON(name(id), raw)
}

func (s *Sessions) Delete(id string) {
	s.mu.Lock()
	delete(s.data, id)
	if t, ok := s.pending[id]; ok {
		t.Stop()
		delete(s.pending, id)
	}
	s.mu.Unlock()
	_ = s.st.Remove(name(id))
}

// FlushAll writes every pending session (on shutdown).
func (s *Sessions) FlushAll() {
	s.mu.Lock()
	ids := make([]string, 0, len(s.pending))
	for id, t := range s.pending {
		t.Stop()
		ids = append(ids, id)
	}
	s.mu.Unlock()
	for _, id := range ids {
		s.flush(id)
	}
}
