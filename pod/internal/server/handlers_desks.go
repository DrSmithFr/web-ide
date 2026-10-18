package server

import (
	"context"
	"encoding/json"

	"github.com/DrSmithFr/web-ide/pod/internal/i18n"
	"github.com/DrSmithFr/web-ide/pod/internal/sessions"
)

// Desks of a project (docs/design/desks.md): each desk is a session of its own; a window shows one
// (window.desk) and its session changes reach the windows of the same desk only.
func (s *Server) registerDesks() {
	// desk.switch shows another desk in the window without reopening the project.
	s.handle("desk.switch", func(ctx context.Context, c *Client, p json.RawMessage) (any, error) {
		a, err := bind[struct{ Desk int }](p)
		if err != nil {
			return nil, err
		}
		if c.project == "" {
			return nil, i18n.New("no open project")
		}
		if !s.Sessions.HasDesk(c.project, a.Desk) {
			return nil, i18n.New("desk not found")
		}
		s.mu.Lock()
		c.desk = a.Desk
		s.mu.Unlock()
		return map[string]any{"session": s.Sessions.Get(sessions.Key(c.project, a.Desk)), "desk": a.Desk}, nil
	})
	// desks.update replaces the desk list (added, renamed, removed desks, the Recent Files).
	s.handle("desks.update", func(ctx context.Context, c *Client, p json.RawMessage) (any, error) {
		a, err := bind[struct{ Desks sessions.DeskList }](p)
		if err != nil {
			return nil, err
		}
		if c.project == "" {
			return nil, i18n.New("no open project")
		}
		d := s.Sessions.PutDesks(c.project, a.Desks)
		s.emitProject(c.project, "desks.changed", d, c.id)
		return d, nil
	})
	// session.get and session.put read and write another desk of the project (a tab sent there).
	s.handle("session.get", func(ctx context.Context, c *Client, p json.RawMessage) (any, error) {
		a, err := bind[struct{ Desk int }](p)
		if err != nil {
			return nil, err
		}
		if c.project == "" || !s.Sessions.HasDesk(c.project, a.Desk) {
			return nil, i18n.New("desk not found")
		}
		return s.Sessions.Get(sessions.Key(c.project, a.Desk)), nil
	})
	s.handle("session.put", func(ctx context.Context, c *Client, p json.RawMessage) (any, error) {
		a, err := bind[struct {
			Desk    int
			Session json.RawMessage
		}](p)
		if err != nil {
			return nil, err
		}
		if c.project == "" || !s.Sessions.HasDesk(c.project, a.Desk) {
			return nil, i18n.New("desk not found")
		}
		s.Sessions.Put(sessions.Key(c.project, a.Desk), a.Session)
		s.emitDesk(c.project, a.Desk, "session.changed", map[string]any{"session": a.Session, "desk": a.Desk}, c.id)
		return nil, nil
	})
}

// emitDesk sends an event to the windows showing a desk of a project (not to those that
// only attached the project as a worktree).
func (s *Server) emitDesk(project string, desk int, name string, data any, except string) {
	s.mu.Lock()
	var targets []*Client
	for c := range s.clients {
		if c.project == project && c.desk == desk && c.id != except {
			targets = append(targets, c)
		}
	}
	s.mu.Unlock()
	for _, c := range targets {
		c.pushIn(name, data, project)
	}
}

// emitProject sends an event to the windows of a project, whatever their desk.
func (s *Server) emitProject(project string, name string, data any, except string) {
	s.mu.Lock()
	var targets []*Client
	for c := range s.clients {
		if c.project == project && c.id != except {
			targets = append(targets, c)
		}
	}
	s.mu.Unlock()
	for _, c := range targets {
		c.pushIn(name, data, project)
	}
}
