package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/DrSmithFr/web-ide/pod/internal/agent"
	"github.com/DrSmithFr/web-ide/pod/internal/i18n"
	"github.com/DrSmithFr/web-ide/pod/internal/llm"
	"github.com/DrSmithFr/web-ide/pod/internal/sshx"
)

// The agent of the assistant runs in the pod (docs/architecture.md): a conversation goes on
// when its window is closed, several run at once (a limit per model server), and the pages
// only show them and send what the user does. The pod is the only writer of a conversation
// while it runs; every change is saved and announced to the windows (agent.update).

// agentRun is a conversation the agent is running.
type agentRun struct {
	id      string
	loc     llm.ChatLocation
	root    string // windows of this project and of its worktrees follow the conversation
	project string // project whose runtime the tools use (a worktree for a development)
	lang    string // language of the window that started it (errors)
	ctx     context.Context
	cancel  context.CancelFunc

	mu      sync.Mutex
	chat    *agent.Chat
	state   string // queued | running | waiting_user | compacting
	ahead   int    // conversations before this one for the model server (queued)
	approve chan bool
	// done: the run has ended; a message for it goes to the stored conversation.
	done bool
}

// agents are the conversations running in the pod.
type agents struct {
	mu   sync.Mutex
	runs map[string]*agentRun
	// slots: one channel per model server, its capacity the number of conversations it runs at once.
	slots   map[string]chan struct{}
	waiting map[string]int
	// ui: tools waiting for a window (open_file, focus), by request id.
	ui map[string]chan uiResult
}

func newID() string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return strconv.FormatInt(time.Now().UnixMilli(), 36) + hex.EncodeToString(b)[:6]
}

// chatLoc is where the conversations of a project are kept, and the project whose windows
// follow them: a worktree keeps its conversations with those of its parent.
func (s *Server) chatLoc(projectID string) (llm.ChatLocation, string, error) {
	root, err := s.kanbanProject(projectID)
	if err != nil {
		return llm.ChatLocation{}, "", err
	}
	l := llm.ChatLocation{Project: root.ID}
	if root.Type == "local" {
		l.IdeDir = filepath.Join(root.Path, ".ide")
	}
	return l, root.ID, nil
}

// emitAgent tells the windows of a project and of its worktrees.
func (s *Server) emitAgent(root, name string, data any) {
	s.mu.Lock()
	var targets []*Client
	for c := range s.clients {
		if c.project == root {
			targets = append(targets, c)
		} else if p, ok := s.Projects.Get(c.project); ok && p.Parent == root {
			targets = append(targets, c)
		}
	}
	s.mu.Unlock()
	for _, c := range targets {
		c.push(name, data)
	}
}

// agentRuntime is the runtime of the project of a run (opened when no window has it).
func (s *Server) agentRuntime(project string) (*runtimeRef, error) {
	rt, err := s.openRuntime(project, sshx.Creds{})
	if err != nil {
		return nil, err
	}
	p, _ := s.Projects.Get(project)
	return &runtimeRef{rt: rt, project: p}, nil
}

// loadChat reads a conversation of a location.
func (s *Server) loadChat(loc llm.ChatLocation, id string) (*agent.Chat, error) {
	raw, err := s.LLM.GetChat(loc, id)
	if err != nil {
		return nil, err
	}
	var c agent.Chat
	if err := json.Unmarshal(raw, &c); err != nil {
		return nil, err
	}
	return &c, nil
}

// saveChat writes a conversation (its title from its first message when it has none) and
// lists it in its ticket.
func (s *Server) saveChat(loc llm.ChatLocation, c *agent.Chat) error {
	c.Updated = time.Now().UnixMilli()
	if c.Created == 0 {
		c.Created = c.Updated
	}
	if c.Title == "" {
		for _, m := range c.Messages {
			if m.Role != "user" {
				continue
			}
			text := m.Display
			if text == "" {
				text = agent.ContentText(m.Content)
			}
			c.Title = strings.Join(strings.Fields(text), " ")
			if r := []rune(c.Title); len(r) > 80 {
				c.Title = string(r[:80])
			}
			break
		}
		if c.Title == "" {
			c.Title = "Conversation"
		}
	}
	data, err := json.Marshal(c)
	if err != nil {
		return err
	}
	if err := s.LLM.SaveChat(loc, data); err != nil {
		return err
	}
	if c.Ticket != nil {
		_ = s.Kanban.LinkChat(kanbanLocOf(loc), c.Ticket.ID, c.ID, c.Ticket.Role, c.Title)
	}
	return nil
}

// update is what the windows receive about a conversation: its state, its fields, and its
// messages from an index (all of them with from 0, none with from -1).
type agentUpdate struct {
	ID       string                `json:"id"`
	State    string                `json:"state"`
	Ahead    int                   `json:"ahead,omitempty"`
	Stream   string                `json:"stream,omitempty"`
	Title    string                `json:"title"`
	Mode     string                `json:"mode,omitempty"`
	Ticket   *agent.TicketLink     `json:"ticket,omitempty"`
	Queue    []agent.QueuedMessage `json:"queue"`
	Approval *agent.Approval       `json:"approval,omitempty"`
	ResetAt  int                   `json:"resetAt,omitempty"`
	Server   string                `json:"server"`
	Model    string                `json:"model"`
	Parent   string                `json:"parent,omitempty"`
	Agent    *agent.SubAgent       `json:"agent,omitempty"`
	Children []string              `json:"children,omitempty"`
	From     int                   `json:"from"`
	Count    int                   `json:"count"`
	Messages []*agent.Message      `json:"messages,omitempty"`
}

func updateOf(c *agent.Chat, state string, ahead, from int) agentUpdate {
	u := agentUpdate{ID: c.ID, State: state, Ahead: ahead, Title: c.Title, Mode: c.Mode, Ticket: c.Ticket, Queue: c.Queue, Approval: c.Approval,
		ResetAt: c.ResetAt, Server: c.Server, Model: c.Model, Parent: c.Parent, Agent: c.Agent, Children: c.Children, From: from, Count: len(c.Messages)}
	if u.Queue == nil {
		u.Queue = []agent.QueuedMessage{}
	}
	if c.Running != nil {
		u.Stream = c.Running.Stream
	}
	if from >= 0 && from < len(c.Messages) {
		u.Messages = c.Messages[from:]
	}
	return u
}

// publish saves the conversation of a run and sends its changes from a message index.
// Called with r.mu held.
func (s *Server) publish(r *agentRun, from int) {
	if err := s.saveChat(r.loc, r.chat); err != nil {
		s.emitAgent(r.root, "agent.error", map[string]string{"id": r.id, "error": i18n.Translate(r.lang, err)})
	}
	s.emitAgent(r.root, "agent.update", updateOf(r.chat, r.state, r.ahead, from))
}

// publishIdle saves a conversation that does not run and announces it.
func (s *Server) publishIdle(loc llm.ChatLocation, root string, c *agent.Chat, from int) error {
	if err := s.saveChat(loc, c); err != nil {
		return err
	}
	s.emitAgent(root, "agent.update", updateOf(c, "idle", 0, from))
	return nil
}

// run returns the running conversation id, or nil.
func (s *Server) run(id string) *agentRun {
	s.agents.mu.Lock()
	defer s.agents.mu.Unlock()
	return s.agents.runs[id]
}

// stateOf is the state of a conversation for the page.
func (s *Server) stateOf(id string) (string, int) {
	if r := s.run(id); r != nil {
		r.mu.Lock()
		defer r.mu.Unlock()
		return r.state, r.ahead
	}
	return "idle", 0
}

// slot waits for a place on a model server: Parallel conversations at once (1 by default
// for a local server: one GPU). Returns the release, or the error of ctx.
func (s *Server) slot(r *agentRun, server string) (func(), error) {
	s.agents.mu.Lock()
	ch := s.agents.slots[server]
	if ch == nil {
		ch = make(chan struct{}, s.LLM.Parallel(server))
		s.agents.slots[server] = ch
	}
	s.agents.waiting[server]++
	ahead := s.agents.waiting[server] - 1 - (cap(ch) - len(ch))
	s.agents.mu.Unlock()
	defer func() {
		s.agents.mu.Lock()
		s.agents.waiting[server]--
		s.agents.mu.Unlock()
	}()
	select {
	case ch <- struct{}{}:
		return func() { <-ch }, nil
	default:
	}
	r.mu.Lock()
	prev := r.state
	r.state, r.ahead = "queued", max(ahead, 0)
	s.emitAgent(r.root, "agent.update", updateOf(r.chat, r.state, r.ahead, -1))
	r.mu.Unlock()
	select {
	case ch <- struct{}{}:
		r.mu.Lock()
		r.state, r.ahead = prev, 0
		r.mu.Unlock()
		return func() { <-ch }, nil
	case <-r.ctx.Done():
		return nil, r.ctx.Err()
	}
}
