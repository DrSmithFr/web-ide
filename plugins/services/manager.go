package services

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io/fs"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"antennae.space/ide/lib/i18n"
	"antennae.space/ide/lib/identity"
	"antennae.space/ide/lib/tunnel"
	"antennae.space/ide/pod/store"
)

// File is the services the pod shares, in its data folder (docs/design/relay.md, *Services*).
const File = "services.json"

// Service is an HTTP service of this machine shared with the other pods of the account:
// always 127.0.0.1:<Port>.
type Service struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Port    int    `json:"port"`
	Enabled bool   `json:"enabled"`
}

// Attached is a pod that linked a service: who, since when, what it asked.
type Attached struct {
	Key      string `json:"key"`
	Name     string `json:"name"`
	Since    int64  `json:"since"` // ms
	Last     int64  `json:"last,omitempty"`
	Requests int64  `json:"requests"`
	Bytes    int64  `json:"bytes"`
	// Route is the way its last request came (the badge).
	Route tunnel.Route `json:"route"`
}

// View is a service with the pods attached to it, as the home page shows it.
type View struct {
	Service
	Attached []Attached `json:"attached"`
}

// Ref is a service as the other pods see it: no port, no address.
type Ref struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// Manager holds the shared services and the pods attached to them (in memory: a pod links again
// after a restart).
type Manager struct {
	st      *store.Store
	changed func()

	mu       sync.Mutex
	list     []Service
	attached map[string]map[string]*Attached // service id → pod key
	// cut are the pods this pod cut, by service: they attach again only when their user turns
	// the link on (relink), not by themselves.
	cut map[string]map[string]bool
}

// Load reads the services of the pod; changed is called after each change (an event to the
// windows, the list published again).
func Load(st *store.Store, changed func()) (*Manager, error) {
	m := &Manager{st: st, changed: changed, attached: map[string]map[string]*Attached{}, cut: map[string]map[string]bool{}}
	if err := st.ReadJSON(File, &m.list); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return m, err
	}
	return m, nil
}

// List is every service with its attached pods, by name.
func (m *Manager) List() []View {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []View{}
	for _, s := range m.list {
		v := View{Service: s, Attached: []Attached{}}
		for _, a := range m.attached[s.ID] {
			v.Attached = append(v.Attached, *a)
		}
		sort.Slice(v.Attached, func(i, j int) bool { return v.Attached[i].Name < v.Attached[j].Name })
		out = append(out, v)
	}
	sort.SliceStable(out, func(i, j int) bool { return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name) })
	return out
}

// Published are the services on, as the other pods see them.
func (m *Manager) Published() []Ref {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []Ref{}
	for _, s := range m.list {
		if s.Enabled {
			out = append(out, Ref{ID: s.ID, Name: s.Name})
		}
	}
	return out
}

// Save adds a service (no id) or changes one. Turned off, its pods are detached.
func (m *Manager) Save(s Service) (Service, error) {
	s.Name = strings.TrimSpace(s.Name)
	if s.Name == "" {
		return s, i18n.New("a service needs a name")
	}
	if s.Port < 1 || s.Port > 65535 {
		return s, i18n.Errorf("the port must be between 1 and 65535")
	}
	m.mu.Lock()
	i := slices.IndexFunc(m.list, func(o Service) bool { return o.ID == s.ID })
	if s.ID != "" && i < 0 {
		m.mu.Unlock()
		return s, i18n.New("this service no longer exists")
	}
	for _, o := range m.list {
		if o.Port == s.Port && o.ID != s.ID {
			m.mu.Unlock()
			return s, i18n.Errorf("the port %d is shared already, as %s", s.Port, o.Name)
		}
	}
	list := slices.Clone(m.list)
	if i < 0 {
		s.ID = newID()
		list = append(list, s)
	} else {
		list[i] = s
	}
	if err := m.st.WriteJSON(File, list); err != nil {
		m.mu.Unlock()
		return s, err
	}
	m.list = list
	if !s.Enabled {
		delete(m.attached, s.ID)
	}
	m.mu.Unlock()
	m.changed()
	return s, nil
}

// Delete stops sharing a service; its pods are detached.
func (m *Manager) Delete(id string) error {
	m.mu.Lock()
	list := slices.DeleteFunc(slices.Clone(m.list), func(o Service) bool { return o.ID == id })
	if err := m.st.WriteJSON(File, list); err != nil {
		m.mu.Unlock()
		return err
	}
	m.list = list
	delete(m.attached, id)
	m.mu.Unlock()
	m.changed()
	return nil
}

// Attach links a pod to a service that is on, and gives the port of the service (the default
// port of the link). A pod this pod cut attaches again only when its user asked (relink).
func (m *Manager) Attach(id string, pod *identity.Cert, route tunnel.Route, relink bool) (int, error) {
	m.mu.Lock()
	s, ok := m.find(id)
	if !ok || !s.Enabled {
		m.mu.Unlock()
		return 0, errOff
	}
	if m.cut[id][pod.Key] {
		if !relink {
			m.mu.Unlock()
			return 0, errCut
		}
		delete(m.cut[id], pod.Key)
	}
	if m.attached[id] == nil {
		m.attached[id] = map[string]*Attached{}
	}
	if a := m.attached[id][pod.Key]; a != nil {
		a.Name = pod.Name
	} else {
		m.attached[id][pod.Key] = &Attached{Key: pod.Key, Name: pod.Name, Since: time.Now().UnixMilli()}
	}
	m.attached[id][pod.Key].Route = route
	m.mu.Unlock()
	m.changed()
	return s.Port, nil
}

// Cut detaches a pod and keeps it out until its user turns its link on again.
func (m *Manager) Cut(id, key string) {
	m.mu.Lock()
	if m.cut[id] == nil {
		m.cut[id] = map[string]bool{}
	}
	m.cut[id][key] = true
	m.mu.Unlock()
	m.Detach(id, key)
}

// Detach unlinks a pod (it turned its link off, or this pod cut it).
func (m *Manager) Detach(id, key string) {
	m.mu.Lock()
	_, ok := m.attached[id][key]
	delete(m.attached[id], key)
	m.mu.Unlock()
	if ok {
		m.changed()
	}
}

// errOff: the service does not exist or is off (the same answer: a pod learns nothing more).
var errOff = errors.New("no such service")

// errCut: this pod cut the pod asking; its link turns off.
var errCut = errors.New("cut by this pod")

// errDetached: the pod did not link the service, or was cut.
var errDetached = errors.New("not attached to this service")

// target is the port of a service for a pod that may use it now.
func (m *Manager) target(id, key string) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.find(id)
	if !ok || !s.Enabled {
		return 0, errOff
	}
	if m.attached[id][key] == nil {
		return 0, errDetached
	}
	return s.Port, nil
}

// count adds a request, its bytes and its route to what a pod asked.
func (m *Manager) count(id, key string, requests, bytes int64, route tunnel.Route) {
	m.mu.Lock()
	a := m.attached[id][key]
	if a != nil {
		a.Requests += requests
		a.Bytes += bytes
		a.Last = time.Now().UnixMilli()
		if route.Name != "" {
			a.Route = route
		}
	}
	m.mu.Unlock()
}

func (m *Manager) find(id string) (Service, bool) {
	i := slices.IndexFunc(m.list, func(o Service) bool { return o.ID == id })
	if i < 0 {
		return Service{}, false
	}
	return m.list[i], true
}

func newID() string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
