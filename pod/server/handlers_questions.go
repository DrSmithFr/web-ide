package server

import (
	"context"
	"encoding/json"
	"net/url"
	"sort"
	"strings"
	"sync"

	"antennae.space/ide/lib/i18n"
	"antennae.space/ide/pod/plugins"
	"antennae.space/ide/pod/push"
)

// Questions tool: every question waiting for the user in the projects of a window, whatever
// asked it — a CLI agent through the MCP tool ask_user (mcp_ask.go), or a source of a plugin
// (Host.Questions: the sessions of the agents and the conversations of the assistant, the AI
// plugin). The list is built when asked; questions.changed tells the windows of the project to
// ask again.

// Question is a question waiting for the user, as the Questions tool shows it.
type Question = plugins.Question

// questionRoot is the project whose windows show the questions of a project (a worktree shows in
// the window of its parent).
func (s *Server) questionRoot(projectID string) string {
	if p, err := s.Projects.Root(projectID); err == nil {
		return p.ID
	}
	return projectID
}

// questionsOf lists the questions waiting in the projects of root (itself and its worktrees),
// the oldest first: those of the plugins enabled on root, then those of the MCP.
func (s *Server) questionsOf(root string) []Question {
	var out []Question
	for _, src := range s.plugins.questions {
		if s.questionsOn(root, src.plugin) {
			out = append(out, src.q.List(root)...)
		}
	}
	out = append(out, s.asks.of(root)...)
	sort.SliceStable(out, func(i, j int) bool { return out[i].Time < out[j].Time })
	return out
}

// questionsOn tells whether a plugin is enabled on root (open: its windows ask).
func (s *Server) questionsOn(root, plugin string) bool {
	s.mu.Lock()
	rt := s.runtimes[root]
	s.mu.Unlock()
	return rt == nil || s.pluginOn(rt, plugin)
}

// questionsChanged tells the windows of the root of a project that its questions changed;
// asked names who just asked one (a push notification).
func (s *Server) questionsChanged(projectID, asked string) {
	root := s.questionRoot(projectID)
	s.emitFamily(root, "questions.changed", map[string]string{"project": root})
	if asked != "" {
		s.notifyQuestion(root, asked)
	}
}

// notifyQuestion: an agent asks the user (an ACP session, an MCP client).
func (s *Server) notifyQuestion(root, who string) {
	name := s.projectName(root)
	go s.notify(plugins.Note{Kind: push.Asks, Path: "/project/" + url.PathEscape(root) + "?questions", Tag: "questions-" + root, Urgency: "high",
		Text: func(lang string) (string, string) { return i18n.T(lang, "The agent needs you"), name + " · " + who }})
}

// askRegistry holds the questions of the MCP tool ask_user waiting for the user.
type askRegistry struct {
	mu    sync.Mutex
	next  int
	items map[string]*mcpAsk
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
	s.asks.items = map[string]*mcpAsk{}
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
	// questions.answer answers a question of a source or of the MCP tool from any window of its
	// root (the session may run in a worktree the window did not attach). The questions of the
	// assistant go through assistant.answer.
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
		if len(parts) == 2 && parts[0] == "mcp" {
			return nil, s.answerMCPAsk(a.ID, root, mcpAnswer{answers: a.Answers, notes: a.Notes, path: a.Path, offPath: a.OffPath, skip: a.Skip})
		}
		for _, src := range s.plugins.questions {
			if src.q.Answer != nil && parts[0] == src.q.Prefix && s.questionsOn(root, src.plugin) {
				return nil, src.q.Answer(root, a.ID, plugins.Answer{Option: a.Option, Action: a.Action, Content: a.Content})
			}
		}
		return nil, i18n.New("this question is no longer open")
	})
}
