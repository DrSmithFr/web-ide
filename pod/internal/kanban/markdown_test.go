package kanban

import (
	"strings"
	"testing"
)

func TestClaudeAuthor(t *testing.T) {
	m, loc := newManager(t)
	id, _ := m.Create(loc, Patch{Title: ptr("Export"), Description: ptr("Need a CSV export")}, ByClaude)
	if err := m.Move(loc, id, InProgress, ByClaude, ""); err == nil {
		t.Fatal("Claude moved a ticket from New to In progress")
	}
	if err := m.SetPlan(loc, id, "# Plan\nsteps", []GoalInput{{Title: "exported", Description: "open the file"}}, ByClaude); err != nil {
		t.Fatal(err)
	}
	if err := m.AddNote(loc, id, "decided: comma separator", ByClaude, "claude-code"); err != nil {
		t.Fatal(err)
	}
	tk, _ := m.Get(loc, id)
	if tk.Status != Todo {
		t.Fatalf("status %s", tk.Status)
	}
	if n := tk.Notes[len(tk.Notes)-1]; n.Author != ByClaude {
		t.Fatalf("note: %+v", n)
	}
	if !CanMove(Todo, InProgress, ByClaude) || CanMove(Todo, InProgress, ByModel) || CanMove(Review, Done, ByClaude) {
		t.Fatal("moves of Claude")
	}
	md := Markdown(tk)
	for _, want := range []string{"# Ticket #1 · Export", "Status: To do · priority: Normal", "## Description\nNeed a CSV export", "(Claude, ", "decided: comma separator", "# Plan\nsteps", "- [ ] (id 1) exported\n  open the file"} {
		if !strings.Contains(md, want) {
			t.Errorf("missing %q in:\n%s", want, md)
		}
	}
}
