package kanban

import (
	"fmt"
	"strings"

	kb "antennae.space/ide/plugins/kanban/kanban"
)

// The kanban_objective tool, shared by the MCP endpoint and the assistant (its schema:
// assistant.ObjectiveProps). The models write objectives; only the user reaches, abandons or
// reorders one.

// objectiveArgs are the arguments of kanban_objective.
type objectiveArgs struct {
	Action      string
	ID          int64
	Title       string
	Description string
	Due         *string
	Text        string
}

// objectiveTool runs an action.
func (s *K) objectiveTool(root string, loc kb.Location, a objectiveArgs, by, chatID string) (string, error) {
	switch a.Action {
	case "list":
		list, err := s.Kanban.Objectives(loc)
		if err != nil {
			return "", err
		}
		if len(list) == 0 {
			return "No objective.", nil
		}
		var b strings.Builder
		for _, o := range list {
			fmt.Fprintf(&b, "Objective %d [%s] %s · tickets finished %d/%d", o.ID, kb.ObjectiveStateNames[o.State], o.Title, o.TicketsDone, o.Tickets)
			if o.Due != "" {
				fmt.Fprintf(&b, " · due %s", o.Due)
			}
			b.WriteString("\n")
		}
		return strings.TrimSpace(b.String()), nil
	case "create":
		if strings.TrimSpace(a.Title) == "" {
			return "", fmt.Errorf("title is missing")
		}
		if err := TooLong(a.Description, kb.MaxDescription, "Say what reaching it means, in short sentences."); err != nil {
			return "", err
		}
		id, err := s.Kanban.CreateObjective(loc, kb.ObjectivePatch{Title: &a.Title, Description: NonEmpty(a.Description), Due: a.Due}, by)
		if err != nil {
			return "", err
		}
		s.emitObjective(root, id, 0)
		return fmt.Sprintf("Objective %d created, last in the order. Put tickets in it with kanban_update (objective=%d), or a milestone's with kanban_milestone update.", id, id), nil
	}
	if a.ID <= 0 {
		return "", fmt.Errorf("id is missing")
	}
	switch a.Action {
	case "get":
		o, err := s.Kanban.GetObjective(loc, a.ID)
		if err != nil {
			return "", err
		}
		return kb.ObjectiveMarkdown(o), nil
	case "update":
		if err := TooLong(a.Description, kb.MaxDescription, "Say what reaching it means, in short sentences."); err != nil {
			return "", err
		}
		if err := s.Kanban.UpdateObjective(loc, a.ID, kb.ObjectivePatch{Title: NonEmpty(a.Title), Description: NonEmpty(a.Description), Due: a.Due}); err != nil {
			return "", err
		}
		s.emitObjective(root, a.ID, 0)
		return fmt.Sprintf("Objective %d updated.", a.ID), nil
	case "note":
		if err := TooLong(a.Text, kb.MaxNote, "Keep only what is worth remembering, in a few lines."); err != nil {
			return "", err
		}
		if err := s.Kanban.AddObjectiveNote(loc, a.ID, a.Text, by, chatID); err != nil {
			return "", err
		}
		s.emitObjective(root, a.ID, 0)
		return "Note added.", nil
	}
	return "", fmt.Errorf("unknown action: %s", a.Action)
}
