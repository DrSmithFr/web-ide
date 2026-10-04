// Package tunnels forwards local ports to addresses reached from the SSH host of a project,
// like ssh -L. Tunnels belong to the pod, not to a window: they stay open while any window
// is connected, and the server closes them all a while after the last one leaves.
package tunnels

import (
	"context"
	"io"
	"net"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/DrSmithFr/web-ide/pod/internal/i18n"
)

// Spec is a tunnel as saved in .ide/tunnels.json.
type Spec struct {
	ID         string `json:"id"`
	RemoteHost string `json:"remoteHost"` // as seen from the SSH host
	RemotePort int    `json:"remotePort"`
	LocalPort  int    `json:"localPort"`
	LAN        bool   `json:"lan"`     // listens on every interface instead of 127.0.0.1
	Enabled    bool   `json:"enabled"` // opened with the project
}

// Check fills the defaults and validates the ports.
func (s *Spec) Check() error {
	if s.RemoteHost == "" {
		s.RemoteHost = "127.0.0.1"
	}
	if s.LocalPort == 0 {
		s.LocalPort = s.RemotePort
	}
	if s.RemotePort < 1 || s.RemotePort > 65535 || s.LocalPort < 1 || s.LocalPort > 65535 {
		return i18n.New("ports go from 1 to 65535")
	}
	return nil
}

func (s Spec) Remote() string { return net.JoinHostPort(s.RemoteHost, strconv.Itoa(s.RemotePort)) }

func (s Spec) Local() string {
	host := "127.0.0.1"
	if s.LAN {
		host = "0.0.0.0"
	}
	return net.JoinHostPort(host, strconv.Itoa(s.LocalPort))
}

// State is a tunnel with its live state.
type State struct {
	Spec
	Project string `json:"project"`
	Open    bool   `json:"open"`
	Error   string `json:"error,omitempty"`
	Conns   int    `json:"conns"`
}

// Dialer reaches an address from the SSH host.
type Dialer func(ctx context.Context, addr string) (net.Conn, error)

type key struct{ project, id string }

type tunnel struct {
	spec    Spec
	project string
	l       net.Listener
	conns   map[net.Conn]bool
}

type Manager struct {
	mu     sync.Mutex
	open   map[key]*tunnel
	errs   map[key]string
	notify func()
}

// New returns a manager calling changed after every change of the open tunnels.
func New(changed func()) *Manager {
	return &Manager{open: map[key]*tunnel{}, errs: map[key]string{}, notify: changed}
}

func (m *Manager) changed() {
	if m.notify != nil {
		m.notify()
	}
}

// Open starts listening for a tunnel (already open: closed and opened again with the spec).
func (m *Manager) Open(project string, s Spec, dial Dialer) error {
	m.close(key{project, s.ID})
	l, err := net.Listen("tcp", s.Local())
	k := key{project, s.ID}
	m.mu.Lock()
	if err != nil {
		m.errs[k] = err.Error()
		m.mu.Unlock()
		m.changed()
		return err
	}
	delete(m.errs, k)
	t := &tunnel{spec: s, project: project, l: l, conns: map[net.Conn]bool{}}
	m.open[k] = t
	m.mu.Unlock()
	go m.serve(t, dial)
	m.changed()
	return nil
}

func (m *Manager) serve(t *tunnel, dial Dialer) {
	for {
		c, err := t.l.Accept()
		if err != nil {
			return
		}
		go m.forward(t, c, dial)
	}
}

func (m *Manager) forward(t *tunnel, c net.Conn, dial Dialer) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	r, err := dial(ctx, t.spec.Remote())
	cancel()
	if err != nil {
		c.Close()
		return
	}
	m.mu.Lock()
	t.conns[c], t.conns[r] = true, true
	m.mu.Unlock()
	m.changed()
	done := make(chan struct{}, 2)
	pipe := func(dst, src net.Conn) {
		_, _ = io.Copy(dst, src)
		done <- struct{}{}
	}
	go pipe(r, c)
	go pipe(c, r)
	<-done
	c.Close()
	r.Close()
	<-done
	m.mu.Lock()
	delete(t.conns, c)
	delete(t.conns, r)
	m.mu.Unlock()
	m.changed()
}

// close stops a tunnel and its connections; true when it was open.
func (m *Manager) close(k key) bool {
	m.mu.Lock()
	t := m.open[k]
	delete(m.open, k)
	delete(m.errs, k)
	var conns []net.Conn
	if t != nil {
		for c := range t.conns {
			conns = append(conns, c)
		}
	}
	m.mu.Unlock()
	if t == nil {
		return false
	}
	t.l.Close()
	for _, c := range conns {
		c.Close()
	}
	return true
}

func (m *Manager) Close(project, id string) {
	if m.close(key{project, id}) {
		m.changed()
	}
}

// CloseAll closes every tunnel, of every project.
func (m *Manager) CloseAll() {
	m.mu.Lock()
	var keys []key
	for k := range m.open {
		keys = append(keys, k)
	}
	m.errs = map[key]string{}
	m.mu.Unlock()
	for _, k := range keys {
		m.close(k)
	}
	if len(keys) > 0 {
		m.changed()
	}
}

// IsOpen tells whether a tunnel listens.
func (m *Manager) IsOpen(project, id string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.open[key{project, id}] != nil
}

// State gives the live state of the saved tunnels of a project.
func (m *Manager) State(project string, specs []Spec) []State {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []State{}
	for _, s := range specs {
		st := State{Spec: s, Project: project, Error: m.errs[key{project, s.ID}]}
		if t := m.open[key{project, s.ID}]; t != nil {
			st.Open, st.Conns = true, len(t.conns)/2
		}
		out = append(out, st)
	}
	return out
}

// List gives the open tunnels of every project.
func (m *Manager) List() []State {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []State{}
	for k, t := range m.open {
		out = append(out, State{Spec: t.spec, Project: k.project, Open: true, Conns: len(t.conns) / 2})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Project != out[j].Project {
			return out[i].Project < out[j].Project
		}
		return out[i].LocalPort < out[j].LocalPort
	})
	return out
}
