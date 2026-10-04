// Package clipboard keeps the history of the texts copied in the IDE, shared by every window
// and project (~/.web-ide/clipboard.json), the most recent first.
package clipboard

import (
	"sync"
	"time"

	"github.com/DrSmithFr/web-ide/pod/internal/store"
)

const (
	file = "clipboard.json"
	// MaxText is the size of the largest text kept; larger copies are left out.
	MaxText = 1 << 20
	// DefaultMax is the number of entries kept when the page gives none.
	DefaultMax = 50
	limit      = 500
)

type Entry struct {
	Text string `json:"text"`
	At   int64  `json:"at"` // milliseconds since the epoch
}

type History struct {
	mu      sync.Mutex
	st      *store.Store
	entries []Entry
}

func Load(st *store.Store) *History {
	h := &History{st: st}
	if err := st.ReadJSON(file, &h.entries); err != nil || h.entries == nil {
		h.entries = []Entry{}
	}
	return h
}

func (h *History) List() []Entry {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]Entry{}, h.entries...)
}

// Add puts a text at the top (an equal entry moves up) and keeps max entries.
// It reports false when nothing changed: empty or too large text, already at the top.
func (h *History) Add(text string, max int) ([]Entry, bool) {
	if max <= 0 {
		max = DefaultMax
	}
	max = min(max, limit)
	h.mu.Lock()
	defer h.mu.Unlock()
	if text == "" || len(text) > MaxText || (len(h.entries) > 0 && h.entries[0].Text == text && len(h.entries) <= max) {
		return append([]Entry{}, h.entries...), false
	}
	out := []Entry{{Text: text, At: time.Now().UnixMilli()}}
	for _, e := range h.entries {
		if e.Text != text && len(out) < max {
			out = append(out, e)
		}
	}
	h.entries = out
	return h.save(), true
}

// Remove drops the entries equal to text.
func (h *History) Remove(text string) []Entry {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := h.entries[:0:0]
	for _, e := range h.entries {
		if e.Text != text {
			out = append(out, e)
		}
	}
	h.entries = out
	return h.save()
}

func (h *History) Clear() []Entry {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.entries = []Entry{}
	return h.save()
}

func (h *History) save() []Entry {
	_ = h.st.WriteJSON(file, h.entries)
	return append([]Entry{}, h.entries...)
}
