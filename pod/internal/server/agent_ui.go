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
	// A page drawn by board_draw.
	Page *agent.Page `json:"page,omitempty"`
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
	res, window, answered := s.uiCall(r, name, a, !quiet, 10*time.Second)
	switch {
	case !window:
		return toolResult{Content: "No IDE window is open: the user will not see it.", Summary: agent.T("no window open", nil).Raw(), Status: "ok"}, nil
	case quiet:
		return toolResult{}, nil
	case !answered:
		return toolResult{Content: "No IDE window answered: the user may not see it.", Summary: agent.T("no window open", nil).Raw(), Status: "ok"}, nil
	}
	return toolResult{Content: res.Content, Summary: res.Summary, Status: res.Status}, nil
}

// uiCall asks a window of the project to run a tool and, with wait, waits for its result:
// window tells whether a window was there, answered whether it answered in time.
func (s *Server) uiCall(r *agentRun, name string, args any, wait bool, timeout time.Duration) (res uiResult, window, answered bool) {
	c := s.uiClient(r)
	if c == nil {
		return res, false, false
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
	c.push("agent.ui", map[string]any{"id": id, "chat": r.id, "tool": name, "args": args})
	if !wait {
		return res, true, false
	}
	ctx, cancel := context.WithTimeout(r.ctx, timeout)
	defer cancel()
	select {
	case res = <-ch:
		return res, true, true
	case <-ctx.Done():
		return res, true, false
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
