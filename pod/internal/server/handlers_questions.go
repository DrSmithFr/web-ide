package server

import (
	"context"
	"encoding/json"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/DrSmithFr/web-ide/pod/internal/acp"
	"github.com/DrSmithFr/web-ide/pod/internal/assistant"
	"github.com/DrSmithFr/web-ide/pod/internal/i18n"
	"github.com/DrSmithFr/web-ide/pod/internal/push"
)

// Questions tool: every question waiting for the user in the projects of a window, whatever
// asked it — an ACP session (a permission or a form, package acp), a conversation of the
// assistant (ask_user) or a CLI agent through the MCP tool ask_user (mcp_ask.go). The list
// is built when asked; questions.changed tells the windows of the project to ask again.

// Question is a question waiting for the user, as the Questions tool shows it.
type Question struct {
	// ID: acp:<project>:<session>:<index>, chat:<chat>:<index> or mcp:<n>.
	ID     string `json:"id"`
	Source string `json:"source"` // acp | assistant | mcp
	// Project the question lives in (a worktree has its own), Session the ACP session or the
	// conversation, Index its entry or message.
	Project string `json:"project"`
	Session string `json:"session,omitempty"`
	Index   int    `json:"index"`
	// Name of the asker (an agent, the assistant, an MCP client) and Title of its session.
	Name  string `json:"name"`
	Title string `json:"title"`
	// Kind: permission (Options), form (Text and Schema), ask (Questions of ask_user).
	Kind      string               `json:"kind"`
	Text      string               `json:"text,omitempty"`
	Options   []acp.Option         `json:"options,omitempty"`
	Schema    json.RawMessage      `json:"schema,omitempty"`
	Questions []assistant.Question `json:"questions,omitempty"`
	// Last is what the asker said before the question (its context).
	Last string `json:"last,omitempty"`
	Time int64  `json:"time"`
	// AutoOpen: an override of the setting for this session (nil: the setting).
	AutoOpen *bool `json:"autoOpen,omitempty"`
}

// questionRoot is the project whose windows show the questions of a project (a worktree shows in
// the window of its parent).
func (s *Server) questionRoot(projectID string) string {
	if p, err := s.Projects.Root(projectID); err == nil {
		return p.ID
	}
	return projectID
}

// questionsOf lists the questions waiting in the projects of root (itself and its worktrees),
// the oldest first.
func (s *Server) questionsOf(root string) []Question {
	var out []Question
	s.mu.Lock()
	type rtOf struct {
		id string
		m  *acp.Manager
	}
	var rts []rtOf
	for id, rt := range s.runtimes {
		if rt != nil && rt.Agents != nil {
			rts = append(rts, rtOf{id, rt.Agents})
		}
	}
	s.mu.Unlock()
	for _, r := range rts {
		if s.questionRoot(r.id) != root {
			continue
		}
		for _, a := range r.m.Questions() {
			q := Question{ID: "acp:" + r.id + ":" + a.Session + ":" + strconv.Itoa(a.Index), Source: "acp", Project: r.id, Session: a.Session, Index: a.Index,
				Name: a.Name, Title: a.Title, Last: a.Last, Time: a.Entry.Time, AutoOpen: a.AutoOpen}
			if a.Entry.Kind == "permission" {
				q.Kind, q.Text, q.Options = "permission", a.Entry.Title, a.Entry.Options
			} else {
				q.Kind, q.Text, q.Schema = "form", a.Entry.Text, a.Entry.Schema
			}
			out = append(out, q)
		}
	}
	out = append(out, s.assistantQuestions(root)...)
	out = append(out, s.asks.of(root)...)
	sort.SliceStable(out, func(i, j int) bool { return out[i].Time < out[j].Time })
	return out
}

// assistantQuestions are the ask_user questions of the conversations of root waiting for the
// user.
func (s *Server) assistantQuestions(root string) []Question {
	loc, _, err := s.chatLoc(root)
	if err != nil {
		return nil
	}
	list, err := s.LLM.ListChats(loc)
	if err != nil {
		return nil
	}
	var out []Question
	for _, info := range list {
		if !info.Waiting {
			continue
		}
		c, err := s.loadChat(loc, info.ID)
		if err != nil {
			continue
		}
		last := ""
		for i, m := range c.Messages {
			if m.Role == "assistant" {
				if t := textOf(m.Content); strings.TrimSpace(t) != "" {
					last = t
				}
			}
			if m.AskState == "pending" {
				out = append(out, Question{ID: "chat:" + c.ID + ":" + strconv.Itoa(i), Source: "assistant", Project: root, Session: c.ID, Index: i,
					Name: "Assistant", Title: c.Title, Kind: "ask", Questions: m.Questions, Last: last, Time: m.At})
			}
		}
	}
	return out
}

// textOf is the text of the content of a message (a string; parts are skipped).
func textOf(raw json.RawMessage) string {
	var s string
	_ = json.Unmarshal(raw, &s)
	return s
}

// questionsChanged tells the windows of the root of a project that its questions changed;
// asked names who just asked one (a push notification).
func (s *Server) questionsChanged(projectID, asked string) {
	root := s.questionRoot(projectID)
	s.emitAssistant(root, "questions.changed", map[string]string{"project": root})
	if asked != "" {
		s.notifyQuestion(root, asked)
	}
}

// notifyQuestion: an agent asks the user (an ACP session, an MCP client).
func (s *Server) notifyQuestion(root, who string) {
	name := s.projectName(root)
	go s.notify(pushNote{kind: push.Asks, path: "/project/" + url.PathEscape(root) + "?questions", tag: "questions-" + root, urgency: "high",
		text: func(lang string) (string, string) { return i18n.T(lang, "The agent needs you"), name + " · " + who }})
}

// noteAsks: a conversation of the assistant was saved; its questions changed when it asks
// or stops asking, or when a message with questions is among those published.
func (s *Server) noteAsks(root string, c *assistant.Chat, from int) {
	pending, touched := false, false
	for i, m := range c.Messages {
		if m.AskState == "pending" {
			pending = true
		}
		if i >= from && from >= 0 && m.AskState != "" {
			touched = true
		}
	}
	s.asks.mu.Lock()
	prev := s.asks.chats[c.ID]
	if pending {
		s.asks.chats[c.ID] = true
	} else {
		delete(s.asks.chats, c.ID)
	}
	s.asks.mu.Unlock()
	if prev != pending || touched {
		s.emitAssistant(root, "questions.changed", map[string]string{"project": root})
	}
}

// askRegistry holds the questions of the MCP tool ask_user waiting for the user, and which
// conversations of the assistant ask (noteAsks).
type askRegistry struct {
	mu    sync.Mutex
	next  int
	items map[string]*mcpAsk
	chats map[string]bool
}

func (r *askRegistry) of(root string) []Question {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []Question
	for _, a := range r.items {
		if a.root == root && !a.answered {
			out = append(out, a.q)
		}
	}
	return out
}

func (s *Server) registerQuestions() {
	s.asks.items, s.asks.chats = map[string]*mcpAsk{}, map[string]bool{}
	s.handle("questions.list", func(ctx context.Context, c *Client, p json.RawMessage) (any, error) {
		if c.project == "" {
			return []Question{}, nil
		}
		list := s.questionsOf(s.questionRoot(c.project))
		if list == nil {
			list = []Question{}
		}
		return list, nil
	})
	// questions.answer answers a question of an ACP session or of the MCP tool from any window
	// of its root (the session may run in a worktree the window did not attach). The questions
	// of the assistant go through assistant.answer.
	s.handle("questions.answer", func(ctx context.Context, c *Client, p json.RawMessage) (any, error) {
		a, err := bind[struct {
			ID string `json:"id"`
			// A permission: the option chosen.
			Option string `json:"option"`
			// A form: accept (with Content), decline or cancel.
			Action  string          `json:"action"`
			Content json.RawMessage `json:"content"`
			// ask_user of MCP: as assistant.answer; Skip: the user does not answer.
			Answers [][]string `json:"answers"`
			Notes   []string   `json:"notes"`
			Path    []int      `json:"path"`
			OffPath *int       `json:"offPath"`
			Skip    bool       `json:"skip"`
		}](p)
		if err != nil {
			return nil, err
		}
		root := s.questionRoot(c.project)
		parts := strings.Split(a.ID, ":")
		switch {
		case len(parts) == 4 && parts[0] == "acp":
			if s.questionRoot(parts[1]) != root {
				return nil, i18n.New("this question is no longer open")
			}
			s.mu.Lock()
			rt := s.runtimes[parts[1]]
			s.mu.Unlock()
			if rt == nil || rt.Agents == nil {
				return nil, i18n.New("this question is no longer open")
			}
			sess, err := rt.Agents.Get(parts[2])
			if err != nil {
				return nil, err
			}
			index, _ := strconv.Atoi(parts[3])
			if a.Option != "" {
				return nil, sess.Answer(index, a.Option)
			}
			return nil, sess.AnswerForm(index, a.Action, a.Content)
		case len(parts) == 2 && parts[0] == "mcp":
			return nil, s.answerMCPAsk(a.ID, root, mcpAnswer{answers: a.Answers, notes: a.Notes, path: a.Path, offPath: a.OffPath, skip: a.Skip})
		}
		return nil, i18n.New("this question is no longer open")
	})
	// agents.autoOpen sets, for one session, whether its questions open by themselves (null:
	// the setting of the page).
	s.handle("agents.autoOpen", func(ctx context.Context, c *Client, p json.RawMessage) (any, error) {
		a, err := bind[struct {
			ID    string `json:"id"`
			Value *bool  `json:"value"`
		}](p)
		if err != nil {
			return nil, err
		}
		rt, err := c.runtime()
		if err != nil {
			return nil, err
		}
		if rt.Agents == nil {
			return nil, i18n.New("agents run on local projects only")
		}
		sess, err := rt.Agents.Get(a.ID)
		if err != nil {
			return nil, err
		}
		sess.SetAutoOpen(a.Value)
		return nil, nil
	})
}
