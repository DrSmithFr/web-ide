// Package settingsync merges the settings of the pods of an account. The relay stores one encrypted
// blob per account and kind and cannot read a value; the pods do the merge.
//
// Every entry carries the value, the hybrid logical clock of the change that wrote it and the device
// that made it. A pod keeps the last state it synced (its base): an entry changed on the other side
// and untouched here is taken, an entry changed here and untouched there stays, and only an entry
// changed on both sides to different values asks the user.
package settingsync

import (
	"encoding/json"
	"sort"
	"strings"
)

// sep separates the parts of a path. A key of a map field may hold a dot, so paths use a slash:
// "font/size", "tokenColors/comment".
const sep = "/"

// Field is one entry of a state: the value, the clock of the change that wrote it, and the device
// that made it. Del marks an entry deleted on one side; an entry with clock 0 is absent.
type Field struct {
	Value json.RawMessage `json:"value,omitempty"`
	Rev   int64           `json:"rev"`
	Dev   string          `json:"dev"`
	Del   bool            `json:"del,omitempty"`
}

// State is the settings of one device, flattened to one entry per leaf path.
type State struct {
	Device string           `json:"device"`
	Clock  int64            `json:"clock"`
	Fields map[string]Field `json:"fields"`
}

// Conflict is an entry changed on both sides to different values. The merged state keeps the local
// value and the conflict is recorded; the choice the user makes is a normal write.
type Conflict struct {
	Path   string          `json:"path"`
	Local  json.RawMessage `json:"local"`
	Remote json.RawMessage `json:"remote"`
	Dev    string          `json:"dev"`
	Rev    int64           `json:"rev"`
}

// NewState makes an empty state for a device.
func NewState(device string) State {
	return State{Device: device, Fields: map[string]Field{}}
}

// copyOf copies a state under another device: the same view, nothing changed since the base.
func copyOf(base State, device string) State {
	s := NewState(device)
	for p, f := range base.Fields {
		s.Fields[p] = f
	}
	s.Clock = base.Clock
	return s
}

// Apply records a local change: the entries whose value moved take the clock of this device, the
// others keep the clock they came with. It reports whether anything changed.
func (s *State) Apply(content json.RawMessage, mapFields []string) bool {
	leaves := flatten(content, mapFields)
	changed := false
	for p, v := range leaves {
		f := s.Fields[p]
		if f.Rev > 0 && sameValue(f, Field{Value: v}) {
			continue
		}
		s.Clock++
		s.Fields[p] = Field{Value: v, Rev: s.Clock, Dev: s.Device}
		changed = true
	}
	for p, f := range s.Fields {
		if _, ok := leaves[p]; !ok && !f.Del {
			s.Clock++
			s.Fields[p] = Field{Del: true, Rev: s.Clock, Dev: s.Device}
			changed = true
		}
	}
	return changed
}

// Project writes the state back as an object: the paths become nested objects, and a map field stays
// an object even when all its entries are gone.
func (s *State) Project(mapFields []string) json.RawMessage {
	root := map[string]any{}
	node := func(path string) map[string]any {
		if path == "" {
			return root
		}
		n := root
		for _, part := range strings.Split(path, sep) {
			next, ok := n[part].(map[string]any)
			if !ok {
				next = map[string]any{}
				n[part] = next
			}
			n = next
		}
		return n
	}
	isMap := map[string]bool{}
	for _, m := range mapFields {
		isMap[m] = true
	}
	for p, f := range s.Fields {
		parts := strings.Split(p, sep)
		parent := strings.Join(parts[:len(parts)-1], sep)
		// A map field emptied on one side keeps its shape: its entries are still in the state as
		// deletions, so the other pod reads an object, not a missing field. A map field never seen
		// is not added.
		if isMap[parent] {
			node(parent)
		}
		if f.Rev == 0 || f.Del {
			continue
		}
		node(parent)[parts[len(parts)-1]] = f.Value
	}
	b, err := json.Marshal(root)
	if err != nil {
		return json.RawMessage(`{}`)
	}
	return b
}

// Merge merges the three states: base is the last state the pod synced, local its own view, remote
// the state read from the relay. Only an entry changed on both sides to different values is a
// conflict; the merged state keeps the local value.
func Merge(base, local, remote State) (State, []Conflict) {
	out := NewState(local.Device)
	out.Clock = max(base.Clock, local.Clock, remote.Clock)

	seen := map[string]bool{}
	var paths []string
	for _, m := range []map[string]Field{base.Fields, local.Fields, remote.Fields} {
		for p := range m {
			if !seen[p] {
				seen[p] = true
				paths = append(paths, p)
			}
		}
	}
	sort.Strings(paths)

	var conflicts []Conflict
	for _, p := range paths {
		b, l, r := base.Fields[p], local.Fields[p], remote.Fields[p]
		// An entry a side never saw (clock 0) is not a change on that side: only a value it wrote,
		// or a deletion it recorded, counts. A pod that writes a state where a field is simply
		// missing has not deleted it, it has never seen it.
		lc, rc := l.Rev > 0 && !sameValue(l, b), r.Rev > 0 && !sameValue(r, b)
		keep := l
		switch {
		case lc && rc && !sameValue(l, r):
			// A deletion merges silently: the user cannot be shown a value next to nothing. The
			// later change wins; on the same clock the value beats the deletion, so nothing is
			// dropped by a tie. Only two values ask.
			if l.Del || r.Del {
				keep = r
				if l.Rev > r.Rev || (l.Rev == r.Rev && !l.Del) {
					keep = l
				}
			} else {
				conflicts = append(conflicts, Conflict{Path: p, Local: l.Value, Remote: r.Value, Dev: r.Dev, Rev: r.Rev})
			}
		case !lc && rc:
			keep = r
		}
		if keep.Rev == 0 {
			continue
		}
		out.Fields[p] = keep
	}
	return out, conflicts
}

// flatten writes one entry per leaf path. mapFields are the containers whose entries merge per key:
// a token color added on one pod must not overwrite the whole map of the other. Unknown paths are
// kept as they are, so a field an older pod does not know survives the merge.
func flatten(content json.RawMessage, mapFields []string) map[string]json.RawMessage {
	var v any
	if err := json.Unmarshal(content, &v); err != nil {
		return nil
	}
	isMap := map[string]bool{}
	for _, m := range mapFields {
		isMap[m] = true
	}
	out := map[string]json.RawMessage{}
	var walk func(v any, path string)
	walk = func(v any, path string) {
		obj, ok := v.(map[string]any)
		if !ok {
			if path != "" {
				out[path] = marshal(v)
			}
			return
		}
		if isMap[path] {
			for k, val := range obj {
				out[path+sep+k] = marshal(val)
			}
			return
		}
		for k, val := range obj {
			child := k
			if path != "" {
				child = path + sep + k
			}
			walk(val, child)
		}
	}
	walk(v, "")
	return out
}

// sameValue compares the content of two entries, not the clock that wrote them.
func sameValue(a, b Field) bool { return a.Del == b.Del && string(a.Value) == string(b.Value) }

// sameState reports whether two states hold the same values.
func sameState(a, b State) bool {
	if len(a.Fields) != len(b.Fields) {
		return false
	}
	for p, f := range a.Fields {
		g, ok := b.Fields[p]
		if !ok || !sameValue(f, g) {
			return false
		}
	}
	return true
}

// marshal encodes a value the way the entries of a state store it: sorted keys, no spaces, so two
// states holding the same object compare as the same text.
func marshal(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		return json.RawMessage(`null`)
	}
	return b
}
