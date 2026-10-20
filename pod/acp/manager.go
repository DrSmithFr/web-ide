// Package acp runs CLI agents (Claude Code, opencode, pi) through the Agent Client Protocol:
// JSON-RPC over the standard input and output of one process per session. The processes run
// in the keeper when there is one, so they survive the pod: the state of each session (its
// thread and where its output was read up to) is saved in a file, and the next pod follows
// the process again from there. A process that ends is started again with its session loaded.
package acp

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/DrSmithFr/web-ide/lib/i18n"
	"github.com/DrSmithFr/web-ide/pod/keeper"
)

type Deps struct {
	// Keeper runs the agents (nil: the pod runs them, they end with it).
	Keeper *keeper.Client
	// Owner is the project, for the processes of the keeper.
	Owner string
	// Dir holds the files of the sessions of the project (<data>/agents/<project>).
	Dir string
	// Root is the folder the agents work in.
	Root    string
	Catalog Catalog
	// Command overrides the command line of an agent (tests).
	Command func(agent string) ([]string, error)
	Emit    func(event string, data any)
	Version string
	// MCP are the MCP servers given to the sessions (the one of the IDE), to the agents that
	// support them over HTTP.
	MCP []MCPServer
}

// MCPServer is an MCP server over HTTP as ACP passes it in session/new.
type MCPServer struct {
	Type    string   `json:"type"`
	Name    string   `json:"name"`
	URL     string   `json:"url"`
	Headers []Header `json:"headers"`
}

type Header struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// Options of a new session.
type Options struct {
	// Title replaces the name of the agent (and the title the agent gives).
	Title string
	// Ticket the session works on.
	Ticket int64
	// Prompt is sent once the session is open.
	Prompt string
}

type Manager struct {
	Deps
	mu       sync.Mutex
	sessions map[string]*Session
	// dirty: the sessions to save, written a moment after their last change.
	dirty map[*Session]bool
	timer *time.Timer
}

func NewManager(d Deps) *Manager {
	m := &Manager{Deps: d, sessions: map[string]*Session{}, dirty: map[*Session]bool{}}
	m.restore()
	return m
}

func (m *Manager) emit(event string, data any) {
	if m.Emit != nil {
		m.Emit(event, data)
	}
}

func (m *Manager) command(agent string) ([]string, error) {
	if m.Command != nil {
		return m.Command(agent)
	}
	return m.Catalog.Command(agent)
}

func newID() string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func (m *Manager) file(id string) string    { return filepath.Join(m.Dir, id+".json") }
func (m *Manager) logFile(id string) string { return filepath.Join(m.Dir, id+".log") }

// spawn starts the process of s (under s.mu); its error output goes to the log of s.
func (m *Manager) spawn(s *Session, argv []string, gen int) (proc, string, error) {
	if err := os.MkdirAll(m.Dir, 0o700); err != nil {
		return nil, "", err
	}
	wrapped := append([]string{"sh", "-c", `exec "$@" 2>>"$0"`, m.logFile(s.info.ID)}, argv...)
	ev := events{
		line: func(line []byte, next int64) { s.line(gen, line, next) },
		exit: func(code int) { s.exit(gen, code) },
	}
	env := []string{"PATH=" + os.Getenv("PATH")}
	if m.Keeper != nil {
		id, err := spawnKeeper(m.Keeper, m.Owner, s.info.ID, wrapped, s.info.Cwd, env)
		if err != nil {
			return nil, "", err
		}
		p, err := followKeeper(m.Keeper, id, 0, s.q, ev)
		if err != nil {
			_ = m.Keeper.Forget(id)
			return nil, "", err
		}
		return p, id, nil
	}
	p, err := spawnLocal(wrapped, s.info.Cwd, nil, s.q, ev)
	return p, "", err
}

// stderrTail is the end of the error output of a session.
func (m *Manager) stderrTail(id string) string {
	data, err := os.ReadFile(m.logFile(id))
	if err != nil {
		return ""
	}
	if len(data) > 2000 {
		data = data[len(data)-2000:]
	}
	return strings.TrimSpace(string(data))
}

// ---------- saving ----------

// save writes s a moment later (under s.mu: it only marks it).
func (m *Manager) save(s *Session) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.dirty[s] = true
	if m.timer == nil {
		m.timer = time.AfterFunc(300*time.Millisecond, m.flush)
	}
}

func (m *Manager) flush() {
	m.mu.Lock()
	list := make([]*Session, 0, len(m.dirty))
	for s := range m.dirty {
		list = append(list, s)
	}
	m.dirty, m.timer = map[*Session]bool{}, nil
	m.mu.Unlock()
	for _, s := range list {
		s.mu.Lock()
		closed, snap := s.closed, s.snapshot()
		if !closed {
			m.write(snap)
		}
		s.mu.Unlock()
	}
}

func (m *Manager) write(snap saved) {
	data, err := json.Marshal(snap)
	if err != nil {
		return
	}
	if err := os.MkdirAll(m.Dir, 0o700); err != nil {
		log.Printf("acp: %v", err)
		return
	}
	tmp := m.file(snap.Info.ID) + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		log.Printf("acp: %v", err)
		return
	}
	_ = os.Rename(tmp, m.file(snap.Info.ID))
}

// restore takes back the sessions of the files: the process of the keeper that still runs is
// followed from where it was read, the others are started again with their session loaded.
func (m *Manager) restore() {
	files, _ := filepath.Glob(filepath.Join(m.Dir, "*.json"))
	procs := map[string]keeper.Proc{}
	if m.Keeper != nil {
		list, err := m.Keeper.List(m.Owner)
		if err != nil {
			log.Printf("acp: the processes of the keeper are not listed: %v", err)
		}
		for _, p := range list {
			var mt procMeta
			if !p.PTY && json.Unmarshal(p.Meta, &mt) == nil && mt.Agent != "" {
				procs[p.ID] = p
			}
		}
	}
	for _, f := range files {
		var sv saved
		data, err := os.ReadFile(f)
		if err != nil || json.Unmarshal(data, &sv) != nil || sv.Info.ID == "" {
			continue
		}
		s := m.newSession(sv.Info)
		s.th.Entries, s.acp, s.canLoad, s.nextID, s.first = sv.Entries, sv.ACP, sv.CanLoad, sv.NextID, sv.First
		s.caps, s.queue, s.images = sv.Caps, sv.Queue, sv.Images
		s.reportedCost, s.subscription = sv.ReportedCost, sv.Subscription
		s.th.index()
		m.sessions[s.info.ID] = s
		if p, ok := procs[sv.Proc]; ok {
			delete(procs, sv.Proc)
			if m.adopt(s, sv, p) {
				continue
			}
		}
		if sv.Info.State == Failed {
			continue // stopped by its crashes: the user restarts it
		}
		// Its process is gone: what it was doing is lost.
		s.prompt = 0
		for i := range s.th.Entries {
			if e := &s.th.Entries[i]; isOpenAsk(*e) {
				e.Outcome = "cancelled"
			}
		}
		go s.start(true)
	}
	// Processes of sessions closed meanwhile.
	for id := range procs {
		_ = m.Keeper.Forget(id)
	}
}

// adopt follows the process of the keeper of a restored session again.
func (m *Manager) adopt(s *Session, sv saved, p keeper.Proc) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if sv.Info.State == Starting {
		_ = m.Keeper.Forget(p.ID)
		return false
	}
	s.gen++
	gen := s.gen
	ev := events{
		line: func(line []byte, next int64) { s.line(gen, line, next) },
		exit: func(code int) { s.exit(gen, code) },
	}
	kp, err := followKeeper(m.Keeper, p.ID, sv.Offset, s.q, ev)
	if err != nil {
		log.Printf("acp: %s not adopted: %v", s.info.ID, err)
		_ = m.Keeper.Forget(p.ID)
		return false
	}
	s.proc, s.procID, s.offset, s.prompt, s.turn = kp, p.ID, sv.Offset, sv.Prompt, sv.Turn
	return true
}

func (m *Manager) newSession(info Info) *Session {
	return &Session{m: m, q: newQueue(), info: info, pending: map[int64]chan response{}, th: thread{tools: map[string]int{}, plan: -1}}
}

// ---------- API ----------

// Create opens a session with an agent, in cwd (the project by default).
func (m *Manager) Create(agent, cwd string) (Info, error) { return m.CreateWith(agent, cwd, Options{}) }

// CreateWith opens a session with options (a ticket's session: its title, its briefing).
func (m *Manager) CreateWith(agent, cwd string, o Options) (Info, error) {
	a, ok := agentByID(agent)
	if !ok {
		return Info{}, i18n.Errorf("unknown agent: %s", agent)
	}
	if _, err := m.command(agent); err != nil {
		return Info{}, err
	}
	if cwd == "" {
		cwd = m.Root
	}
	title := a.Name
	if o.Title != "" {
		title = o.Title
	}
	s := m.newSession(Info{ID: newID(), Agent: agent, Name: a.Name, Title: title, Cwd: cwd, State: Starting, Started: time.Now().UnixMilli(), Ticket: o.Ticket})
	s.first = o.Prompt
	_ = os.MkdirAll(m.Dir, 0o700)
	_ = os.WriteFile(m.logFile(s.info.ID), nil, 0o600)
	m.mu.Lock()
	m.sessions[s.info.ID] = s
	m.mu.Unlock()
	s.mu.Lock()
	m.write(s.snapshot())
	s.mu.Unlock()
	go s.start(false)
	return s.Info(), nil
}

func (m *Manager) Get(id string) (*Session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sessions[id]
	if !ok {
		return nil, i18n.New("agent session not found")
	}
	return s, nil
}

// List returns the sessions in the order they were opened.
func (m *Manager) List() []Info {
	m.mu.Lock()
	list := make([]*Session, 0, len(m.sessions))
	for _, s := range m.sessions {
		list = append(list, s)
	}
	m.mu.Unlock()
	out := make([]Info, 0, len(list))
	for _, s := range list {
		out = append(out, s.Info())
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Started != out[j].Started {
			return out[i].Started < out[j].Started
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// Close ends a session: its process, its file.
func (m *Manager) Close(id string) error {
	s, err := m.Get(id)
	if err != nil {
		return err
	}
	m.mu.Lock()
	delete(m.sessions, id)
	delete(m.dirty, s)
	m.mu.Unlock()
	s.mu.Lock()
	s.closed = true
	if s.proc != nil {
		s.proc.Kill()
		s.proc = nil
	}
	for pid, ch := range s.pending {
		close(ch)
		delete(s.pending, pid)
	}
	s.mu.Unlock()
	s.q.close()
	removeQuiet(m.file(id))
	_ = os.RemoveAll(filepath.Join(m.Dir, id))
	removeQuiet(m.logFile(id))
	m.emit("agents.closed", map[string]string{"id": id})
	return nil
}

// CloseAll ends every session (the project is closed).
func (m *Manager) CloseAll() {
	for _, i := range m.List() {
		_ = m.Close(i.ID)
	}
}

// Release saves the sessions and leaves their processes to the next pod (the pod stops);
// without keeper they end, and the next pod starts them again.
func (m *Manager) Release() {
	m.mu.Lock()
	list := make([]*Session, 0, len(m.sessions))
	for _, s := range m.sessions {
		list = append(list, s)
	}
	if m.timer != nil {
		m.timer.Stop()
		m.timer = nil
	}
	m.dirty = map[*Session]bool{}
	m.mu.Unlock()
	for _, s := range list {
		s.mu.Lock()
		s.released = true
		if s.proc != nil {
			s.proc.Detach()
		}
		m.write(s.snapshot())
		s.mu.Unlock()
		s.q.close()
	}
}

// Purge ends the sessions of a project that is not open (the worktree of a ticket removed):
// their processes in the keeper and their files.
func Purge(k *keeper.Client, owner, dir string) {
	if k != nil {
		if list, err := k.List(owner); err == nil {
			for _, p := range list {
				var mt procMeta
				if !p.PTY && json.Unmarshal(p.Meta, &mt) == nil && mt.Agent != "" {
					_ = k.Forget(p.ID)
				}
			}
		}
	}
	_ = os.RemoveAll(dir)
}

// Find reads the saved sessions of a project (open or not) for the one of a ticket.
func Find(dir string, ticket int64) (Info, []Entry, bool) {
	files, _ := filepath.Glob(filepath.Join(dir, "*.json"))
	var best saved
	found := false
	for _, f := range files {
		var sv saved
		data, err := os.ReadFile(f)
		if err != nil || json.Unmarshal(data, &sv) != nil || sv.Info.Ticket != ticket {
			continue
		}
		if !found || sv.Info.Started > best.Info.Started {
			best, found = sv, true
		}
	}
	return best.Info, best.Entries, found
}
