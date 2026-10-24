package kanban

import (
	"context"
	"encoding/json"

	kb "antennae.space/ide/plugins/kanban/kanban"
)

// emitMilestone tells the windows of a project that a milestone changed (kanban.changed with
// its number; id names a ticket that changed with it).
func (s *K) emitMilestone(root string, ms, id int64) {
	s.pod.EmitFamily(root, "kanban.changed", map[string]any{"project": root, "id": id, "milestone": ms}, "")
}

// registerMilestones: the milestones of the kanban (docs/design/kanban.md). Only the user
// validates, abandons or reopens one, and checks its user stories.
func (s *K) registerMilestones() {
	type msArg struct {
		ID int64  `json:"id"`
		By string `json:"by"`
	}
	by := func(b string) string {
		if b == kb.ByModel {
			return b
		}
		return kb.ByUser
	}
	// change binds the arguments, runs f on the kanban of the project, announces the
	// milestone (and the ticket f answers) and returns the milestone.
	change := func(f func(loc kb.Location, a msArg, p json.RawMessage) (int64, error)) handler {
		return func(ctx context.Context, c *client, p json.RawMessage) (any, error) {
			root, err := s.kanbanProject(c.project)
			if err != nil {
				return nil, err
			}
			a, err := bind[msArg](p)
			if err != nil {
				return nil, err
			}
			a.By = by(a.By)
			loc := kanbanLoc(root)
			tid, err := f(loc, a, p)
			if err != nil {
				return nil, err
			}
			s.emitMilestone(root.ID, a.ID, tid)
			return s.Kanban.GetMilestone(loc, a.ID)
		}
	}
	s.handle("kanban.milestone.list", func(ctx context.Context, c *client, p json.RawMessage) (any, error) {
		root, err := s.kanbanProject(c.project)
		if err != nil {
			return nil, err
		}
		return s.Kanban.Milestones(kanbanLoc(root))
	})
	s.handle("kanban.milestone.get", func(ctx context.Context, c *client, p json.RawMessage) (any, error) {
		root, err := s.kanbanProject(c.project)
		if err != nil {
			return nil, err
		}
		a, err := bind[msArg](p)
		if err != nil {
			return nil, err
		}
		return s.Kanban.GetMilestone(kanbanLoc(root), a.ID)
	})
	s.handle("kanban.milestone.create", func(ctx context.Context, c *client, p json.RawMessage) (any, error) {
		root, err := s.kanbanProject(c.project)
		if err != nil {
			return nil, err
		}
		a, err := bind[struct {
			kb.MilestonePatch
			By string `json:"by"`
		}](p)
		if err != nil {
			return nil, err
		}
		loc := kanbanLoc(root)
		id, err := s.Kanban.CreateMilestone(loc, a.MilestonePatch, by(a.By))
		if err != nil {
			return nil, err
		}
		s.emitMilestone(root.ID, id, 0)
		return s.Kanban.GetMilestone(loc, id)
	})
	s.handle("kanban.milestone.update", change(func(loc kb.Location, a msArg, p json.RawMessage) (int64, error) {
		b, err := bind[struct {
			Patch kb.MilestonePatch `json:"patch"`
		}](p)
		if err != nil {
			return 0, err
		}
		return 0, s.Kanban.UpdateMilestone(loc, a.ID, b.Patch, a.By)
	}))
	s.handle("kanban.milestone.delete", func(ctx context.Context, c *client, p json.RawMessage) (any, error) {
		root, err := s.kanbanProject(c.project)
		if err != nil {
			return nil, err
		}
		a, err := bind[msArg](p)
		if err != nil {
			return nil, err
		}
		if err := s.Kanban.DeleteMilestone(kanbanLoc(root), a.ID); err != nil {
			return nil, err
		}
		s.emitMilestone(root.ID, a.ID, 0)
		return nil, nil
	})
	s.handle("kanban.milestone.story", change(func(loc kb.Location, a msArg, p json.RawMessage) (int64, error) {
		b, err := bind[struct {
			Story kb.StoryOp `json:"story"`
		}](p)
		if err != nil {
			return 0, err
		}
		_, err = s.Kanban.Story(loc, a.ID, b.Story, a.By)
		return 0, err
	}))
	s.handle("kanban.milestone.feedback", change(func(loc kb.Location, a msArg, p json.RawMessage) (int64, error) {
		b, err := bind[struct {
			Feedback kb.FeedbackOp `json:"feedback"`
		}](p)
		if err != nil {
			return 0, err
		}
		_, err = s.Kanban.MilestoneFeedback(loc, a.ID, b.Feedback, a.By)
		return 0, err
	}))
	// kanban.milestone.feedback.ticket makes a ticket of the milestone from a feedback.
	s.handle("kanban.milestone.feedback.ticket", change(func(loc kb.Location, a msArg, p json.RawMessage) (int64, error) {
		b, err := bind[struct {
			Feedback int64 `json:"feedback"`
		}](p)
		if err != nil {
			return 0, err
		}
		return s.Kanban.FeedbackTicket(loc, a.ID, b.Feedback, a.By)
	}))
	s.handle("kanban.milestone.validate", change(func(loc kb.Location, a msArg, p json.RawMessage) (int64, error) {
		return 0, s.Kanban.ValidateMilestone(loc, a.ID, a.By)
	}))
	s.handle("kanban.milestone.state", change(func(loc kb.Location, a msArg, p json.RawMessage) (int64, error) {
		b, err := bind[struct {
			State string `json:"state"`
		}](p)
		if err != nil {
			return 0, err
		}
		return 0, s.Kanban.SetMilestoneState(loc, a.ID, b.State, a.By)
	}))
	s.handle("kanban.milestone.chat.link", change(func(loc kb.Location, a msArg, p json.RawMessage) (int64, error) {
		b, err := bind[struct {
			ChatID string `json:"chatId"`
			Role   string `json:"role"`
			Title  string `json:"title"`
		}](p)
		if err != nil {
			return 0, err
		}
		return 0, s.Kanban.LinkMilestoneChat(loc, a.ID, b.ChatID, b.Role, b.Title)
	}))
	s.handle("kanban.milestone.chat.unlink", change(func(loc kb.Location, a msArg, p json.RawMessage) (int64, error) {
		b, err := bind[struct {
			ChatID string `json:"chatId"`
		}](p)
		if err != nil {
			return 0, err
		}
		return 0, s.Kanban.UnlinkMilestoneChat(loc, a.ID, b.ChatID)
	}))
	s.handle("kanban.milestone.note", change(func(loc kb.Location, a msArg, p json.RawMessage) (int64, error) {
		b, err := bind[struct {
			Text   string `json:"text"`
			ChatID string `json:"chatId"`
		}](p)
		if err != nil {
			return 0, err
		}
		return 0, s.Kanban.AddMilestoneNote(loc, a.ID, b.Text, a.By, b.ChatID)
	}))
	s.handle("kanban.milestone.note.delete", change(func(loc kb.Location, a msArg, p json.RawMessage) (int64, error) {
		b, err := bind[struct {
			NoteID int64 `json:"noteId"`
		}](p)
		if err != nil {
			return 0, err
		}
		return 0, s.Kanban.DeleteMilestoneNote(loc, a.ID, b.NoteID)
	}))
}
