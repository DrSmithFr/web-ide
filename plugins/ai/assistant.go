package ai

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"time"

	"github.com/DrSmithFr/web-ide/lib/i18n"
	"github.com/DrSmithFr/web-ide/plugins/ai/engine"
	"github.com/DrSmithFr/web-ide/plugins/ai/llm"
	"github.com/DrSmithFr/web-ide/pod/assistant"
)

// The assistant runs in the pod (docs/design/architecture.md): a conversation goes on
// when its window is closed, several run at once (a limit per model server), and the pages
// only show them and send what the user does. The pod is the only writer of a conversation
// while it runs; every change is saved and announced to the windows (assistant.update).

// assistantRun is a conversation the assistant is running.
type assistantRun struct {
	id      string
	loc     llm.ChatLocation
	root    string        // windows of this project and of its worktrees follow the conversation
	project string        // project whose runtime the tools use (a worktree for a development)
	lang    string        // language of the window that started it (errors)
	waited  time.Duration // approval wait of the tool call running (confirm)
	ctx     context.Context
	cancel  context.CancelFunc

	mu      sync.Mutex
	chat    *engine.Chat
	state   string // queued | running | waiting_user | compacting
	ahead   int    // conversations before this one for the model server (queued)
	approve chan bool
	// done: the run has ended; a message for it goes to the stored conversation.
	done bool
	// A run taken back after a restart of the pod (assistant_resume.go): the completion to read
	// again, the tool calls to end first, the command of one of them to follow.
	resumeStream *engine.Running
	resumeCalls  []engine.ToolCall
	follow       *engine.Running
}

func contextWithCancel() (context.Context, context.CancelFunc) {
	return context.WithCancel(context.Background())
}

// assistants are the conversations running in the pod.
type assistants struct {
	mu   sync.Mutex
	runs map[string]*assistantRun
	// slots: one channel per model server, its capacity the number of conversations it runs at once.
	slots   map[string]chan struct{}
	waiting map[string]int
}

// emitAssistant tells the windows of a project and of its worktrees.
func (s *A) emitAssistant(root, name string, data any) { s.pod.EmitFamily(root, name, data, "") }

// assistantRuntime is the runtime of the project of a run (opened when no window has it).
func (s *A) assistantRuntime(project string) (*runtimeRef, error) {
	rt, err := s.pod.Runtime(project)
	if err != nil {
		return nil, err
	}
	p, _ := s.Projects.Get(project)
	return &runtimeRef{rt: rt, project: p}, nil
}

// loadChat reads a conversation of a location.
func (s *A) loadChat(loc llm.ChatLocation, id string) (*engine.Chat, error) {
	return engine.LoadChat(s.LLM, loc, id)
}

// saveChat writes a conversation (its title from its first message when it has none) and
// lists it in its ticket.
func (s *A) saveChat(loc llm.ChatLocation, c *engine.Chat) error {
	c.Updated = time.Now().UnixMilli()
	if c.Created == 0 {
		c.Created = c.Updated
	}
	// The messages added since the last save (those of before the field were dated once).
	for _, m := range c.Messages {
		if m.At == 0 {
			m.At = c.Updated
		}
	}
	if c.Title == "" {
		for _, m := range c.Messages {
			if m.Role != "user" {
				continue
			}
			text := m.Display
			if text == "" {
				text = assistant.ContentText(m.Content)
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
		_ = s.kb().Kanban.LinkChat(kanbanLocOf(loc), c.Ticket.ID, c.ID, c.Ticket.Role, c.Title)
	}
	return nil
}

// update is what the windows receive about a conversation: its state, its fields, and its
// messages from an index (all of them with from 0, none with from -1).
type assistantUpdate struct {
	ID        string                 `json:"id"`
	State     string                 `json:"state"`
	Ahead     int                    `json:"ahead,omitempty"`
	Stream    string                 `json:"stream,omitempty"`
	Title     string                 `json:"title"`
	Mode      string                 `json:"mode,omitempty"`
	Ticket    *engine.TicketLink     `json:"ticket,omitempty"`
	Queue     []engine.QueuedMessage `json:"queue"`
	Approval  *engine.Approval       `json:"approval,omitempty"`
	ResetAt   int                    `json:"resetAt,omitempty"`
	Nested    []engine.NestedFile    `json:"nested,omitempty"`
	Server    string                 `json:"server"`
	Model     string                 `json:"model"`
	Parent    string                 `json:"parent,omitempty"`
	Agent     *engine.SubAgent       `json:"agent,omitempty"`
	Children  []string               `json:"children,omitempty"`
	Dismissed bool                   `json:"dismissed,omitempty"`
	From      int                    `json:"from"`
	Count     int                    `json:"count"`
	Messages  []*engine.Message      `json:"messages,omitempty"`
}

func updateOf(c *engine.Chat, state string, ahead, from int) assistantUpdate {
	u := assistantUpdate{ID: c.ID, State: state, Ahead: ahead, Title: c.Title, Mode: c.Mode, Ticket: c.Ticket, Queue: c.Queue, Approval: c.Approval,
		ResetAt: c.ResetAt, Nested: c.Nested, Server: c.Server, Model: c.Model, Parent: c.Parent, Agent: c.Agent, Children: c.Children, Dismissed: c.Dismissed, From: from, Count: len(c.Messages)}
	if u.Queue == nil {
		u.Queue = []engine.QueuedMessage{}
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
func (s *A) publish(r *assistantRun, from int) {
	if s.stopping() {
		return // what is saved is what the next pod takes back
	}
	if err := s.saveChat(r.loc, r.chat); err != nil {
		s.emitAssistant(r.root, "assistant.error", map[string]string{"id": r.id, "error": i18n.Translate(r.lang, err)})
	}
	s.emitAssistant(r.root, "assistant.update", updateOf(r.chat, r.state, r.ahead, from))
	s.noteAsks(r.root, r.chat, from)
}

// publishIdle saves a conversation that does not run and announces it.
func (s *A) publishIdle(loc llm.ChatLocation, root string, c *engine.Chat, from int) error {
	if err := s.saveChat(loc, c); err != nil {
		return err
	}
	s.emitAssistant(root, "assistant.update", updateOf(c, "idle", 0, from))
	s.noteAsks(root, c, from)
	return nil
}

// run returns the running conversation id, or nil.
func (s *A) run(id string) *assistantRun {
	s.assistants.mu.Lock()
	defer s.assistants.mu.Unlock()
	return s.assistants.runs[id]
}

// stateOf is the state of a conversation for the page.
func (s *A) stateOf(id string) (string, int) {
	if r := s.run(id); r != nil {
		r.mu.Lock()
		defer r.mu.Unlock()
		return r.state, r.ahead
	}
	return "idle", 0
}

// slot waits for a place on a model server: Parallel conversations at once (1 by default
// for a local server: one GPU). Returns the release, or the error of ctx.
func (s *A) slot(r *assistantRun, server string) (func(), error) {
	s.assistants.mu.Lock()
	ch := s.assistants.slots[server]
	if ch == nil {
		ch = make(chan struct{}, s.LLM.Parallel(server))
		s.assistants.slots[server] = ch
	}
	s.assistants.waiting[server]++
	ahead := s.assistants.waiting[server] - 1 - (cap(ch) - len(ch))
	s.assistants.mu.Unlock()
	defer func() {
		s.assistants.mu.Lock()
		s.assistants.waiting[server]--
		s.assistants.mu.Unlock()
	}()
	select {
	case ch <- struct{}{}:
		return func() { <-ch }, nil
	default:
	}
	r.mu.Lock()
	prev := r.state
	r.state, r.ahead = "queued", max(ahead, 0)
	s.emitAssistant(r.root, "assistant.update", updateOf(r.chat, r.state, r.ahead, -1))
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
