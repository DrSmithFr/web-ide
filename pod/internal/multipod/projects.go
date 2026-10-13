package multipod

import (
	"encoding/json"
	"time"

	"github.com/DrSmithFr/web-ide/pod/internal/identity"
	"github.com/DrSmithFr/web-ide/pod/internal/settingsync"
)

// ProjectsSource publishes the project list of this pod under its own kind, "projects:<device key>".
// One writer per kind, so there is nothing to merge: the list is written whole, and the other pods
// read it from the blob the relay cached.
type ProjectsSource struct {
	own     string
	name    string
	version string
	read    func() []Project
}

// NewProjectsSource makes the source of the list of this pod. read gives its projects, with the
// icon of each when the pod has one.
func NewProjectsSource(own, name, version string, read func() []Project) *ProjectsSource {
	return &ProjectsSource{own: own, name: name, version: version, read: read}
}

func (s *ProjectsSource) Kind() string { return identity.ProjectsKind(s.own) }

// No map field: the list is written whole. There is one writer per kind, so nothing is merged.
func (s *ProjectsSource) MapFields() []string { return nil }
func (s *ProjectsSource) Enabled() bool       { return true }

// Read is the whole list. It holds no timestamp: the relay stamps the write it stores, so an
// unchanged list stays an unchanged list and nothing is written again.
func (s *ProjectsSource) Read() json.RawMessage {
	l := List{V: 1, Name: s.name, Version: s.version, Projects: s.read()}
	b, err := json.Marshal(l)
	if err != nil {
		return json.RawMessage(`{}`)
	}
	return b
}

// Write and Clear do nothing: this pod is the only writer of its own list. A list written by
// another device under this kind cannot exist — the relay checks the signature against the device
// the blob names.
func (s *ProjectsSource) Write(content json.RawMessage, label string) error { return nil }
func (s *ProjectsSource) Clear(label string) error                          { return nil }

// MaxProjectsPlain is the size the sealed list must stay under, in characters of plain JSON: the
// relay caps a blob at 64 KiB of base64 ciphertext, and a list over the cap is refused. Icons are
// the heavy part, so they are dropped first.
const MaxProjectsPlain = 40 << 10

// MaxIconSize is the size of one project icon kept in a list.
const MaxIconSize = 8 << 10

// Fit drops the icons of a list that would not fit the blob, the biggest first.
func Fit(projects []Project) []Project {
	out := append([]Project{}, projects...)
	total := 0
	for _, p := range out {
		total += len(p.Icon)
	}
	for total > MaxProjectsPlain {
		big, best := 0, -1
		for i, p := range out {
			if len(p.Icon) > big {
				big, best = len(p.Icon), i
			}
		}
		if best < 0 {
			break // no icon left: the list itself is over the cap
		}
		out[best].Icon = ""
		total -= big
	}
	for i := range out {
		if len(out[i].Icon) > MaxIconSize {
			out[i].Icon = ""
		}
	}
	return out
}

// Debounce runs the publish at most once per interval: a burst of project edits (or icon edits) is
// one write, not ten.
type Debounce struct {
	kind     string
	hub      *settingsync.Hub
	interval time.Duration
	timer    *time.Timer
}

// NewDebounce makes the debounced publish of a source.
func NewDebounce(hub *settingsync.Hub, kind string, interval time.Duration) *Debounce {
	return &Debounce{kind: kind, hub: hub, interval: interval}
}

// Touch schedules the write of the list.
func (d *Debounce) Touch() {
	if d.timer != nil {
		d.timer.Reset(d.interval)
		return
	}
	d.timer = time.AfterFunc(d.interval, d.flush)
}

func (d *Debounce) flush() {
	s := d.hub.Source(d.kind)
	if s == nil {
		return
	}
	_ = s.Push()
}
