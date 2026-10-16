package sessions

import (
	"encoding/json"
	"strconv"
)

// A desk is a session of its own on a project (its layout, tabs and tools), as the virtual
// desktops of a desktop environment. The list of the desks of a project, and what they
// share (the Recent Files), is in sessions/<project>.desks.json.
type Desk struct {
	N    int    `json:"n"`
	Name string `json:"name,omitempty"`
	// Mode is the layout mode: full (rails and tool zones) or grid (tiles only).
	Mode string `json:"mode,omitempty"`
}

type DeskList struct {
	Desks  []Desk   `json:"desks"`
	Recent []string `json:"recent"`
}

// Key is the session key of a desk: the project id for desk 1 (the session it had before
// desks), id@N for the others.
func Key(id string, desk int) string {
	if desk <= 1 {
		return id
	}
	return id + "@" + strconv.Itoa(desk)
}

func desksName(id string) string { return "sessions/" + id + ".desks.json" }

// Desks is the desk list of a project. Without a file it is desk 1 alone, with the Recent
// Files taken from its session (where they were before desks).
func (s *Sessions) Desks(id string) DeskList {
	s.mu.Lock()
	if d, ok := s.desks[id]; ok {
		defer s.mu.Unlock()
		return clone(d)
	}
	s.mu.Unlock()
	var d DeskList
	if err := s.st.ReadJSON(desksName(id), &d); err != nil {
		var old struct{ Recent []string }
		_ = json.Unmarshal(s.Get(id), &old)
		d = DeskList{Recent: old.Recent}
	}
	d = normalize(d)
	s.mu.Lock()
	defer s.mu.Unlock()
	if cur, ok := s.desks[id]; ok {
		return clone(cur)
	}
	s.desks[id] = &d
	return clone(&d)
}

// HasDesk tells whether desk n exists on the project.
func (s *Sessions) HasDesk(id string, n int) bool {
	for _, d := range s.Desks(id).Desks {
		if d.N == n {
			return true
		}
	}
	return false
}

// PutDesks replaces the desk list (desk 1 is always kept) and removes the sessions of the
// desks no longer listed.
func (s *Sessions) PutDesks(id string, d DeskList) DeskList {
	prev := s.Desks(id)
	d = normalize(d)
	s.mu.Lock()
	s.desks[id] = &d
	s.mu.Unlock()
	_ = s.st.WriteJSON(desksName(id), d)
	kept := map[int]bool{}
	for _, x := range d.Desks {
		kept[x.N] = true
	}
	for _, x := range prev.Desks {
		if !kept[x.N] {
			s.drop(Key(id, x.N))
		}
	}
	return clone(&d)
}

func normalize(d DeskList) DeskList {
	out := DeskList{Recent: d.Recent, Desks: []Desk{{N: 1}}}
	if out.Recent == nil {
		out.Recent = []string{}
	}
	seen := map[int]bool{1: true}
	for _, x := range d.Desks {
		if x.N == 1 {
			out.Desks[0] = x
			continue
		}
		if x.N < 1 || seen[x.N] {
			continue
		}
		seen[x.N] = true
		out.Desks = append(out.Desks, x)
	}
	for i := range out.Desks {
		if m := out.Desks[i].Mode; m != "full" && m != "grid" {
			out.Desks[i].Mode = "full"
		}
	}
	return out
}

func clone(d *DeskList) DeskList {
	return DeskList{Desks: append([]Desk(nil), d.Desks...), Recent: append([]string{}, d.Recent...)}
}
