package kanban

import (
	"reflect"
	"strings"
	"testing"
)

func TestLineageLinks(t *testing.T) {
	m, loc := newManager(t)
	mk := func(title string, p Patch) int64 {
		t.Helper()
		p.Title = ptr(title)
		id, err := m.Create(loc, p, ByUser)
		if err != nil {
			t.Fatalf("create %s: %v", title, err)
		}
		return id
	}
	root := mk("root", Patch{})
	c1 := mk("child 1", Patch{Parent: ptr(root)})
	c2 := mk("child 2", Patch{Parent: ptr(root)})
	other := mk("other", Patch{DependsOn: &[]int64{root}})

	tk, _ := m.Get(loc, root)
	if len(tk.Children) != 2 || tk.Children[0].ID != c1 || tk.Children[1].ID != c2 {
		t.Fatalf("children: %+v", tk.Children)
	}
	if o, _ := m.Get(loc, other); !reflect.DeepEqual(o.DependsOn, []int64{root}) {
		t.Fatalf("depends on: %v", o.DependsOn)
	}
	list, _ := m.List(loc)
	for _, s := range list {
		if s.ID == other && !reflect.DeepEqual(s.DependsOn, []int64{root}) {
			t.Fatalf("list depends on: %v", s.DependsOn)
		}
	}

	refused := func(what string, err error) {
		t.Helper()
		if err == nil {
			t.Fatalf("%s accepted", what)
		}
	}
	refused("grandchild", m.Update(loc, other, Patch{Parent: ptr(c1)}, ByUser))
	refused("parent with children as child", m.Update(loc, root, Patch{Parent: ptr(other)}, ByUser))
	refused("own parent", m.Update(loc, other, Patch{Parent: ptr(other)}, ByUser))
	refused("loop", m.Update(loc, root, Patch{DependsOn: &[]int64{other}}, ByUser))
	refused("loop through a child", m.Update(loc, c2, Patch{DependsOn: &[]int64{other}}, ByUser))
	refused("same lineage", m.Update(loc, c2, Patch{DependsOn: &[]int64{c1}}, ByUser))
	refused("unknown dependency", m.Update(loc, other, Patch{DependsOn: &[]int64{99}}, ByUser))
	refused("delete with children", m.Delete(loc, root))
	if o, _ := m.Get(loc, root); len(o.DependsOn) != 0 {
		t.Fatalf("refused change kept: %v", o.DependsOn)
	}

	// Order: the second child moves up, then the dependencies change.
	if err := m.MoveChild(loc, c2, -1, ByUser); err != nil {
		t.Fatal(err)
	}
	tk, _ = m.Get(loc, root)
	if tk.Children[0].ID != c2 {
		t.Fatalf("order after move: %+v", tk.Children)
	}
	if err := m.Update(loc, other, Patch{DependsOn: &[]int64{c1}}, ByUser); err != nil {
		t.Fatal(err)
	}
	if o, _ := m.Get(loc, other); !reflect.DeepEqual(o.DependsOn, []int64{c1}) {
		t.Fatalf("replaced dependencies: %v", o.DependsOn)
	}

	// A started child keeps its lineage; a parent is closed after its children.
	if err := m.Move(loc, c2, Todo, ByUser, ""); err != nil {
		t.Fatal(err)
	}
	if err := m.Move(loc, c2, InProgress, ByUser, ""); err != nil {
		t.Fatal(err)
	}
	refused("reparent a started child", m.Update(loc, c2, Patch{Parent: ptr(int64(0))}, ByUser))
	refused("move a started child", m.MoveChild(loc, c1, -1, ByUser))
	for _, s := range []string{Todo, InProgress, Review} {
		if err := m.Move(loc, root, s, ByUser, ""); err != nil {
			t.Fatal(err)
		}
	}
	refused("close a parent with open children", m.Move(loc, root, Done, ByUser, ""))
	refused("back to in progress once children started", m.Move(loc, root, InProgress, ByUser, ""))
	if err := m.ValidateStep(loc, root, ByUser); err != nil {
		t.Fatal(err)
	}
	refused("validate a ticket without children", m.ValidateStep(loc, other, ByUser))
	if tk, _ = m.Get(loc, root); !tk.StepDone {
		t.Fatal("step not validated")
	}
	if err := m.Update(loc, c1, Patch{Parent: ptr(int64(0))}, ByUser); err != nil {
		t.Fatal(err)
	}
	if tk, _ = m.Get(loc, root); len(tk.Children) != 1 || len(OpenChildren(tk)) != 1 {
		t.Fatalf("after removing c1: %+v", tk.Children)
	}
	if body := PRBody(tk, "en"); !strings.Contains(body, "## Steps\n- [ ] #3 child 2") {
		t.Fatalf("pull request body: %s", body)
	}
	if md := Markdown(tk); !strings.Contains(md, "## Lineage\n- Children, in order") || !strings.Contains(md, "  - #3 [In progress] child 2") {
		t.Fatalf("markdown: %s", md)
	}
}

func TestBlockers(t *testing.T) {
	all := []Summary{
		{ID: 1, Status: Review, Worktree: "/w1"},
		{ID: 2, Status: Todo, Parent: 1, Pos: 1},
		{ID: 3, Status: Todo, Parent: 1, Pos: 2},
		{ID: 4, Status: Todo, DependsOn: []int64{3}},
		{ID: 5, Status: Abandoned},
		{ID: 6, Status: New, DependsOn: []int64{5}},
		{ID: 7, Status: Todo, Parent: 8, Pos: 1},
		{ID: 8, Status: Todo},
	}
	merged := map[int64]bool{}
	of := func(id int64) []Blocker {
		for i := range all {
			if all[i].ID == id {
				return Blockers(&all[i], all, func(r *Summary) bool { return merged[r.ID] })
			}
		}
		return nil
	}
	check := func(id int64, want []Blocker) {
		t.Helper()
		if got := of(id); !reflect.DeepEqual(got, want) {
			t.Fatalf("#%d: %v, want %v", id, got, want)
		}
	}
	check(2, []Blocker{{1, BlockPrevious}})
	check(3, []Blocker{{2, BlockPrevious}})
	check(4, []Blocker{{3, BlockDepends}})
	check(6, []Blocker{{5, BlockAbandoned}})
	check(7, []Blocker{{8, BlockParent}})
	check(8, nil)

	all[0].StepDone = true
	check(2, nil)
	all[1].Status = Done
	check(3, nil)
	// A dependency on a child resolves with its lineage: merged, or the root done.
	merged[1] = true
	check(4, nil)
	merged[1] = false
	all[0].Status = Done
	check(4, nil)
}
