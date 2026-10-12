package settingsync

import (
	"encoding/json"
	"reflect"
	"testing"
)

// The fields whose entries merge per key: a token color added on one pod must not overwrite the
// whole map of the other.
var mapFields = []string{"tokenColors", "syntax", "keyboard/overrides"}

// state makes a state for a device by applying a content to an empty one.
func state(t *testing.T, device, content string) State {
	t.Helper()
	s := NewState(device)
	if !s.Apply(json.RawMessage(content), mapFields) {
		t.Fatalf("applying %q changed nothing", content)
	}
	return s
}

// changed copies a state and applies a content that differs from it: a change made on one side
// after the base.
func changed(t *testing.T, base State, device, content string) State {
	t.Helper()
	s := copyOf(base, device)
	if !s.Apply(json.RawMessage(content), mapFields) {
		t.Fatalf("applying %q to the base changed nothing", content)
	}
	return s
}

// project reads the merged state back as a map, so a test compares values and not the order of the
// keys.
func project(t *testing.T, s State) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal(s.Project(mapFields), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestMergeTakesRemoteWhenLocalUntouched(t *testing.T) {
	base := state(t, "a", `{"theme":"nuit","language":"en"}`)
	local := copyOf(base, "a")
	remote := changed(t, base, "b", `{"theme":"jour","language":"en"}`)

	got, conflicts := Merge(base, local, remote)
	if len(conflicts) != 0 {
		t.Fatalf("a field changed on one side only must not ask: %+v", conflicts)
	}
	// A map field never seen is not added; only the values are there.
	want := map[string]any{"theme": "jour", "language": "en"}
	if !reflect.DeepEqual(project(t, got), want) {
		t.Fatalf("the remote theme must be taken: %v", project(t, got))
	}
}

func TestMergeKeepsLocalWhenRemoteUntouched(t *testing.T) {
	base := state(t, "a", `{"theme":"nuit","language":"en"}`)
	local := changed(t, base, "a", `{"theme":"nuit","language":"fr"}`)
	remote := copyOf(base, "b")

	got, conflicts := Merge(base, local, remote)
	if len(conflicts) != 0 {
		t.Fatalf("a field changed on one side only must not ask: %+v", conflicts)
	}
	p := project(t, got)
	if p["language"] != "fr" || p["theme"] != "nuit" {
		t.Fatalf("the local language must stay: %v", p)
	}
}

func TestMergeAsksOnlyForTheSameField(t *testing.T) {
	base := state(t, "a", `{"theme":"nuit","language":"en","editor":{"tabSize":4}}`)
	// Both sides changed the theme to a different value; only the language moved on one side.
	remote := changed(t, base, "b", `{"theme":"jour","language":"fr","editor":{"tabSize":4}}`)
	local := changed(t, base, "a", `{"theme":"dark","language":"en","editor":{"tabSize":4}}`)

	got, conflicts := Merge(base, local, remote)
	if len(conflicts) != 1 {
		t.Fatalf("only the theme must ask: %+v", conflicts)
	}
	c := conflicts[0]
	if c.Path != "theme" || string(c.Local) != `"dark"` || string(c.Remote) != `"jour"` || c.Dev != "b" {
		t.Fatalf("the conflict must name the field, both values and the other device: %+v", c)
	}
	// The merged state keeps the local value until the user chooses; the field changed on one side
	// only is taken.
	p := project(t, got)
	if p["theme"] != "dark" || p["language"] != "fr" {
		t.Fatalf("the merged state must keep the local value and take the other: %v", p)
	}

	// The same value written on both sides is not a conflict.
	same := changed(t, base, "a", `{"theme":"jour","language":"fr","editor":{"tabSize":4}}`)
	_, conflicts = Merge(base, same, same)
	if len(conflicts) != 0 {
		t.Fatalf("the same value on both sides must not ask: %+v", conflicts)
	}
}

func TestMergeMapsPerKey(t *testing.T) {
	base := state(t, "a", `{"tokenColors":{"comment":"#888","keyword":"#f0f"},"syntax":{"go":{"type":"keyword"}}}`)
	remote := changed(t, base, "b", `{"tokenColors":{"comment":"#777","keyword":"#f0f","string":"#a5a"},"syntax":{"go":{"type":"keyword"},"json":{"type":"string"}}}`)
	local := changed(t, base, "a", `{"tokenColors":{"comment":"#888","keyword":"#eee"},"syntax":{"go":{"type":"keyword"}}}`)

	got, conflicts := Merge(base, local, remote)
	if len(conflicts) != 0 {
		t.Fatalf("different keys of a map must not conflict: %+v", conflicts)
	}
	tc := project(t, got)["tokenColors"].(map[string]any)
	if tc["comment"] != "#777" || tc["keyword"] != "#eee" || tc["string"] != "#a5a" {
		t.Fatalf("the map must merge per key: %v", tc)
	}
	sy := project(t, got)["syntax"].(map[string]any)
	if _, ok := sy["json"]; !ok {
		t.Fatalf("a key added on the other side must come in: %v", sy)
	}

	// The same key changed on both sides asks.
	localMoved := changed(t, base, "a", `{"tokenColors":{"comment":"#111","keyword":"#f0f"},"syntax":{"go":{"type":"keyword"}}}`)
	_, conflicts = Merge(base, localMoved, remote)
	if len(conflicts) != 1 || conflicts[0].Path != "tokenColors/comment" {
		t.Fatalf("the same key of a map must ask: %+v", conflicts)
	}
}

func TestMergeDeletions(t *testing.T) {
	base := state(t, "a", `{"tokenColors":{"comment":"#888","keyword":"#f0f"},"language":"en"}`)

	// Deleted on the remote side only: it goes, no prompt.
	remote := changed(t, base, "b", `{"tokenColors":{"keyword":"#f0f"},"language":"en"}`)
	local := copyOf(base, "a")
	got, conflicts := Merge(base, local, remote)
	if len(conflicts) != 0 {
		t.Fatalf("a deletion on one side must not ask: %+v", conflicts)
	}
	if _, ok := project(t, got)["tokenColors"].(map[string]any)["comment"]; ok {
		t.Fatalf("the deleted key must be gone: %v", project(t, got))
	}

	// Deleted on one side, changed on the other: the later change wins, no prompt — the user cannot
	// be shown a value next to nothing.
	later := changed(t, base, "a", `{"tokenColors":{"comment":"#111","keyword":"#f0f"},"language":"fr"}`)
	got, conflicts = Merge(base, later, remote)
	if len(conflicts) != 0 {
		t.Fatalf("a deletion against a change must not ask: %+v", conflicts)
	}
	if project(t, got)["tokenColors"].(map[string]any)["comment"] != "#111" {
		t.Fatalf("the later change must win against the deletion: %v", project(t, got))
	}

	// Deleted on both sides: nothing left.
	both := changed(t, base, "a", `{"tokenColors":{"keyword":"#f0f"},"language":"en"}`)
	got, conflicts = Merge(base, both, remote)
	if len(conflicts) != 0 {
		t.Fatalf("the same deletion must not ask: %+v", conflicts)
	}
	if len(project(t, got)["tokenColors"].(map[string]any)) != 1 {
		t.Fatalf("only the surviving key must be there: %v", project(t, got))
	}
}

// A field an older pod does not know must survive the merge: unknown paths are kept as they are.
func TestMergeKeepsUnknownPaths(t *testing.T) {
	base := state(t, "a", `{"theme":"nuit","unknown":{"deep":{"value":1}}}`)
	local := changed(t, base, "a", `{"theme":"dark","unknown":{"deep":{"value":1}}}`)
	remote := changed(t, base, "b", `{"theme":"nuit","unknown":{"deep":{"value":1},"other":2}}`)

	got, conflicts := Merge(base, local, remote)
	if len(conflicts) != 0 {
		t.Fatalf("unknown paths must not conflict: %+v", conflicts)
	}
	p := project(t, got)
	unk, ok := p["unknown"].(map[string]any)
	if !ok {
		t.Fatalf("the unknown field must survive: %v", p)
	}
	if unk["other"] != float64(2) {
		t.Fatalf("a path only the other side knows must come in: %v", unk)
	}
	if p["theme"] != "dark" {
		t.Fatalf("the local change must stay: %v", p)
	}
}

// A map field emptied on one side keeps its shape (an empty object), so the other pod does not read
// it as a missing field.
func TestMergeKeepsEmptyMaps(t *testing.T) {
	base := state(t, "a", `{"tokenColors":{"comment":"#888"}}`)
	local := changed(t, base, "a", `{"tokenColors":{}}`)
	remote := copyOf(base, "b")
	got, conflicts := Merge(base, local, remote)
	if len(conflicts) != 0 {
		t.Fatalf("a deletion on one side must not ask: %+v", conflicts)
	}
	want := map[string]any{"tokenColors": map[string]any{}}
	if !reflect.DeepEqual(project(t, got), want) {
		t.Fatalf("the emptied map must stay an object: %v", project(t, got))
	}
}

// The clock is a hybrid logical clock: the merged state never goes back below the clock it saw, so a
// later change on either side is still a change against the base.
func TestClockNeverGoesBack(t *testing.T) {
	base := state(t, "a", `{"theme":"nuit"}`)
	remote := changed(t, base, "b", `{"theme":"jour"}`)
	got, _ := Merge(base, base, remote)
	if got.Clock < remote.Clock {
		t.Fatalf("the merged clock must not be lower than the clock it merged: %d < %d", got.Clock, remote.Clock)
	}
	before := got.Clock
	if !got.Apply(json.RawMessage(`{"theme":"dark"}`), mapFields) {
		t.Fatal("the merged state must be usable as a base")
	}
	if got.Clock <= before {
		t.Fatalf("a change after a merge must move the clock: %d <= %d", got.Clock, before)
	}
}
