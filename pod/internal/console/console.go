// Package console runs the terminals and tasks of a project. They live in the pod and
// survive page reloads: a reconnecting page gets the scrollback back.
package console

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"sync"
	"time"

	"github.com/DrSmithFr/web-ide/pod/internal/execx"
)

const scrollbackMax = 512 * 1024

type Info struct {
	ID      string   `json:"id"`
	Title   string   `json:"title"`
	Kind    string   `json:"kind"` // terminal | task
	Command []string `json:"command,omitempty"`
	Exited  bool     `json:"exited"`
	Code    int      `json:"code"`
	Started int64    `json:"started"`
}

type Console struct {
	Info
	pty execx.PTY
	mu  sync.Mutex
	buf []byte
}

// Output is called with each chunk of console output, and exit with the code.
type Events struct {
	Output func(id string, data []byte)
	Exit   func(id string, code int)
}

type Manager struct {
	mu       sync.Mutex
	runner   execx.Runner
	root     string
	ev       Events
	consoles map[string]*Console
	order    []string
}

func NewManager(r execx.Runner, root string, ev Events) *Manager {
	return &Manager{runner: r, root: root, ev: ev, consoles: map[string]*Console{}}
}

func newID() string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func (m *Manager) Create(kind, title string, command []string, cwd string, cols, rows int) (Info, error) {
	if cwd == "" {
		cwd = m.root
	}
	if cols <= 0 {
		cols, rows = 120, 30
	}
	p, err := m.runner.StartPTY(command, cwd, cols, rows)
	if err != nil {
		return Info{}, err
	}
	if kind == "" {
		kind = "terminal"
	}
	if title == "" {
		title = "Terminal"
		if len(command) > 0 {
			title = execx.Join(command)
		}
	}
	c := &Console{Info: Info{ID: newID(), Title: title, Kind: kind, Command: command, Started: time.Now().UnixMilli()}, pty: p}
	m.mu.Lock()
	m.consoles[c.ID] = c
	m.order = append(m.order, c.ID)
	m.mu.Unlock()
	go m.pump(c)
	return c.Info, nil
}

func (m *Manager) pump(c *Console) {
	b := make([]byte, 32*1024)
	for {
		n, err := c.pty.Read(b)
		if n > 0 {
			chunk := append([]byte(nil), b[:n]...)
			c.mu.Lock()
			c.buf = append(c.buf, chunk...)
			if over := len(c.buf) - scrollbackMax; over > 0 {
				c.buf = append([]byte(nil), c.buf[over:]...)
			}
			c.mu.Unlock()
			m.ev.Output(c.ID, chunk)
		}
		if err != nil {
			break
		}
	}
	code := c.pty.Wait()
	c.mu.Lock()
	c.Exited, c.Code = true, code
	c.mu.Unlock()
	m.ev.Exit(c.ID, code)
}

func (m *Manager) get(id string) (*Console, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.consoles[id]
	if !ok {
		return nil, errors.New("console introuvable")
	}
	return c, nil
}

func (m *Manager) Input(id string, data []byte) error {
	c, err := m.get(id)
	if err != nil {
		return err
	}
	_, err = c.pty.Write(data)
	return err
}

func (m *Manager) Resize(id string, cols, rows int) error {
	c, err := m.get(id)
	if err != nil {
		return err
	}
	return c.pty.Resize(cols, rows)
}

// Attach returns the console info and its scrollback.
func (m *Manager) Attach(id string) (Info, []byte, error) {
	c, err := m.get(id)
	if err != nil {
		return Info{}, nil, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.Info, append([]byte(nil), c.buf...), nil
}

func (m *Manager) Rename(id, title string) error {
	c, err := m.get(id)
	if err != nil {
		return err
	}
	c.mu.Lock()
	c.Title = title
	c.mu.Unlock()
	return nil
}

func (m *Manager) Close(id string) error {
	c, err := m.get(id)
	if err != nil {
		return err
	}
	m.mu.Lock()
	delete(m.consoles, id)
	for i, o := range m.order {
		if o == id {
			m.order = append(m.order[:i], m.order[i+1:]...)
			break
		}
	}
	m.mu.Unlock()
	return c.pty.Kill()
}

func (m *Manager) List() []Info {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Info, 0, len(m.order))
	for _, id := range m.order {
		c := m.consoles[id]
		c.mu.Lock()
		out = append(out, c.Info)
		c.mu.Unlock()
	}
	return out
}

func (m *Manager) CloseAll() {
	for _, i := range m.List() {
		_ = m.Close(i.ID)
	}
}
