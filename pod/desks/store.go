// Package desks stores the state of the desks of each project (open files, cursors,
// consoles, split layout) in ~/.web-ide/desks/<project>.json. A project has desks
// (desks.go): desk 1 is that file, desk N is <project>@N.json.
//
// Before the desks were named so, the state lived in sessions/ (a session is now a run of an
// agent): a file found there and missing in desks/ is moved to desks/ when it is first read.
package desks

import (
	"encoding/json"
	"os"
	"sync"
	"time"

	"antennae.space/ide/pod/store"
)

type Store struct {
	mu      sync.Mutex
	st      *store.Store
	data    map[string]json.RawMessage
	pending map[string]*time.Timer
	desks   map[string]*DeskList
}

func New(st *store.Store) *Store {
	return &Store{st: st, data: map[string]json.RawMessage{}, pending: map[string]*time.Timer{}, desks: map[string]*DeskList{}}
}

func name(id string) string { return "desks/" + id + ".json" }

// legacyDir is where the state of the desks was stored before they were named so.
const legacyDir = "sessions/"

// migrate moves the old file of a state (sessions/<file>) to desks/<file> when desks/ has none.
// A failure leaves the old file in place: it is read from there until the next start.
func (s *Store) migrate(file string) {
	if _, err := os.Stat(s.st.Path("desks", file)); err == nil {
		return
	}
	old := s.st.Path(legacyDir, file)
	if _, err := os.Stat(old); err != nil {
		return
	}
	if err := os.MkdirAll(s.st.Path("desks"), 0o700); err == nil {
		_ = os.Rename(old, s.st.Path("desks", file))
	}
}

func (s *Store) Get(id string) json.RawMessage {
	s.mu.Lock()
	defer s.mu.Unlock()
	if d, ok := s.data[id]; ok {
		return d
	}
	s.migrate(id + ".json")
	var raw json.RawMessage
	if err := s.st.ReadJSON(name(id), &raw); err != nil {
		raw = json.RawMessage(`null`)
	}
	s.data[id] = raw
	return raw
}

// Put replaces the state of a desk in memory and writes it to disk after a short delay.
func (s *Store) Put(id string, raw json.RawMessage) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data[id] = raw
	if t, ok := s.pending[id]; ok {
		t.Stop()
	}
	s.pending[id] = time.AfterFunc(500*time.Millisecond, func() { s.flush(id) })
}

func (s *Store) flush(id string) {
	s.mu.Lock()
	raw := s.data[id]
	delete(s.pending, id)
	s.mu.Unlock()
	_ = s.st.WriteJSON(name(id), raw)
}

// Delete removes the state of a project and of its desks.
func (s *Store) Delete(id string) {
	for _, d := range s.List(id).Desks {
		if d.N != 1 {
			s.drop(Key(id, d.N))
		}
	}
	s.mu.Lock()
	delete(s.desks, id)
	s.mu.Unlock()
	_ = s.st.Remove(desksName(id))
	_ = s.st.Remove(legacyDir + id + ".desks.json")
	s.drop(id)
}

func (s *Store) drop(id string) {
	s.mu.Lock()
	delete(s.data, id)
	if t, ok := s.pending[id]; ok {
		t.Stop()
		delete(s.pending, id)
	}
	s.mu.Unlock()
	_ = s.st.Remove(name(id))
	_ = s.st.Remove(legacyDir + id + ".json")
}

// FlushAll writes every pending state (on shutdown).
func (s *Store) FlushAll() {
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
