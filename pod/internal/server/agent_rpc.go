package server

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/DrSmithFr/web-ide/pod/internal/agent"
	"github.com/DrSmithFr/web-ide/pod/internal/i18n"
	"github.com/DrSmithFr/web-ide/pod/internal/llm"
)

// What the pages ask the agent: open a conversation (and follow it), send a message,
// answer questions, a plan or an approval, stop, resume, retry, compact.

// chatCtx is a conversation addressed by a window: where it is stored and who follows it.
type chatCtx struct {
	loc  llm.ChatLocation
	root string
}

func (s *Server) chatOf(c *Client) (chatCtx, error) {
	loc, root, err := s.chatLoc(c.project)
	return chatCtx{loc, root}, err
}

// change applies f to a conversation: under the lock of its run when it runs (then the run
// saves and announces it), else loaded, changed, saved and announced here. f returns the
// index of the first message it changed (-1: none).
func (s *Server) change(cc chatCtx, id string, f func(c *agent.Chat, r *agentRun) (int, error)) error {
	if r := s.run(id); r != nil {
		r.mu.Lock()
		defer r.mu.Unlock()
		from, err := f(r.chat, r)
		if err != nil {
			return err
		}
		s.publish(r, from)
		return nil
	}
	c, err := s.loadChat(cc.loc, id)
	if err != nil {
		return err
	}
	from, err := f(c, nil)
	if err != nil {
		return err
	}
	return s.publishIdle(cc.loc, cc.root, c, from)
}

// skipQuestions: a message sent instead of answering leaves the pending questions (and a
// capture of the screen waiting for the user) aside.
func skipQuestions(c *agent.Chat) int {
	from := -1
	for i, m := range c.Messages {
		if m.Capture == "pending" {
			m.Capture, m.Status = "skipped", "denied"
			m.Content = agent.String("The user did not share their screen; their message follows.")
			m.Summary = agent.T("capture not shared", nil).Raw()
			if from < 0 {
				from = i
			}
		}
		if m.AskState == "pending" {
			m.AskState = "skipped"
			m.Content = agent.String("The user did not answer these questions; their message follows.")
			m.Summary = agent.T("questions not answered", nil).Raw()
			if from < 0 {
				from = i
			}
		}
	}
	return from
}

func lastUserIndex(c *agent.Chat) int {
	i := len(c.Messages) - 1
	for i >= 0 && (c.Messages[i].Role != "user" || c.Messages[i].Kind == "summary") {
		i--
	}
	return i
}

func minFrom(a, b int) int {
	switch {
	case a < 0:
		return b
	case b < 0:
		return a
	}
	return min(a, b)
}

// openChat reads a conversation for a window. A conversation left running by a pod that
// stopped is closed as interrupted.
func (s *Server) openChat(cc chatCtx, id string) (*agent.Chat, error) {
	if r := s.run(id); r != nil {
		r.mu.Lock()
		defer r.mu.Unlock()
		data, _ := json.Marshal(r.chat)
		var copy agent.Chat
		_ = json.Unmarshal(data, &copy)
		return &copy, nil
	}
	c, err := s.loadChat(cc.loc, id)
	if err != nil {
		return nil, err
	}
	if c.Running != nil || c.Approval != nil {
		c.Running, c.Approval = nil, nil
		closeToolCalls(c, "Interrupted: the pod stopped before this tool ended.")
		c.Messages = append(c.Messages, &agent.Message{Role: "assistant", Error: "Interrupted: the pod stopped."})
		_ = s.saveChat(cc.loc, c)
	}
	return c, nil
}

func (s *Server) registerAgent() {
	type idArg struct {
		ID string `json:"id"`
	}
	withChat := func(f func(ctx context.Context, c *Client, cc chatCtx, p json.RawMessage) (any, error)) handler {
		return func(ctx context.Context, c *Client, p json.RawMessage) (any, error) {
			cc, err := s.chatOf(c)
			if err != nil {
				return nil, err
			}
			return f(ctx, c, cc, p)
		}
	}
	// agent.open: a conversation and its state; the window follows it from now on.
	s.handle("agent.open", withChat(func(ctx context.Context, c *Client, cc chatCtx, p json.RawMessage) (any, error) {
		a, err := bind[idArg](p)
		if err != nil {
			return nil, err
		}
		c.watch(a.ID)
		chat, err := s.openChat(cc, a.ID)
		if err != nil {
			return nil, err
		}
		state, ahead := s.stateOf(a.ID)
		return map[string]any{"chat": chat, "state": state, "ahead": ahead}, nil
	}))
	// agent.watch: the conversation shown by the window ("" for none).
	s.handle("agent.watch", func(ctx context.Context, c *Client, p json.RawMessage) (any, error) {
		a, err := bind[idArg](p)
		if err != nil {
			return nil, err
		}
		c.watch(a.ID)
		return nil, nil
	})
	// agent.states: the conversations running in the pod, with their state.
	s.handle("agent.states", withChat(func(ctx context.Context, c *Client, cc chatCtx, p json.RawMessage) (any, error) {
		out := map[string]string{}
		s.agents.mu.Lock()
		runs := make([]*agentRun, 0, len(s.agents.runs))
		for _, r := range s.agents.runs {
			if r.root == cc.root {
				runs = append(runs, r)
			}
		}
		s.agents.mu.Unlock()
		for _, r := range runs {
			r.mu.Lock()
			out[r.id] = r.state
			r.mu.Unlock()
		}
		return out, nil
	}))
	// agent.send: a message of the user (queued while the conversation runs).
	s.handle("agent.send", withChat(func(ctx context.Context, c *Client, cc chatCtx, p json.RawMessage) (any, error) {
		a, err := bind[struct {
			ID          string            `json:"id"`
			Text        string            `json:"text"`
			Parts       json.RawMessage   `json:"parts"`
			Attachments json.RawMessage   `json:"attachments"`
			Display     string            `json:"display"`
			Server      string            `json:"server"`
			Model       string            `json:"model"`
			Mode        string            `json:"mode"`
			Options     *agent.Options    `json:"options"`
			Ticket      *agent.TicketLink `json:"ticket"`
			Title       string            `json:"title"`
			// Project: where the tools work (a worktree of the project of the window).
			Project string `json:"project"`
			// From: the conversation restarts from this message (an edited message).
			From *int `json:"from"`
		}](p)
		if err != nil {
			return nil, err
		}
		if a.ID == "" {
			return nil, i18n.New("conversation id is missing")
		}
		project := c.project
		if a.Project != "" {
			if _, root, err := s.chatLoc(a.Project); err != nil || root != cc.root {
				return nil, i18n.New("project not found")
			}
			project = a.Project
		}
		if r := s.run(a.ID); r != nil {
			r.mu.Lock()
			defer r.mu.Unlock()
			r.chat.Queue = append(r.chat.Queue, agent.QueuedMessage{ID: newID(), Text: a.Text, Parts: a.Parts, Attachments: a.Attachments, Display: a.Display})
			if a.Options != nil {
				r.chat.Options = a.Options
			}
			s.publish(r, -1)
			return map[string]any{"queued": true}, nil
		}
		if a.Server == "" || a.Model == "" {
			return nil, i18n.New("Choose a server and a model")
		}
		chat, err := s.openChat(cc, a.ID)
		if err != nil {
			chat = &agent.Chat{ID: a.ID, Title: a.Title}
		}
		from := len(chat.Messages)
		if a.From != nil && *a.From >= 0 && *a.From < len(chat.Messages) {
			chat.Messages = chat.Messages[:*a.From]
			from = *a.From
		}
		chat.Server, chat.Model = a.Server, a.Model
		if a.Options != nil {
			chat.Options = a.Options
		}
		if a.Mode != "" {
			chat.Mode = a.Mode
		}
		if a.Ticket != nil && chat.Ticket == nil {
			chat.Ticket = a.Ticket
		}
		from = minFrom(from, skipQuestions(chat))
		chat.Messages = append(chat.Messages, &agent.Message{Role: "user", Content: userContent(a.Text, a.Parts), Display: displayOf(a.Text, a.Display, a.Parts), Attachments: a.Attachments})
		s.startRun(cc.loc, cc.root, project, c.language(), chat, from)
		return map[string]any{"queued": false}, nil
	}))
	s.handle("agent.unqueue", withChat(func(ctx context.Context, c *Client, cc chatCtx, p json.RawMessage) (any, error) {
		a, err := bind[struct{ ID, Queued string }](p)
		if err != nil {
			return nil, err
		}
		return nil, s.change(cc, a.ID, func(chat *agent.Chat, _ *agentRun) (int, error) {
			kept := chat.Queue[:0]
			for _, q := range chat.Queue {
				if q.ID != a.Queued {
					kept = append(kept, q)
				}
			}
			chat.Queue = kept
			return -1, nil
		})
	}))
	s.handle("agent.stop", func(ctx context.Context, c *Client, p json.RawMessage) (any, error) {
		a, err := bind[idArg](p)
		if err != nil {
			return nil, err
		}
		if r := s.run(a.ID); r != nil {
			r.cancel()
		}
		return nil, nil
	})
	// restart runs a conversation that does not run, after f changed it.
	restart := func(c *Client, cc chatCtx, id string, f func(chat *agent.Chat) (int, error)) error {
		if s.run(id) != nil {
			return nil
		}
		chat, err := s.openChat(cc, id)
		if err != nil {
			return err
		}
		from, err := f(chat)
		if err != nil {
			return err
		}
		s.startRun(cc.loc, cc.root, c.project, c.language(), chat, from)
		return nil
	}
	// agent.resume goes on from the last completed step (after an error or a stop).
	s.handle("agent.resume", withChat(func(ctx context.Context, c *Client, cc chatCtx, p json.RawMessage) (any, error) {
		a, err := bind[idArg](p)
		if err != nil {
			return nil, err
		}
		return nil, restart(c, cc, a.ID, func(chat *agent.Chat) (int, error) {
			n := len(chat.Messages)
			if n > 0 {
				if last := chat.Messages[n-1]; last.Role == "assistant" && last.Error != "" && len(last.ToolCalls) == 0 {
					chat.Messages = chat.Messages[:n-1]
				}
			}
			closeToolCalls(chat, "Interrupted: the run stopped before this tool ended.")
			return max(0, n-1), nil
		})
	}))
	// agent.retry asks again from the last message of the user.
	s.handle("agent.retry", withChat(func(ctx context.Context, c *Client, cc chatCtx, p json.RawMessage) (any, error) {
		a, err := bind[idArg](p)
		if err != nil {
			return nil, err
		}
		return nil, restart(c, cc, a.ID, func(chat *agent.Chat) (int, error) {
			last := lastUserIndex(chat)
			if last < 0 {
				return -1, i18n.New("nothing to retry")
			}
			chat.Messages = chat.Messages[:last+1]
			return last, nil
		})
	}))
	// agent.answer: the answers to the questions of a tool message; the agent goes on.
	s.handle("agent.answer", withChat(func(ctx context.Context, c *Client, cc chatCtx, p json.RawMessage) (any, error) {
		a, err := bind[struct {
			ID      string     `json:"id"`
			Index   int        `json:"index"`
			Answers [][]string `json:"answers"`
			Notes   []string   `json:"notes"`
			Path    []int      `json:"path"`
			OffPath *int       `json:"offPath"`
		}](p)
		if err != nil {
			return nil, err
		}
		return nil, restart(c, cc, a.ID, func(chat *agent.Chat) (int, error) {
			if a.Index < 0 || a.Index >= len(chat.Messages) || chat.Messages[a.Index].AskState != "pending" {
				return -1, i18n.New("these questions are not waiting for an answer")
			}
			m := chat.Messages[a.Index]
			for _, i := range append(a.Path, deref(a.OffPath)) {
				if i < 0 || i >= len(m.Questions) {
					return -1, i18n.New("these questions are not waiting for an answer")
				}
			}
			m.Answers, m.Notes, m.Path, m.OffPath, m.AskState = a.Answers, a.Notes, a.Path, a.OffPath, "answered"
			m.Content = agent.String(agent.AnswersText(m.Questions, a.Answers, a.Notes, a.Path, a.OffPath))
			m.Summary = agent.T("answers received", nil).Raw()
			return a.Index, nil
		})
	}))
	// agent.capture: the page drawn by the window that captured the screen for board_draw, or
	// the refusal of the user; the agent goes on.
	s.handle("agent.capture", withChat(func(ctx context.Context, c *Client, cc chatCtx, p json.RawMessage) (any, error) {
		a, err := bind[struct {
			ID      string          `json:"id"`
			Index   int             `json:"index"`
			Refused bool            `json:"refused"`
			Error   string          `json:"error"`
			Content string          `json:"content"`
			Summary json.RawMessage `json:"summary"`
			Page    *agent.Page     `json:"page"`
		}](p)
		if err != nil {
			return nil, err
		}
		return nil, restart(c, cc, a.ID, func(chat *agent.Chat) (int, error) {
			if a.Index < 0 || a.Index >= len(chat.Messages) || chat.Messages[a.Index].Capture != "pending" {
				return -1, i18n.New("no capture of the screen is waiting here")
			}
			m := chat.Messages[a.Index]
			switch {
			case a.Refused:
				m.Capture, m.Status = "refused", "denied"
				m.Content, m.Summary = agent.String("The user refused to share their screen."), agent.T("capture refused", nil).Raw()
			case a.Error != "" || a.Page == nil:
				m.Capture, m.Status = "refused", "error"
				m.Content, m.Summary = agent.String("Error: "+a.Error), agent.Plain(a.Error)
			default:
				m.Capture, m.Status, m.Page = "done", "ok", a.Page
				m.Content, m.Summary = agent.String(a.Content), a.Summary
			}
			return a.Index, nil
		})
	}))
	// agent.plan: the user accepts (Build mode: the page then sends the go) or dismisses a plan.
	s.handle("agent.plan", withChat(func(ctx context.Context, c *Client, cc chatCtx, p json.RawMessage) (any, error) {
		a, err := bind[struct {
			ID    string `json:"id"`
			Index int    `json:"index"`
			State string `json:"state"`
		}](p)
		if err != nil {
			return nil, err
		}
		return nil, s.change(cc, a.ID, func(chat *agent.Chat, _ *agentRun) (int, error) {
			if a.Index < 0 || a.Index >= len(chat.Messages) || chat.Messages[a.Index].Plan == "" {
				return -1, i18n.New("no plan here")
			}
			chat.Messages[a.Index].PlanState = a.State
			if a.State == "accepted" {
				chat.Mode = agent.Build
			}
			return a.Index, nil
		})
	}))
	// agent.approve: the answer of the user to a file change or a command.
	s.handle("agent.approve", func(ctx context.Context, c *Client, p json.RawMessage) (any, error) {
		a, err := bind[struct {
			ID       string `json:"id"`
			Approval string `json:"approval"`
			Allow    bool   `json:"allow"`
			// Always: apply the next file changes of this conversation without asking.
			Always bool `json:"always"`
		}](p)
		if err != nil {
			return nil, err
		}
		r := s.run(a.ID)
		if r == nil {
			return nil, i18n.New("nothing is waiting for an answer")
		}
		r.mu.Lock()
		defer r.mu.Unlock()
		if r.chat.Approval == nil || r.chat.Approval.ID != a.Approval || r.approve == nil {
			return nil, i18n.New("nothing is waiting for an answer")
		}
		if a.Always {
			o := opt(r.chat)
			o.AutoApply = true
			r.chat.Options = &o
		}
		select {
		case r.approve <- a.Allow:
		default:
		}
		return nil, nil
	})
	s.handle("agent.compact", withChat(func(ctx context.Context, c *Client, cc chatCtx, p json.RawMessage) (any, error) {
		a, err := bind[struct {
			ID           string         `json:"id"`
			Instructions string         `json:"instructions"`
			Options      *agent.Options `json:"options"`
		}](p)
		if err != nil {
			return nil, err
		}
		if s.run(a.ID) != nil {
			return nil, i18n.New("the conversation is running")
		}
		chat, err := s.openChat(cc, a.ID)
		if err != nil {
			return nil, err
		}
		if a.Options != nil {
			chat.Options = a.Options
		}
		// A short run that only compacts.
		ctx2, cancel := context.WithCancel(context.Background())
		defer cancel()
		r := &agentRun{id: a.ID, loc: cc.loc, root: cc.root, project: c.project, lang: c.language(), ctx: ctx2, cancel: cancel, chat: chat, state: "compacting"}
		s.agents.mu.Lock()
		if s.agents.runs[a.ID] != nil {
			s.agents.mu.Unlock()
			return nil, i18n.New("the conversation is running")
		}
		s.agents.runs[a.ID] = r
		s.agents.mu.Unlock()
		defer func() {
			s.agents.mu.Lock()
			delete(s.agents.runs, a.ID)
			s.agents.mu.Unlock()
			r.mu.Lock()
			r.state = "idle"
			s.publish(r, -1)
			r.mu.Unlock()
		}()
		return nil, s.compact(r, true, a.Instructions)
	}))
	// agent.set: the mode, the options or the ticket of a conversation.
	s.handle("agent.set", withChat(func(ctx context.Context, c *Client, cc chatCtx, p json.RawMessage) (any, error) {
		a, err := bind[struct {
			ID      string            `json:"id"`
			Mode    string            `json:"mode"`
			Options *agent.Options    `json:"options"`
			Ticket  *agent.TicketLink `json:"ticket"`
		}](p)
		if err != nil {
			return nil, err
		}
		return nil, s.change(cc, a.ID, func(chat *agent.Chat, _ *agentRun) (int, error) {
			if a.Mode != "" {
				chat.Mode = a.Mode
			}
			if a.Options != nil {
				chat.Options = a.Options
			}
			if a.Ticket != nil {
				chat.Ticket = a.Ticket
			}
			return -1, nil
		})
	}))
	// agent.prompt: the system prompt of a mode as the agent would send it now (settings).
	s.handle("agent.prompt", withChat(func(ctx context.Context, c *Client, cc chatCtx, p json.RawMessage) (any, error) {
		a, err := bind[struct{ Mode, ActiveFile string }](p)
		if err != nil {
			return nil, err
		}
		ref, err := s.agentRuntime(c.project)
		if err != nil {
			return nil, err
		}
		chat := &agent.Chat{Mode: a.Mode, Options: &agent.Options{ActiveFile: a.ActiveFile}}
		return s.systemPrompt(&agentRun{loc: cc.loc, root: cc.root, project: c.project}, ref, chat, true), nil
	}))
	// agent.ui.result: the result of a tool run by this window (open_file, focus).
	s.handle("agent.ui.result", func(ctx context.Context, c *Client, p json.RawMessage) (any, error) {
		a, err := bind[struct {
			ID string `json:"id"`
			uiResult
		}](p)
		if err != nil {
			return nil, err
		}
		s.uiAnswer(a.ID, a.uiResult)
		return nil, nil
	})
}

// watch sets the conversation the window shows.
func (c *Client) watch(id string) { c.watching.Store(strings.TrimSpace(id)) }

func (c *Client) chat() string {
	id, _ := c.watching.Load().(string)
	return id
}

// deref is the value of an optional index (0 when absent: always a valid question).
func deref(i *int) int {
	if i == nil {
		return 0
	}
	return *i
}
