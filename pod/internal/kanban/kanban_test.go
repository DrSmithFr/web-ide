package kanban

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"webide/pod/internal/store"
)

func newManager(t *testing.T) (*Manager, Location) {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	m := NewManager(st)
	t.Cleanup(m.Close)
	return m, Location{Project: "p1", IdeDir: filepath.Join(t.TempDir(), ".ide")}
}

func ptr[T any](v T) *T { return &v }

func TestTicketLifecycle(t *testing.T) {
	m, loc := newManager(t)
	id, err := m.Create(loc, Patch{Title: ptr(" Export CSV "), Type: ptr("feature"), Description: ptr("desc"), AddFiles: []string{"a.go", "b.go"}}, ByUser)
	if err != nil || id != 1 {
		t.Fatalf("create: %v %d", err, id)
	}
	if _, err := m.Create(loc, Patch{Title: ptr("x"), Type: ptr("nope")}, ByUser); err == nil {
		t.Fatal("unknown type accepted")
	}
	if err := m.SetPlan(loc, id, "# Plan", []string{"goal 1", "goal 2", " "}, ByModel); err != nil {
		t.Fatal(err)
	}
	// The model may move New → Ready but not Ready → In progress.
	if err := m.Move(loc, id, Ready, ByModel, ""); err != nil {
		t.Fatal(err)
	}
	if err := m.Move(loc, id, InProgress, ByModel, ""); err == nil {
		t.Fatal("model moved to in progress")
	}
	if err := m.Move(loc, id, InProgress, ByUser, ""); err != nil {
		t.Fatal(err)
	}
	tk, _ := m.Get(loc, id)
	if len(tk.GoalList) != 2 || tk.Title != "Export CSV" || len(tk.Files) != 2 {
		t.Fatalf("ticket: %+v", tk)
	}
	for _, g := range tk.GoalList {
		if err := m.Goal(loc, id, GoalOp{Op: "check", ID: g.ID, Done: true}); err != nil {
			t.Fatal(err)
		}
	}
	if err := m.Update(loc, id, Patch{TestSummary: ptr("tester l'export"), RemoveFiles: []string{"a.go"}}, ByModel); err != nil {
		t.Fatal(err)
	}
	if err := m.Move(loc, id, Review, ByModel, "prêt"); err != nil {
		t.Fatal(err)
	}
	// A feedback becomes a goal and sends the ticket to Fix.
	if err := m.AddNote(loc, id, "feedback", "le séparateur est faux", ByUser); err != nil {
		t.Fatal(err)
	}
	tk, _ = m.Get(loc, id)
	if tk.Status != Fix || tk.Goals != 3 || tk.GoalsDone != 2 || tk.GoalList[2].Source != "feedback" || len(tk.Files) != 1 {
		t.Fatalf("after feedback: %+v", tk.Summary)
	}
	// A new plan keeps the feedback goals.
	if err := m.SetPlan(loc, id, "v2", []string{"g"}, ByModel); err != nil {
		t.Fatal(err)
	}
	tk, _ = m.Get(loc, id)
	if tk.Goals != 2 {
		t.Fatalf("goals after replan: %d", tk.Goals)
	}
	if err := m.Move(loc, id, Review, ByModel, ""); err != nil {
		t.Fatal(err)
	}
	if err := m.Move(loc, id, Done, ByModel, ""); err == nil {
		t.Fatal("model closed the ticket")
	}
	if err := m.Move(loc, id, Done, ByUser, ""); err != nil {
		t.Fatal(err)
	}
	tk, _ = m.Get(loc, id)
	if tk.Closed == 0 {
		t.Fatal("closed not set")
	}
	events := 0
	for _, n := range tk.Notes {
		if n.Kind == "event" {
			events++
		}
	}
	if events < 7 {
		t.Fatalf("events: %d %+v", events, tk.Notes)
	}
	if err := m.Move(loc, id, Abandoned, ByUser, ""); err == nil {
		t.Fatal("abandoned a closed ticket")
	}
}

func TestLinksAndAttachments(t *testing.T) {
	m, loc := newManager(t)
	id, _ := m.Create(loc, Patch{Title: ptr("t")}, ByUser)
	if err := m.LinkChat(loc, id, "c1", "briefing", "Briefing"); err != nil {
		t.Fatal(err)
	}
	if err := m.LinkChat(loc, id, "c1", "plan", ""); err != nil {
		t.Fatal(err)
	}
	if err := m.LinkChat(loc, id, "c2", "bad", ""); err == nil {
		t.Fatal("bad role accepted")
	}
	m.RenameChat(loc, "c1", "Renommée")
	if err := m.LinkCommit(loc, id, "abc", "#1 fix"); err != nil {
		t.Fatal(err)
	}
	aid, err := m.AddAttachment(loc, id, "shot.png", "image/png", []byte("png"))
	if err != nil {
		t.Fatal(err)
	}
	tk, _ := m.Get(loc, id)
	if len(tk.ChatList) != 1 || tk.ChatList[0].Role != "plan" || tk.ChatList[0].Title != "Renommée" || tk.Chats != 1 {
		t.Fatalf("chats: %+v", tk.ChatList)
	}
	if len(tk.Commits) != 1 || len(tk.Attachments) != 1 {
		t.Fatalf("links: %+v", tk)
	}
	a, data, err := m.Attachment(loc, id, aid)
	if err != nil || a.Name != "shot.png" || string(data) != "png" {
		t.Fatalf("attachment: %v %+v", err, a)
	}
	snap := &Snapshot{Base: "b", Head: "h", Files: []DiffFile{{Path: "x", Status: "M"}}}
	if err := m.SetGit(loc, id, GitState{Branch: ptr("ticket/1-t"), Snapshot: snap}); err != nil {
		t.Fatal(err)
	}
	tk, _ = m.Get(loc, id)
	if tk.Branch != "ticket/1-t" || tk.Snapshot == nil || tk.Snapshot.Files[0].Path != "x" {
		t.Fatalf("git: %+v", tk)
	}
	if err := m.Delete(loc, id); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Get(loc, id); err != ErrNotFound {
		t.Fatalf("deleted: %v", err)
	}
	// Numbers are never reused.
	id2, _ := m.Create(loc, Patch{Title: ptr("u")}, ByUser)
	if id2 != 2 {
		t.Fatalf("id reused: %d", id2)
	}
	gi, _ := os.ReadFile(filepath.Join(loc.IdeDir, ".gitignore"))
	if !strings.Contains(string(gi), "kanban.db") || !strings.Contains(string(gi), "worktrees/") {
		t.Fatalf("gitignore: %s", gi)
	}
	if _, err := os.Stat(filepath.Join(loc.IdeDir, "kanban.db")); err != nil {
		t.Fatal(err)
	}
}
