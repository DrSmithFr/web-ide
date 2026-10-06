package server

import (
	"context"
	"encoding/json"
	"time"

	"github.com/DrSmithFr/web-ide/pod/internal/agent"
)

// Tools that act on the interface (open_file, focus) run in a window of the project: the
// one showing the conversation, else another window of the project. Without any, the model
// is told the user will not see it.

type uiResult struct {
	Content string          `json:"content"`
	Summary json.RawMessage `json:"summary"`
	Status  string          `json:"status"`
}

// uiClient picks the window that runs a tool for a run.
func (s *Server) uiClient(r *agentRun) *Client {
	s.mu.Lock()
	defer s.mu.Unlock()
	var best *Client
	score := -1
	for c := range s.clients {
		sc := -1
		switch {
		case c.project == r.project && c.chat() == r.id:
			sc = 3
		case c.project == r.project:
			sc = 2
		case c.chat() == r.id:
			sc = 1
		}
		if sc > score {
			best, score = c, sc
		}
	}
	if score < 1 {
		return nil
	}
	return best
}

func (s *Server) uiTool(r *agentRun, name string, a toolArgs) (toolResult, error) {
	quiet := a.boolean("quiet", false)
	c := s.uiClient(r)
	if c == nil {
		return toolResult{Content: "No IDE window is open: the user will not see it.", Summary: agent.T("no window open", nil).Raw(), Status: "ok"}, nil
	}
	id := newID()
	ch := make(chan uiResult, 1)
	s.agents.mu.Lock()
	s.agents.ui[id] = ch
	s.agents.mu.Unlock()
	defer func() {
		s.agents.mu.Lock()
		delete(s.agents.ui, id)
		s.agents.mu.Unlock()
	}()
	c.push("agent.ui", map[string]any{"id": id, "chat": r.id, "tool": name, "args": a})
	if quiet {
		return toolResult{}, nil
	}
	ctx, cancel := context.WithTimeout(r.ctx, 10*time.Second)
	defer cancel()
	select {
	case res := <-ch:
		return toolResult{Content: res.Content, Summary: res.Summary, Status: res.Status}, nil
	case <-ctx.Done():
		return toolResult{Content: "No IDE window answered: the user may not see it.", Summary: agent.T("no window open", nil).Raw(), Status: "ok"}, nil
	}
}

// uiAnswer gives the result of a tool run by a window.
func (s *Server) uiAnswer(id string, res uiResult) {
	s.agents.mu.Lock()
	ch := s.agents.ui[id]
	s.agents.mu.Unlock()
	if ch != nil {
		select {
		case ch <- res:
		default:
		}
	}
}
