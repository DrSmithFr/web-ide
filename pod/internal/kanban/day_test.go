package kanban

import "testing"

func TestHistory(t *testing.T) {
	m, loc := newManager(t)
	clock := int64(1_000_000)
	m.Now = func() int64 { return clock }
	a, _ := m.Create(loc, Patch{Title: ptr("Old")}, ByUser)
	clock = 2_000_000 // "yesterday"
	b, _ := m.Create(loc, Patch{Title: ptr("Moved")}, ByUser)
	_ = m.SetPlan(loc, b, "plan", nil, ByModel)
	c, _ := m.Create(loc, Patch{Title: ptr("Closed")}, ByUser)
	_ = m.SetPlan(loc, c, "plan", nil, ByModel)
	_ = m.Move(loc, c, InProgress, ByUser, "")
	_ = m.Move(loc, c, Review, ByUser, "")
	clock = 2_500_000
	_ = m.Move(loc, c, Done, ByUser, "")
	_ = m.Move(loc, b, InProgress, ByUser, "")
	clock = 3_000_000 // "today"
	_ = m.Move(loc, a, Abandoned, ByUser, "")

	got, err := m.History(loc, 2_000_000, 3_000_000)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].ID != c || got[1].ID != b {
		t.Fatalf("history: %+v", got)
	}
	key, p := EventKey(got[0].Events[len(got[0].Events)-1].Text)
	if key != "{from} → {to}" || p["to"] != Done {
		t.Fatalf("last event of the closed ticket: %s %v", key, p)
	}
}

func TestNext(t *testing.T) {
	list := []Summary{
		{ID: 1, Status: Todo, Priority: "normal", Size: "l", Created: 1},
		{ID: 2, Status: Todo, Priority: "high", Size: "xl", Created: 2},
		{ID: 3, Status: Todo, Priority: "normal", Size: "s", Created: 3},
		{ID: 4, Status: Todo, Priority: "critical", Blockers: []Blocker{{ID: 9, Kind: BlockDepends}}},
		{ID: 5, Status: InProgress, Priority: "low"},
		{ID: 6, Status: Todo, Priority: "low", Parent: 5, Created: 6}, // next step of a lineage in progress
		{ID: 7, Status: Todo, Priority: "normal", Created: 7},         // no size: after the sized ones
		{ID: 8, Status: Review},
		{ID: 10, Status: InProgress, FeedbackOpen: 2},
	}
	var ids []int64
	for _, t := range Next(list) {
		ids = append(ids, t.ID)
	}
	if want := []int64{6, 2, 3, 1, 7}; len(ids) != len(want) || ids[0] != 6 || ids[1] != 2 || ids[2] != 3 || ids[3] != 1 || ids[4] != 7 {
		t.Fatalf("next: %v, want %v", ids, want)
	}
	if w := Waiting(list); len(w) != 2 || w[0].ID != 8 || w[1].ID != 10 {
		t.Fatalf("waiting: %+v", w)
	}
}
