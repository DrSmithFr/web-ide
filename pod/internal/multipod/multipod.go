// Package multipod is the pod's view of the other pods of its account (milestone 3): the pods of
// its site it sees on the LAN, the presence the relay reports, and the project lists the pods
// published as encrypted blobs. The relay never sees a project name: it caches ciphertext, and a
// pod that holds the sync key reads the lists itself — the gateway of a site first, since the
// browser opens its tunnel there.
package multipod

import (
	"encoding/json"
	"sort"
	"sync"

	"github.com/DrSmithFr/web-ide/pod/internal/identity"
	"github.com/DrSmithFr/web-ide/pod/internal/lan"
	"github.com/DrSmithFr/web-ide/pod/internal/relayclient"
	"github.com/DrSmithFr/web-ide/pod/internal/settingsync"
)

// LAN is the LAN link of the pod, seen from the directory: the pods it reached on the local
// network, which work with the relay down.
type LAN interface {
	Status() lan.Status
}

// PodView is one pod of the account, as the home page shows it.
type PodView struct {
	Key         string `json:"key"`
	Name        string `json:"name"`
	Version     string `json:"version"`
	Fingerprint string `json:"fingerprint"`
	Site        string `json:"site,omitempty"`
	Gateway     bool   `json:"gateway,omitempty"`
	Online      bool   `json:"online"`
	// Protocol is the multi-pod protocol of the pod (multipod.Protocol): 0 for a pod that tells
	// none, which is a pod older than #60. Mismatch says what this pod makes of it: "outdated"
	// (Update this pod) or "newer" (this pod is the one to update), empty when they agree.
	Protocol int    `json:"protocol"`
	Mismatch string `json:"mismatch,omitempty"`
	// Path is where the page of the pod runs through this pod (a pod of the LAN).
	Path     string          `json:"path,omitempty"`
	Seen     int64           `json:"seen,omitempty"` // time the pod last published its list
	Projects json.RawMessage `json:"projects,omitempty"`
	// NoKey: this pod published nothing the pod can read — it has no sync key yet, or it never
	// published. The page says "waiting for the sync key".
	NoKey bool `json:"nokey,omitempty"`
	// OneWay: the pod reaches this pod on the LAN but this pod cannot reach it back (lan.PeerView).
	// The page says so, rather than a bare "offline".
	OneWay bool `json:"oneway,omitempty"`
}

// Directory merges the three views of the pods of the account and answers pods.list.
type Directory struct {
	hub *settingsync.Hub
	lan LAN
	own string // the device key of this pod

	mu      sync.Mutex
	relay   []relayclient.PodInfo
	site    string
	gateway string
	// remote: this pod has a link to the relay, so it can dial a pod of another site pod to pod
	// (pod/internal/multipod.RemoteGateway) and serve its page under its own /_lan/pod/.
	remote bool
	lists  map[string]*List // device key → the list read from a blob
	seen   map[string]int64
	emit   func()
}

// New makes the directory of a pod. Without a hub (the pod is not paired, or has no sync key yet)
// it still shows the pods of its LAN: the relay reports nothing and the lists cannot be read.
func New(hub *settingsync.Hub, l LAN) *Directory {
	d := &Directory{hub: hub, lan: l, lists: map[string]*List{}, seen: map[string]int64{}}
	if hub != nil {
		d.own = hub.Device()
	}
	return d
}

// SetEmitter is called when the view changes, so the page refreshes its blocks.
func (d *Directory) SetEmitter(fn func()) { d.emit = fn }

// Changed tells the windows the pods moved.
func (d *Directory) Changed() {
	if d.emit != nil {
		d.emit()
	}
}

// RelayPods takes the presence the relay reported: who is online, in which site, and which pod is
// the gateway of each site.
func (d *Directory) RelayPods(pods []relayclient.PodInfo) {
	d.mu.Lock()
	d.relay = pods
	for _, p := range pods {
		if p.Key == d.own {
			d.site, d.gateway = p.Site, ""
			if p.Gateway {
				d.gateway = p.Key
			}
		}
	}
	d.mu.Unlock()
	d.Changed()
}

// RelayGateway records the gateway of a site the relay elected.
func (d *Directory) RelayGateway(site, key string) {
	d.mu.Lock()
	if site == d.site {
		d.gateway = key
	}
	d.mu.Unlock()
	d.Changed()
}

// PodID is the pod id the relay reported for a device key: the address of its signaling link.
// Empty when this pod never saw that pod online.
func (d *Directory) PodID(key string) string {
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, p := range d.relay {
		if p.Key == key {
			return p.PodID
		}
	}
	return ""
}

// SetRemote says that this pod can dial the pods the relay reports (it has a link of its own).
func (d *Directory) SetRemote(on bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.remote = on
}

// Gateway is the device key of the gateway of this pod's site ("" when the relay says nothing).
func (d *Directory) Gateway() string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.gateway
}

// Site is the site of this pod: the smallest device key of the pods linked by the local network.
func (d *Directory) Site() string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.site
}

// Pull asks the relay for the project lists of the account: a blobGet of the kind prefix answers
// one blob per pod that published one.
func (d *Directory) Pull() error {
	if d.hub == nil {
		return nil
	}
	return d.hub.Send(settingsync.Msg{Type: "blobGet", Account: d.hub.Account(), Device: d.own, Kind: identity.ProjectsPrefix})
}

// HandleBlob reads a blob the relay pushed for a project list of another pod. The relay only holds
// ciphertext: the pod opens it with the sub-key of its kind, which only a device holding the sync
// key can build.
func (d *Directory) HandleBlob(m settingsync.Msg) {
	if d.hub == nil || m.Type != "blob" || m.Blob == nil || m.Blob.Kind != identity.ProjectsKind(m.Blob.Device) {
		return
	}
	key := d.hub.KeyFor(m.Blob.Kind)
	if key == nil {
		return // no sync key yet: nothing to read
	}
	if m.Blob.Op == identity.OpDel {
		d.mu.Lock()
		delete(d.lists, m.Blob.Device)
		delete(d.seen, m.Blob.Device)
		d.mu.Unlock()
		d.Changed()
		return
	}
	if m.Blob.Epoch != d.hub.Epoch() {
		return // a list of another epoch cannot be opened with this key
	}
	plain, err := identity.Unseal(key, identity.BlobAAD(m.Blob.Account, m.Blob.Kind, m.Blob.Epoch, m.Blob.Seq), m.Blob.Nonce, m.Blob.Data)
	if err != nil {
		return
	}
	// The blob holds the state envelope of #28, not the list itself.
	var st settingsync.State
	if json.Unmarshal(plain, &st) != nil {
		return
	}
	var l List
	if json.Unmarshal(st.Project(nil), &l) != nil {
		return
	}
	d.mu.Lock()
	d.lists[m.Blob.Device] = &l
	d.seen[m.Blob.Device] = m.Updated
	d.mu.Unlock()
	d.Changed()
}

// List is what a pod publishes about its projects, sealed under its own kind.
type List struct {
	V        int       `json:"v"`
	Name     string    `json:"name"`
	Version  string    `json:"version"`
	Protocol int       `json:"protocol,omitempty"`
	Projects []Project `json:"projects"`
}

// Project is one entry of a published list.
type Project struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Type        string `json:"type"`
	Path        string `json:"path"`
	Icon        string `json:"icon,omitempty"`
}

// List returns the merged view: the pods of the LAN first (they work with the relay down and their
// page is reachable through this pod), then the pods the relay reports online, then the pods whose
// list is only in the cached blobs (offline, greyed).
func (d *Directory) List() []PodView {
	d.mu.Lock()
	defer d.mu.Unlock()
	seen := map[string]bool{}
	out := []PodView{}
	// rank keeps the order of the sources: a pod of the LAN first (its page is reachable through
	// this pod, with the relay down or not), then the pods the relay reports, then the ones only
	// known by a cached list. Inside a rank, by name.
	rank := map[string]int{}
	online := map[string]bool{}
	for _, p := range d.relay {
		online[p.Key] = true
	}
	oneway := map[string]bool{}
	if d.lan != nil {
		for _, p := range d.lan.Status().Peers {
			if p.OneWay {
				oneway[p.ID] = true
				// A LAN peer this pod cannot dial back is not offline when the relay sees it: its
				// view comes from the relay, as a pod of another network.
				if online[p.ID] {
					continue
				}
			}
			v := PodView{Key: p.ID, Name: p.Name, Version: p.Version, Fingerprint: p.Fingerprint,
				Online: p.Online, Path: p.Path, Projects: p.Projects, Protocol: p.Protocol, OneWay: p.OneWay}
			if l := d.lists[p.ID]; l != nil {
				v.Name, v.Version = first(l.Name, v.Name), first(l.Version, v.Version)
			}
			seen[p.ID] = true
			rank[p.ID] = 0
			out = append(out, v)
		}
	}
	for _, p := range d.relay {
		if p.Key == d.own || seen[p.Key] {
			continue
		}
		v := PodView{Key: p.Key, Name: p.Name, Version: p.Version, Fingerprint: identity.Fingerprint(p.Key),
			Site: p.Site, Gateway: p.Gateway, Online: true, Protocol: p.Protocol, OneWay: oneway[p.Key]}
		if d.remote {
			// Reachable pod to pod: its page runs under this pod's origin too.
			v.Path = lan.PeerPrefix + p.Key
		}
		d.fill(&v, p.Key, p.Name)
		seen[p.Key] = true
		rank[p.Key] = 1
		out = append(out, v)
	}
	for key, l := range d.lists {
		if key == d.own || seen[key] {
			continue
		}
		v := PodView{Key: key, Name: l.Name, Version: l.Version, Fingerprint: identity.Fingerprint(key), Online: false, Protocol: l.Protocol}
		d.fill(&v, key, l.Name)
		seen[key] = true
		rank[key] = 2
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool {
		if rank[out[i].Key] != rank[out[j].Key] {
			return rank[out[i].Key] < rank[out[j].Key]
		}
		return out[i].Name < out[j].Name
	})
	for i := range out {
		out[i].Mismatch = Mismatch(Protocol, out[i].Protocol)
	}
	return out
}

// GapOf is what this pod makes of the protocol of another pod, by its device key: -1 when it is
// older, 1 when it is newer, 0 when they speak the same one (or when nothing says). The live
// sources (the relay, the LAN link) answer before a cached list.
func (d *Directory) GapOf(key string) (name, version string, gap int) {
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, p := range d.relay {
		if p.Key == key {
			return p.Name, p.Version, gapTo(Protocol, p.Protocol)
		}
	}
	if d.lan != nil {
		for _, p := range d.lan.Status().Peers {
			if p.ID == key {
				return p.Name, p.Version, gapTo(Protocol, p.Protocol)
			}
		}
	}
	if l := d.lists[key]; l != nil {
		return l.Name, l.Version, gapTo(Protocol, l.Protocol)
	}
	return "", "", 0
}

// Routeable reports whether this pod may open a channel of the pod at key: one that does not
// speak this protocol is never routed to, the home page offers to update it instead.
func (d *Directory) Routeable(key string) bool {
	_, _, gap := d.GapOf(key)
	return gap == 0
}

// gapTo turns two protocol numbers into -1, 0 or 1.
func gapTo(own, other int) int {
	switch {
	case other < own:
		return -1
	case other > own:
		return 1
	}
	return 0
}

// fill adds the cached list of a pod to its view: its projects, and the time it published them.
// The projects are written with the field names of projects.list, so the page shows a cached list
// and a live LAN list the same way.
func (d *Directory) fill(v *PodView, key, name string) {
	l := d.lists[key]
	if l == nil {
		v.NoKey = true
		return
	}
	v.Name, v.Version = first(l.Name, name), first(l.Version, v.Version)
	v.Seen = d.seen[key]
	projects := []map[string]string{}
	for _, p := range l.Projects {
		projects = append(projects, map[string]string{"id": p.ID, "name": p.Title, "description": p.Description, "type": p.Type, "path": p.Path})
	}
	b, _ := json.Marshal(projects)
	v.Projects = b
}

func first(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
