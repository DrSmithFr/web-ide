package server

import (
	"context"
	"encoding/json"

	"github.com/DrSmithFr/web-ide/pod/internal/kanban"
)

// emitObjective tells the windows of a project that an objective changed (kanban.changed with
// its number; id names a ticket that changed with it).
func (s *Server) emitObjective(root string, o, id int64) {
	s.mu.Lock()
	var targets []*Client
	for c := range s.clients {
		if c.project == "" {
			continue
		}
		if c.project == root {
			targets = append(targets, c)
		} else if p, ok := s.Projects.Get(c.project); ok && p.Parent == root {
			targets = append(targets, c)
		}
	}
	s.mu.Unlock()
	for _, c := range targets {
		c.push("kanban.changed", map[string]any{"project": root, "id": id, "objective": o})
	}
}

// registerObjectives: the objectives of the kanban (docs/design/kanban.md). Only the user
// reaches, abandons, reopens or reorders one.
func (s *Server) registerObjectives() {
	type oArg struct {
		ID int64  `json:"id"`
		By string `json:"by"`
	}
	by := func(b string) string {
		if b == kanban.ByModel {
			return b
		}
		return kanban.ByUser
	}
	// change binds the arguments, runs f on the kanban of the project, announces the
	// objective and returns it.
	change := func(f func(loc kanban.Location, a oArg, p json.RawMessage) error) handler {
		return func(ctx context.Context, c *Client, p json.RawMessage) (any, error) {
			root, err := s.kanbanProject(c.project)
			if err != nil {
				return nil, err
			}
			a, err := bind[oArg](p)
			if err != nil {
				return nil, err
			}
			a.By = by(a.By)
			loc := kanbanLoc(root)
			if err := f(loc, a, p); err != nil {
				return nil, err
			}
			s.emitObjective(root.ID, a.ID, 0)
			return s.Kanban.GetObjective(loc, a.ID)
		}
	}
	s.handle("kanban.objective.list", func(ctx context.Context, c *Client, p json.RawMessage) (any, error) {
		root, err := s.kanbanProject(c.project)
		if err != nil {
			return nil, err
		}
		return s.Kanban.Objectives(kanbanLoc(root))
	})
	s.handle("kanban.objective.get", func(ctx context.Context, c *Client, p json.RawMessage) (any, error) {
		root, err := s.kanbanProject(c.project)
		if err != nil {
			return nil, err
		}
		a, err := bind[oArg](p)
		if err != nil {
			return nil, err
		}
		return s.Kanban.GetObjective(kanbanLoc(root), a.ID)
	})
	s.handle("kanban.objective.create", func(ctx context.Context, c *Client, p json.RawMessage) (any, error) {
		root, err := s.kanbanProject(c.project)
		if err != nil {
			return nil, err
		}
		a, err := bind[struct {
			kanban.ObjectivePatch
			By string `json:"by"`
		}](p)
		if err != nil {
			return nil, err
		}
		loc := kanbanLoc(root)
		id, err := s.Kanban.CreateObjective(loc, a.ObjectivePatch, by(a.By))
		if err != nil {
			return nil, err
		}
		s.emitObjective(root.ID, id, 0)
		return s.Kanban.GetObjective(loc, id)
	})
	s.handle("kanban.objective.update", change(func(loc kanban.Location, a oArg, p json.RawMessage) error {
		b, err := bind[struct {
			Patch kanban.ObjectivePatch `json:"patch"`
		}](p)
		if err != nil {
			return err
		}
		return s.Kanban.UpdateObjective(loc, a.ID, b.Patch)
	}))
	s.handle("kanban.objective.delete", func(ctx context.Context, c *Client, p json.RawMessage) (any, error) {
		root, err := s.kanbanProject(c.project)
		if err != nil {
			return nil, err
		}
		a, err := bind[oArg](p)
		if err != nil {
			return nil, err
		}
		if err := s.Kanban.DeleteObjective(kanbanLoc(root), a.ID); err != nil {
			return nil, err
		}
		s.emitObjective(root.ID, a.ID, 0)
		return nil, nil
	})
	s.handle("kanban.objective.move", change(func(loc kanban.Location, a oArg, p json.RawMessage) error {
		b, err := bind[struct{ Delta int }](p)
		if err != nil {
			return err
		}
		return s.Kanban.MoveObjective(loc, a.ID, b.Delta)
	}))
	s.handle("kanban.objective.reach", change(func(loc kanban.Location, a oArg, p json.RawMessage) error {
		return s.Kanban.ReachObjective(loc, a.ID, a.By)
	}))
	s.handle("kanban.objective.state", change(func(loc kanban.Location, a oArg, p json.RawMessage) error {
		b, err := bind[struct {
			State string `json:"state"`
		}](p)
		if err != nil {
			return err
		}
		return s.Kanban.SetObjectiveState(loc, a.ID, b.State, a.By)
	}))
	s.handle("kanban.objective.note", change(func(loc kanban.Location, a oArg, p json.RawMessage) error {
		b, err := bind[struct {
			Text   string `json:"text"`
			ChatID string `json:"chatId"`
		}](p)
		if err != nil {
			return err
		}
		return s.Kanban.AddObjectiveNote(loc, a.ID, b.Text, a.By, b.ChatID)
	}))
	s.handle("kanban.objective.note.delete", change(func(loc kanban.Location, a oArg, p json.RawMessage) error {
		b, err := bind[struct {
			NoteID int64 `json:"noteId"`
		}](p)
		if err != nil {
			return err
		}
		return s.Kanban.DeleteObjectiveNote(loc, a.ID, b.NoteID)
	}))
}
