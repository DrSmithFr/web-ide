package kanban

import (
	"strings"
	"testing"
)

func TestObjectives(t *testing.T) {
	m, loc := newManager(t)
	mvp, err := m.CreateObjective(loc, ObjectivePatch{Title: ptr("MVP"), Due: ptr("2026-11-01")}, ByUser)
	if err != nil {
		t.Fatal(err)
	}
	launch, _ := m.CreateObjective(loc, ObjectivePatch{Title: ptr("Launch")}, ByModel)
	if _, err := m.CreateObjective(loc, ObjectivePatch{Title: ptr("Bad"), Due: ptr("1 nov")}, ByUser); err == nil {
		t.Fatal("a date that is not YYYY-MM-DD")
	}
	// The order: created at the end, moved up.
	if err := m.MoveObjective(loc, launch, -1); err != nil {
		t.Fatal(err)
	}
	list, _ := m.Objectives(loc)
	if len(list) != 2 || list[0].ID != launch || list[1].ID != mvp || list[1].Due != "2026-11-01" {
		t.Fatalf("order: %+v", list)
	}

	// A ticket and its step: the step follows its root and cannot leave alone.
	root, _ := m.Create(loc, Patch{Title: ptr("Root"), Objective: &mvp}, ByUser)
	step, _ := m.Create(loc, Patch{Title: ptr("Step"), Parent: &root}, ByUser)
	if tk, _ := m.Get(loc, step); tk.Objective != mvp {
		t.Fatalf("a new step takes the objective of its root: %d", tk.Objective)
	}
	if err := m.Update(loc, step, Patch{Objective: &launch}, ByUser); err == nil {
		t.Fatal("a step left the objective of its root")
	}
	if err := m.Update(loc, root, Patch{Objective: &launch}, ByModel); err != nil {
		t.Fatal(err)
	}
	if tk, _ := m.Get(loc, step); tk.Objective != launch {
		t.Fatal("the step did not follow its root")
	}
	if tk, _ := m.Get(loc, root); tk.ObjectiveTitle != "Launch" || !strings.Contains(Markdown(tk), "## Objective\nObjective 2 · Launch") {
		t.Fatalf("objective of the ticket: %q", tk.ObjectiveTitle)
	}

	// A milestone's default objective: its open tickets follow, a later one takes it, one put
	// elsewhere by hand stays.
	ms, _ := m.CreateMilestone(loc, MilestonePatch{Title: ptr("Relay")}, ByUser)
	a, _ := m.Create(loc, Patch{Title: ptr("A"), Milestone: &ms}, ByUser)
	b, _ := m.Create(loc, Patch{Title: ptr("B"), Milestone: &ms, Objective: &launch}, ByUser)
	if err := m.UpdateMilestone(loc, ms, MilestonePatch{Objective: &mvp}, ByUser); err != nil {
		t.Fatal(err)
	}
	c, _ := m.Create(loc, Patch{Title: ptr("C"), Milestone: &ms}, ByUser)
	for id, want := range map[int64]int64{a: mvp, b: launch, c: mvp} {
		if tk, _ := m.Get(loc, id); tk.Objective != want {
			t.Fatalf("#%d objective %d, want %d", id, tk.Objective, want)
		}
	}
	o, err := m.GetObjective(loc, mvp)
	if err != nil || o.Tickets != 2 || len(o.MilestoneList) != 1 || o.MilestoneList[0].Objective != mvp {
		t.Fatalf("objective: %+v %v", o, err)
	}
	if md := ObjectiveMarkdown(o); !strings.Contains(md, "Due: 2026-11-01") || !strings.Contains(md, "(its default objective)") {
		t.Fatalf("markdown: %s", md)
	}

	// Reached by the user only, once its tickets are finished.
	if err := m.ReachObjective(loc, mvp, ByUser); err == nil {
		t.Fatal("reached with open tickets")
	}
	for _, id := range []int64{a, c} {
		if err := m.Move(loc, id, Done, ByUser, ""); err != nil {
			t.Fatal(err)
		}
	}
	if err := m.ReachObjective(loc, mvp, ByModel); err == nil {
		t.Fatal("a model reached an objective")
	}
	if err := m.SetObjectiveState(loc, mvp, ObjectiveReached, ByUser); err == nil {
		t.Fatal("reached through the state")
	}
	if err := m.ReachObjective(loc, mvp, ByUser); err != nil {
		t.Fatal(err)
	}
	if err := m.Update(loc, b, Patch{Objective: &mvp}, ByUser); err == nil {
		t.Fatal("a ticket put in a reached objective")
	}
	if err := m.SetObjectiveState(loc, mvp, ObjectiveOpen, ByUser); err != nil {
		t.Fatal(err)
	}
	if err := m.SetObjectiveState(loc, launch, ObjectiveAbandoned, ByModel); err == nil {
		t.Fatal("a model abandoned an objective")
	}

	// Deleted: its tickets and milestones stay, without objective.
	if err := m.DeleteObjective(loc, mvp); err != nil {
		t.Fatal(err)
	}
	if tk, _ := m.Get(loc, a); tk.Objective != 0 {
		t.Fatal("a ticket kept a deleted objective")
	}
	if ml, _ := m.Milestones(loc); ml[0].Objective != 0 {
		t.Fatal("a milestone kept a deleted objective")
	}
	if _, err := m.GetObjective(loc, mvp); err != ErrObjectiveNotFound {
		t.Fatalf("deleted objective: %v", err)
	}
}
