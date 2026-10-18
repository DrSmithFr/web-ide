package server

import (
	"context"
	"encoding/json"
	"time"

	"github.com/DrSmithFr/web-ide/pod/internal/assistant"
)

// Tools that act on the interface (open_file, focus) run in a window of the project: the
// one showing the conversation, else another window of the project. Without any, the model
// is told the user will not see it.

type uiResult struct {
	Content string          `json:"content"`
	Summary json.RawMessage `json:"summary"`
	Status  string          `json:"status"`
	// A page drawn by board_draw.
	Page *assistant.Page `json:"page,omitempty"`
	// Image drawn by the user for request_doodle of the MCP (a data URL of a PNG).
	Image string `json:"image,omitempty"`
}

// uiClient picks the window that runs a tool for a run.
func (s *Server) uiClient(r *assistantRun) *Client { return s.uiClientFor(r.project, r.id) }

// uiClientFor picks a window of project, the one showing the conversation chat first ("" for
// a call of the MCP: a window showing the project first).
func (s *Server) uiClientFor(project, chat string) *Client {
	s.mu.Lock()
	defer s.mu.Unlock()
	var best *Client
	score := -1
	for c := range s.clients {
		sc := -1
		switch {
		case c.sees(project) && chat != "" && c.chat() == chat:
			sc = 3
		case c.sees(project) && chat == "" && c.project == project:
			sc = 3
		case c.sees(project):
			sc = 2
		case chat != "" && c.chat() == chat:
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

func (s *Server) uiTool(r *assistantRun, name string, a toolArgs) (toolResult, error) {
	quiet := a.boolean("quiet", false)
	res, window, answered := s.uiCall(r, name, a, !quiet, 10*time.Second)
	switch {
	case !window:
		return toolResult{Content: "No IDE window is open: the user will not see it.", Summary: assistant.T("no window open", nil).Raw(), Status: "ok"}, nil
	case quiet:
		return toolResult{}, nil
	case !answered:
		return toolResult{Content: "No IDE window answered: the user may not see it.", Summary: assistant.T("no window open", nil).Raw(), Status: "ok"}, nil
	}
	return toolResult{Content: res.Content, Summary: res.Summary, Status: res.Status}, nil
}

// uiCall asks a window of the project to run a tool and, with wait, waits for its result:
// window tells whether a window was there, answered whether it answered in time.
func (s *Server) uiCall(r *assistantRun, name string, args any, wait bool, timeout time.Duration) (res uiResult, window, answered bool) {
	return s.uiCallIn(r.ctx, s.uiClient(r), r.project, r.id, name, args, wait, timeout)
}

// uiCallIn asks the window c (nil: none) to run a tool for the conversation chat ("" for the
// MCP) working in project.
func (s *Server) uiCallIn(ctx context.Context, c *Client, project, chat, name string, args any, wait bool, timeout time.Duration) (res uiResult, window, answered bool) {
	if c == nil {
		return res, false, false
	}
	id := newID()
	ch := make(chan uiResult, 1)
	s.assistants.mu.Lock()
	s.assistants.ui[id] = ch
	s.assistants.mu.Unlock()
	defer func() {
		s.assistants.mu.Lock()
		delete(s.assistants.ui, id)
		s.assistants.mu.Unlock()
	}()
	c.push("assistant.ui", map[string]any{"id": id, "chat": chat, "tool": name, "args": args, "project": project})
	if !wait {
		return res, true, false
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
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
	s.assistants.mu.Lock()
	ch := s.assistants.ui[id]
	s.assistants.mu.Unlock()
	if ch != nil {
		select {
		case ch <- res:
		default:
		}
	}
}
