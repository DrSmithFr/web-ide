package kanban

import (
	"fmt"
	"strings"

	kb "github.com/DrSmithFr/web-ide/pod/internal/kanban"
)

// The kanban_milestone tool, shared by the MCP endpoint and the assistant (its schema:
// assistant.MilestoneProps). The models write milestones, stories and feedback; only the user
// validates a story or a milestone.

// milestoneArgs are the arguments of kanban_milestone.
type milestoneArgs struct {
	Action      string
	ID          int64
	Title       string
	Description string
	Spec        *string
	Story       int64
	Text        string
	Kind        string
	Complexity  string
	Feedback    int64
	Objective   *int64
}

// milestoneTool runs an action; created is the number of a milestone it created.
func (s *K) milestoneTool(root string, loc kb.Location, a milestoneArgs, by, chatID string) (out string, created int64, err error) {
	switch a.Action {
	case "list":
		list, err := s.Kanban.Milestones(loc)
		if err != nil {
			return "", 0, err
		}
		if len(list) == 0 {
			return "No milestone.", 0, nil
		}
		var b strings.Builder
		for _, m := range list {
			fmt.Fprintf(&b, "Milestone %d [%s] %s · tickets finished %d/%d · user stories validated %d/%d", m.ID, kb.MilestoneStateNames[m.State], m.Title, m.TicketsDone, m.Tickets, m.StoriesDone, m.Stories)
			if m.FeedbackOpen > 0 {
				fmt.Fprintf(&b, " · open feedback %d", m.FeedbackOpen)
			}
			b.WriteString("\n")
		}
		return strings.TrimSpace(b.String()), 0, nil
	case "create":
		if strings.TrimSpace(a.Title) == "" {
			return "", 0, fmt.Errorf("title is missing")
		}
		if err := TooLong(a.Description, kb.MaxDescription, "Keep the feature and its scope; the stories go apart (story_add)."); err != nil {
			return "", 0, err
		}
		id, err := s.Kanban.CreateMilestone(loc, kb.MilestonePatch{Title: &a.Title, Description: NonEmpty(a.Description), Spec: a.Spec}, by)
		if err != nil {
			return "", 0, err
		}
		s.emitMilestone(root, id, 0)
		return fmt.Sprintf("Milestone %d created. Add its user stories (story_add), then its tickets (kanban_create with milestone=%d).", id, id), id, nil
	}
	if a.ID <= 0 {
		return "", 0, fmt.Errorf("id is missing")
	}
	if a.Action == "get" {
		ms, err := s.Kanban.GetMilestone(loc, a.ID)
		if err != nil {
			return "", 0, err
		}
		return kb.MilestoneMarkdown(ms), 0, nil
	}
	defer s.emitMilestone(root, a.ID, 0)
	switch a.Action {
	case "update":
		if err := TooLong(a.Description, kb.MaxDescription, "Keep the feature and its scope; the stories go apart (story_add)."); err != nil {
			return "", 0, err
		}
		if err := s.Kanban.UpdateMilestone(loc, a.ID, kb.MilestonePatch{Title: NonEmpty(a.Title), Description: NonEmpty(a.Description), Spec: a.Spec, Objective: a.Objective}, by); err != nil {
			return "", 0, err
		}
		return fmt.Sprintf("Milestone %d updated.", a.ID), 0, nil
	case "story_add":
		sid, err := s.Kanban.Story(loc, a.ID, kb.StoryOp{Op: "add", Text: a.Text}, by)
		if err != nil {
			return "", 0, err
		}
		return fmt.Sprintf("User story added (id %d).", sid), 0, nil
	case "story_edit":
		if _, err := s.Kanban.Story(loc, a.ID, kb.StoryOp{Op: "edit", ID: a.Story, Text: a.Text}, by); err != nil {
			return "", 0, err
		}
		return "User story edited.", 0, nil
	case "story_delete":
		if _, err := s.Kanban.Story(loc, a.ID, kb.StoryOp{Op: "delete", ID: a.Story}, by); err != nil {
			return "", 0, err
		}
		return "User story deleted.", 0, nil
	case "feedback_add":
		if a.Complexity == "" {
			return "", 0, fmt.Errorf("complexity is required: low, medium or high")
		}
		fid, err := s.Kanban.MilestoneFeedback(loc, a.ID, kb.FeedbackOp{Op: "add", Kind: a.Kind, Text: a.Text, Complexity: a.Complexity, ChatID: chatID}, by)
		if err != nil {
			return "", 0, err
		}
		return fmt.Sprintf("Feedback added (id %d): the milestone waits for it to be handled.", fid), 0, nil
	case "feedback_done", "feedback_reopen":
		if _, err := s.Kanban.MilestoneFeedback(loc, a.ID, kb.FeedbackOp{Op: "check", ID: a.Feedback, Done: a.Action == "feedback_done"}, by); err != nil {
			return "", 0, err
		}
		return "Feedback updated.", 0, nil
	case "note":
		if err := TooLong(a.Text, kb.MaxNote, "Keep only what is worth remembering, in a few lines."); err != nil {
			return "", 0, err
		}
		if err := s.Kanban.AddMilestoneNote(loc, a.ID, a.Text, by, chatID); err != nil {
			return "", 0, err
		}
		return "Note added.", 0, nil
	}
	return "", 0, fmt.Errorf("unknown action: %s", a.Action)
}
