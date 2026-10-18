package kanban

import (
	"strings"
	"testing"
)

func TestMilestone(t *testing.T) {
	m, loc := newManager(t)
	ms, err := m.CreateMilestone(loc, MilestonePatch{Title: ptr("CSV export"), Spec: ptr("docs/design/export.md")}, ByModel)
	if err != nil {
		t.Fatal(err)
	}
	// Stories: written by anyone, validated by the user only.
	s1, err := m.Story(loc, ms, StoryOp{Op: "add", Text: "As a user, I export my orders"}, ByModel)
	if err != nil {
		t.Fatal(err)
	}
	s2, _ := m.Story(loc, ms, StoryOp{Op: "add", Text: "As a user, I open the file in a spreadsheet"}, ByModel)
	if _, err := m.Story(loc, ms, StoryOp{Op: "check", ID: s1, Done: true}, ByModel); err == nil {
		t.Fatal("a model validated a user story")
	}
	if _, err := m.Story(loc, ms, StoryOp{Op: "move", ID: s2, Delta: -1}, ByUser); err != nil {
		t.Fatal(err)
	}
	got, _ := m.GetMilestone(loc, ms)
	if len(got.StoryList) != 2 || got.StoryList[0].ID != s2 || got.Spec != "docs/design/export.md" {
		t.Fatalf("stories: %+v", got)
	}

	// A root and its steps share the milestone; a step cannot have another one.
	root, _ := m.Create(loc, Patch{Title: ptr("Export"), Milestone: &ms}, ByModel)
	step, _ := m.Create(loc, Patch{Title: ptr("Export step"), Parent: &root}, ByModel)
	lone, _ := m.Create(loc, Patch{Title: ptr("Lone")}, ByModel)
	if tk, _ := m.Get(loc, step); tk.Milestone != ms {
		t.Fatalf("a new step takes the milestone of its root: %d", tk.Milestone)
	}
	zero := int64(0)
	if err := m.Update(loc, step, Patch{Milestone: &zero}, ByUser); err == nil {
		t.Fatal("milestone changed on a step")
	}
	if err := m.Update(loc, root, Patch{Milestone: &zero}, ByUser); err != nil {
		t.Fatal(err)
	}
	if tk, _ := m.Get(loc, step); tk.Milestone != 0 {
		t.Fatal("the steps follow their root out of the milestone")
	}
	if err := m.Update(loc, root, Patch{Milestone: &ms}, ByUser); err != nil {
		t.Fatal(err)
	}
	if tk, _ := m.Get(loc, step); tk.Milestone != ms || tk.MilestoneTitle != "CSV export" {
		t.Fatalf("the steps follow their root into the milestone: %+v", tk.Summary)
	}

	// Feedback made into a ticket is handled when that ticket is Done.
	fid, err := m.MilestoneFeedback(loc, ms, FeedbackOp{Op: "add", Kind: "bug", Text: "The dates are not ISO\nSeen in the export of May", Complexity: "low"}, ByUser)
	if err != nil {
		t.Fatal(err)
	}
	fix, err := m.FeedbackTicket(loc, ms, fid, ByUser)
	if err != nil {
		t.Fatal(err)
	}
	if tk, _ := m.Get(loc, fix); tk.Title != "The dates are not ISO" || tk.Milestone != ms || tk.Complexity != "low" || !strings.Contains(tk.Description, "May") {
		t.Fatalf("ticket made from the feedback: %+v", tk.Summary)
	}
	if _, err := m.FeedbackTicket(loc, ms, fid, ByUser); err == nil {
		t.Fatal("a feedback made into two tickets")
	}
	got, _ = m.GetMilestone(loc, ms)
	if got.Tickets != 3 || got.FeedbackOpen != 1 || got.FeedbackList[0].Ticket != fix {
		t.Fatalf("milestone: %+v", got.MilestoneSummary)
	}

	// Validation: the user only, once everything is finished.
	if err := m.ValidateMilestone(loc, ms, ByModel); err == nil {
		t.Fatal("a model validated a milestone")
	}
	if err := m.ValidateMilestone(loc, ms, ByUser); err == nil || !strings.Contains(err.Error(), "ticket") {
		t.Fatalf("validated with open tickets: %v", err)
	}
	for _, id := range []int64{step, root} {
		if err := m.Move(loc, id, Abandoned, ByUser, ""); err != nil {
			t.Fatal(err)
		}
	}
	for _, to := range []string{Todo, InProgress, ToTest, Done} {
		if err := m.Move(loc, fix, to, ByUser, ""); err != nil {
			t.Fatal(err)
		}
	}
	got, _ = m.GetMilestone(loc, ms)
	if got.FeedbackOpen != 0 || got.TicketsDone != 3 {
		t.Fatalf("the feedback is handled with its ticket: %+v", got.MilestoneSummary)
	}
	if err := m.ValidateMilestone(loc, ms, ByUser); err == nil || !strings.Contains(err.Error(), "story") {
		t.Fatalf("validated with stories open: %v", err)
	}
	for _, s := range []int64{s1, s2} {
		if _, err := m.Story(loc, ms, StoryOp{Op: "check", ID: s, Done: true}, ByUser); err != nil {
			t.Fatal(err)
		}
	}
	if err := m.ValidateMilestone(loc, ms, ByUser); err != nil {
		t.Fatal(err)
	}
	if err := m.Update(loc, lone, Patch{Milestone: &ms}, ByUser); err == nil {
		t.Fatal("a ticket joined a validated milestone")
	}
	if err := m.SetMilestoneState(loc, ms, MilestoneOpen, ByModel); err == nil {
		t.Fatal("a model reopened a milestone")
	}
	if err := m.SetMilestoneState(loc, ms, MilestoneOpen, ByUser); err != nil {
		t.Fatal(err)
	}
	list, _ := m.Milestones(loc)
	if len(list) != 1 || list[0].State != MilestoneOpen || list[0].Closed != 0 {
		t.Fatalf("list: %+v", list)
	}

	// Deleting the milestone keeps its tickets.
	if err := m.DeleteMilestone(loc, ms); err != nil {
		t.Fatal(err)
	}
	if tk, _ := m.Get(loc, root); tk.Milestone != 0 {
		t.Fatal("tickets keep a deleted milestone")
	}
}
