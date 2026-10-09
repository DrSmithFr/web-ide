// Package console runs the terminals and tasks of a project. They survive page reloads: a
// reconnecting page gets the scrollback back. With a keeper (package keeper), they run in it
// and survive the restarts of the pod too: the pod adopts them again when it opens the project.
package console

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log"
	"sync"
	"time"

	"github.com/DrSmithFr/web-ide/pod/internal/execx"
	"github.com/DrSmithFr/web-ide/pod/internal/i18n"
	"github.com/DrSmithFr/web-ide/pod/internal/keeper"
)

const scrollbackMax = keeper.ScrollbackMax

type Info struct {
	ID      string   `json:"id"`
	Title   string   `json:"title"`
	Kind    string   `json:"kind"` // terminal | task
	Command []string `json:"command,omitempty"`
	Exited  bool     `json:"exited"`
	Code    int      `json:"code"`
	Started int64    `json:"started"`
}

// term is where a console runs: a local PTY, or a process of the keeper.
type term interface {
	Write([]byte) (int, error)
	Resize(cols, rows int) error
	Kill() error
}

type Console struct {
	Info
	term term
	mu   sync.Mutex
	buf  []byte
	// end is the offset after the last byte of output (buf ends there).
	end int64
	// att follows a process of the keeper.
	att *keeper.Attachment
}

// Output is called with each chunk of console output and its offset, and Exit with the code.
type Events struct {
	Output func(id string, data []byte, offset int64)
	Exit   func(id string, code int)
}

type Manager struct {
	mu       sync.Mutex
	runner   execx.Runner
	root     string
	ev       Events
	consoles map[string]*Console
	order    []string
	// keeper runs the consoles when set, for the processes of owner (the project).
	keeper *keeper.Client
	owner  string
}

func NewManager(r execx.Runner, root string, ev Events) *Manager {
	return &Manager{runner: r, root: root, ev: ev, consoles: map[string]*Console{}}
}

// UseKeeper runs the new consoles in the keeper, and adopts those it already runs for owner.
func (m *Manager) UseKeeper(c *keeper.Client, owner string) {
	m.mu.Lock()
	m.keeper, m.owner = c, owner
	m.mu.Unlock()
	m.adopt()
}

func newID() string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// meta is what the keeper keeps of a console, to rebuild it.
type meta struct {
	Console string   `json:"console"`
	Title   string   `json:"title"`
	Kind    string   `json:"kind"`
	Command []string `json:"command,omitempty"`
	Started int64    `json:"started"`
}

func (c *Console) meta() json.RawMessage {
	data, _ := json.Marshal(meta{Console: c.ID, Title: c.Title, Kind: c.Kind, Command: c.Command, Started: c.Started})
	return data
}

func (m *Manager) Create(kind, title string, command []string, cwd string, cols, rows int) (Info, error) {
	if cwd == "" {
		cwd = m.root
	}
	if cols <= 0 {
		cols, rows = 120, 30
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
	c := &Console{Info: Info{ID: newID(), Title: title, Kind: kind, Command: command, Started: time.Now().UnixMilli()}}
	m.mu.Lock()
	k := m.keeper
	m.mu.Unlock()
	if k != nil {
		p, err := k.Spawn(keeper.Spawn{Owner: m.owner, Argv: execx.ShellArgv(command), Dir: cwd, Env: execx.TermEnv, PTY: true, Cols: cols, Rows: rows, Meta: c.meta()})
		switch {
		case err == nil:
			c.term = &keeperTerm{c: k, id: p.ID}
			m.add(c)
			if err := m.follow(c, p.ID, 0); err != nil {
				return Info{}, err
			}
			return c.Info, nil
		case !errors.Is(err, keeper.ErrUnreachable):
			return Info{}, err
		}
		log.Printf("console: the keeper does not answer, %s runs in the pod", c.ID)
	}
	p, err := m.runner.StartPTY(command, cwd, cols, rows)
	if err != nil {
		return Info{}, err
	}
	c.term = p
	m.add(c)
	go m.pump(c, p)
	return c.Info, nil
}

func (m *Manager) add(c *Console) {
	m.mu.Lock()
	m.consoles[c.ID] = c
	m.order = append(m.order, c.ID)
	m.mu.Unlock()
}

// output keeps a chunk at offset in the scrollback and sends it.
func (m *Manager) output(c *Console, offset int64, chunk []byte, truncated bool) {
	c.mu.Lock()
	if truncated || offset != c.end {
		c.buf = nil // the keeper lost the start of it
	}
	c.buf = append(c.buf, chunk...)
	if over := len(c.buf) - scrollbackMax; over > 0 {
		c.buf = append([]byte(nil), c.buf[over:]...)
	}
	c.end = offset + int64(len(chunk))
	c.mu.Unlock()
	m.ev.Output(c.ID, chunk, offset)
}

func (m *Manager) exited(c *Console, code int) {
	c.mu.Lock()
	c.Exited, c.Code = true, code
	c.mu.Unlock()
	m.ev.Exit(c.ID, code)
}

func (m *Manager) pump(c *Console, p execx.PTY) {
	b := make([]byte, 32*1024)
	for {
		n, err := p.Read(b)
		if n > 0 {
			c.mu.Lock()
			off := c.end
			c.mu.Unlock()
			m.output(c, off, append([]byte(nil), b[:n]...), false)
		}
		if err != nil {
			break
		}
	}
	m.exited(c, p.Wait())
}

// follow attaches c to the process id of the keeper from the offset from.
func (m *Manager) follow(c *Console, id string, from int64) error {
	att, err := m.keeper.Attach(id, from,
		func(offset int64, data []byte, truncated bool) { m.output(c, offset, data, truncated) },
		func(code int) { m.exited(c, code) })
	if err != nil {
		return err
	}
	c.mu.Lock()
	c.att = att
	c.mu.Unlock()
	return nil
}

// adopt rebuilds the consoles the keeper runs for the project (the pod restarted), with
// their scrollback, in their order.
func (m *Manager) adopt() {
	procs, err := m.keeper.List(m.owner)
	if err != nil {
		log.Printf("console: the processes of the keeper are not listed: %v", err)
		return
	}
	for _, p := range procs {
		var mt meta
		if !p.PTY || json.Unmarshal(p.Meta, &mt) != nil || mt.Console == "" {
			continue
		}
		m.mu.Lock()
		_, known := m.consoles[mt.Console]
		m.mu.Unlock()
		if known {
			continue
		}
		c := &Console{Info: Info{ID: mt.Console, Title: mt.Title, Kind: mt.Kind, Command: mt.Command, Started: mt.Started}, term: &keeperTerm{c: m.keeper, id: p.ID}}
		c.end = p.Base
		m.add(c)
		if err := m.follow(c, p.ID, p.Base); err != nil {
			log.Printf("console: %s not adopted: %v", mt.Console, err)
		}
	}
}

func (m *Manager) get(id string) (*Console, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.consoles[id]
	if !ok {
		return nil, i18n.New("console not found")
	}
	return c, nil
}

func (m *Manager) Input(id string, data []byte) error {
	c, err := m.get(id)
	if err != nil {
		return err
	}
	_, err = c.term.Write(data)
	return err
}

func (m *Manager) Resize(id string, cols, rows int) error {
	c, err := m.get(id)
	if err != nil {
		return err
	}
	return c.term.Resize(cols, rows)
}

// Attach returns the console info and its scrollback.
func (m *Manager) Attach(id string) (Info, []byte, error) {
	info, data, _, err := m.Snapshot(id)
	return info, data, err
}

// Snapshot returns the console info, its scrollback and the offset where it ends: the
// output events after it start there.
func (m *Manager) Snapshot(id string) (Info, []byte, int64, error) {
	c, err := m.get(id)
	if err != nil {
		return Info{}, nil, 0, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.Info, append([]byte(nil), c.buf...), c.end, nil
}

func (m *Manager) Rename(id, title string) error {
	c, err := m.get(id)
	if err != nil {
		return err
	}
	c.mu.Lock()
	c.Title = title
	kt, _ := c.term.(*keeperTerm)
	data := c.meta()
	c.mu.Unlock()
	if kt != nil {
		return kt.c.SetMeta(kt.id, data)
	}
	return nil
}

func (m *Manager) remove(id string) (*Console, error) {
	c, err := m.get(id)
	if err != nil {
		return nil, err
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
	c.mu.Lock()
	att := c.att
	c.mu.Unlock()
	if att != nil {
		att.Stop()
	}
	return c, nil
}

// Close ends a console and its process.
func (m *Manager) Close(id string) error {
	c, err := m.remove(id)
	if err != nil {
		return err
	}
	return c.term.Kill()
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

// CloseAll ends every console (the project is closed).
func (m *Manager) CloseAll() {
	for _, i := range m.List() {
		_ = m.Close(i.ID)
	}
}

// Release leaves the consoles of the keeper running (the pod stops: it adopts them again
// when it starts) and ends the others.
func (m *Manager) Release() {
	for _, i := range m.List() {
		c, err := m.remove(i.ID)
		if err != nil {
			continue
		}
		if _, kept := c.term.(*keeperTerm); !kept {
			_ = c.term.Kill()
		}
	}
}

// keeperTerm is a console run by the keeper.
type keeperTerm struct {
	c  *keeper.Client
	id string
}

func (t *keeperTerm) Write(b []byte) (int, error) {
	if err := t.c.Input(t.id, b); err != nil {
		return 0, err
	}
	return len(b), nil
}

func (t *keeperTerm) Resize(cols, rows int) error { return t.c.Resize(t.id, cols, rows) }

// Kill hangs up the terminal and forgets the process.
func (t *keeperTerm) Kill() error { return t.c.Forget(t.id) }
