// Package settings keeps the IDE settings as an append-only history of full snapshots
// (~/.web-ide/settings.json). Entries are never modified: a rollback appends a copy of an
// older entry, so a rollback can itself be undone.
package settings

import (
	"bytes"
	"encoding/json"
	"errors"
	"strconv"
	"sync"
	"time"

	"github.com/DrSmithFr/web-ide/pod/internal/store"
)

const (
	file       = "settings.json"
	maxEntries = 100
)

type Entry struct {
	ID       int             `json:"id"`
	TS       time.Time       `json:"ts"`
	Label    string          `json:"label"`
	Settings json.RawMessage `json:"settings"`
}

type history struct {
	Current int     `json:"current"`
	NextID  int     `json:"nextId"`
	Entries []Entry `json:"entries"`
}

type Settings struct {
	mu sync.Mutex
	st *store.Store
	h  history
}

func Load(st *store.Store) (*Settings, error) {
	s := &Settings{st: st}
	if err := st.ReadJSON(file, &s.h); err != nil && !store.IsNotExist(err) {
		return nil, err
	}
	// The file is indented: snapshots are kept compact in memory so they compare as text.
	for i := range s.h.Entries {
		s.h.Entries[i].Settings = compact(s.h.Entries[i].Settings)
	}
	if len(s.h.Entries) == 0 {
		s.h.NextID = 1
		s.append(json.RawMessage(`{}`), "Réglages initiaux")
	}
	return s, nil
}

func (s *Settings) append(data json.RawMessage, label string) Entry {
	e := Entry{ID: s.h.NextID, TS: time.Now(), Label: label, Settings: data}
	s.h.NextID++
	s.h.Entries = append(s.h.Entries, e)
	if over := len(s.h.Entries) - maxEntries; over > 0 {
		s.h.Entries = append([]Entry(nil), s.h.Entries[over:]...)
	}
	s.h.Current = e.ID
	_ = s.st.WriteJSON(file, s.h)
	return e
}

func compact(raw json.RawMessage) json.RawMessage {
	var b bytes.Buffer
	if json.Compact(&b, raw) != nil {
		return raw
	}
	return b.Bytes()
}

func (s *Settings) find(id int) (Entry, bool) {
	for _, e := range s.h.Entries {
		if e.ID == id {
			return e, true
		}
	}
	return Entry{}, false
}

// Current returns the active entry.
func (s *Settings) Current() Entry {
	s.mu.Lock()
	defer s.mu.Unlock()
	if e, ok := s.find(s.h.Current); ok {
		return e
	}
	return s.h.Entries[len(s.h.Entries)-1]
}

// Save records a new snapshot, unless it is identical to the current one.
func (s *Settings) Save(data json.RawMessage, label string) (Entry, error) {
	if !json.Valid(data) {
		return Entry{}, errors.New("réglages invalides")
	}
	data = compact(data)
	s.mu.Lock()
	defer s.mu.Unlock()
	if cur, ok := s.find(s.h.Current); ok && string(cur.Settings) == string(data) && label == "" {
		return cur, nil
	}
	if label == "" {
		label = "Modification"
	}
	return s.append(data, label), nil
}

// Snapshot forces a copy of the current entry (before an import, for example).
func (s *Settings) Snapshot(label string) Entry {
	s.mu.Lock()
	defer s.mu.Unlock()
	cur, _ := s.find(s.h.Current)
	return s.append(cur.Settings, label)
}

func (s *Settings) Rollback(id int) (Entry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	old, ok := s.find(id)
	if !ok {
		return Entry{}, errors.New("entrée d'historique introuvable")
	}
	return s.append(old.Settings, "Retour à #"+strconv.Itoa(old.ID)), nil
}

type Summary struct {
	ID      int       `json:"id"`
	TS      time.Time `json:"ts"`
	Label   string    `json:"label"`
	Current bool      `json:"current"`
}

func (s *Settings) History() []Summary {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Summary, 0, len(s.h.Entries))
	for i := len(s.h.Entries) - 1; i >= 0; i-- {
		e := s.h.Entries[i]
		out = append(out, Summary{ID: e.ID, TS: e.TS, Label: e.Label, Current: e.ID == s.h.Current})
	}
	return out
}
