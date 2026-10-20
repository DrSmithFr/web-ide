package server

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/DrSmithFr/web-ide/pod/plugins"

	"github.com/DrSmithFr/web-ide/pod/assistant"
)

// Tools that act on the interface (open_file, focus) run in a window of the project: the
// one showing the conversation, else another window of the project (the MCP's and the
// assistant's, lent to the AI plugin as Pod.UICall). The window answers with ui.result.

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

// uiCallIn asks the window c (nil: none) to run a tool for the conversation chat ("" for the
// MCP) working in project.
func (s *Server) uiCallIn(ctx context.Context, c *Client, project, chat, name string, args any, wait bool, timeout time.Duration) (res plugins.UIResult, window, answered bool) {
	if c == nil {
		return res, false, false
	}
	id := assistant.NewID()
	ch := make(chan plugins.UIResult, 1)
	s.uiMu.Lock()
	s.uiWait[id] = ch
	s.uiMu.Unlock()
	defer func() {
		s.uiMu.Lock()
		delete(s.uiWait, id)
		s.uiMu.Unlock()
	}()
	c.push("ui.call", map[string]any{"id": id, "chat": chat, "tool": name, "args": args, "project": project})
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
func (s *Server) uiAnswer(id string, res plugins.UIResult) {
	s.uiMu.Lock()
	ch := s.uiWait[id]
	s.uiMu.Unlock()
	if ch != nil {
		select {
		case ch <- res:
		default:
		}
	}
}

// registerUI adds ui.result: the result of a tool run by a window (open_file, focus).
func (s *Server) registerUI() {
	s.handle("ui.result", func(ctx context.Context, c *Client, p json.RawMessage) (any, error) {
		a, err := bind[struct {
			ID string `json:"id"`
			plugins.UIResult
		}](p)
		if err != nil {
			return nil, err
		}
		s.uiAnswer(a.ID, a.UIResult)
		return nil, nil
	})
}

// watch sets the conversation the window shows.
func (c *Client) watch(id string) { c.watching.Store(strings.TrimSpace(id)) }

func (c *Client) chat() string {
	id, _ := c.watching.Load().(string)
	return id
}
