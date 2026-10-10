package kanban

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DrSmithFr/web-ide/pod/internal/store"
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
	id, err := m.Create(loc, Patch{Title: ptr(" Export CSV "), Description: ptr("desc"), AddFiles: []string{"a.go", "b.go"}}, ByUser)
	if err != nil || id != 1 {
		t.Fatalf("create: %v %d", err, id)
	}
	if _, err := m.Create(loc, Patch{Title: ptr("x"), Description: ptr(strings.Repeat("é", MaxDescription+1))}, ByModel); err == nil {
		t.Fatal("description too long accepted")
	}
	if _, err := m.Create(loc, Patch{Title: ptr("x"), Priority: ptr("nope")}, ByUser); err == nil {
		t.Fatal("unknown priority accepted")
	}
	// A plan moves the ticket to To do by itself.
	if err := m.SetPlan(loc, id, "# Plan", []GoalInput{{Title: "goal 1", Description: "how"}, {Title: "goal 2"}, {Title: " "}}, ByModel); err != nil {
		t.Fatal(err)
	}
	tk, _ := m.Get(loc, id)
	if tk.Status != Todo || len(tk.GoalList) != 2 || tk.GoalList[0].Description != "how" {
		t.Fatalf("after plan: %+v", tk)
	}
	if err := m.Move(loc, id, InProgress, ByModel, ""); err == nil {
		t.Fatal("model moved to in progress")
	}
	if err := m.Move(loc, id, InProgress, ByUser, ""); err != nil {
		t.Fatal(err)
	}
	// Priority and size are fixed once the development started; a plan keeps the size.
	if err := m.Update(loc, id, Patch{Priority: ptr("high")}, ByUser); err == nil {
		t.Fatal("priority changed in progress")
	}
	if err := m.Update(loc, id, Patch{Size: ptr("l")}, ByUser); err == nil {
		t.Fatal("size changed in progress")
	}
	if err := m.Update(loc, id, Patch{Size: ptr("l"), PlanSize: true, Title: ptr("Export CSV")}, ByModel); err != nil {
		t.Fatalf("plan with a size in progress: %v", err)
	}
	tk, _ = m.Get(loc, id)
	if tk.Title != "Export CSV" || len(tk.Files) != 2 || tk.Size != "" || tk.Priority != "normal" {
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
	if err := m.Move(loc, id, Review, ByModel, "ready"); err != nil {
		t.Fatal(err)
	}
	// Feedback stays in To test, the model marks it done.
	fid, err := m.Feedback(loc, id, FeedbackOp{Op: "add", Kind: "bug", Text: "the separator is wrong"}, ByUser)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Feedback(loc, id, FeedbackOp{Op: "add", Kind: "nope", Text: "x"}, ByUser); err == nil {
		t.Fatal("unknown feedback kind accepted")
	}
	if _, err := m.Feedback(loc, id, FeedbackOp{Op: "chat", ID: fid, ChatID: "c9"}, ByUser); err != nil {
		t.Fatal(err)
	}
	tk, _ = m.Get(loc, id)
	if tk.Status != Review || tk.FeedbackOpen != 1 || tk.FeedbackList[0].Kind != "bug" || tk.FeedbackList[0].ChatID != "c9" || len(tk.Files) != 1 {
		t.Fatalf("after feedback: %+v", tk)
	}
	if _, err := m.Feedback(loc, id, FeedbackOp{Op: "check", ID: fid, Done: true}, ByModel); err != nil {
		t.Fatal(err)
	}
	// A new plan keeps the goals of the user.
	if err := m.Goal(loc, id, GoalOp{Op: "add", Text: "mine"}); err != nil {
		t.Fatal(err)
	}
	if err := m.SetPlan(loc, id, "v2", []GoalInput{{Title: "g"}}, ByModel); err != nil {
		t.Fatal(err)
	}
	tk, _ = m.Get(loc, id)
	if tk.Goals != 2 || tk.FeedbackOpen != 0 || tk.Status != Review {
		t.Fatalf("after replan: %+v", tk.Summary)
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
	if events < 6 {
		t.Fatalf("events: %d %+v", events, tk.Notes)
	}
	if err := m.Move(loc, id, Abandoned, ByUser, ""); err == nil {
		t.Fatal("abandoned a closed ticket")
	}
	if err := m.Move(loc, id, Review, ByUser, ""); err != nil {
		t.Fatal("reopen to To test: ", err)
	}
}

func TestNotes(t *testing.T) {
	m, loc := newManager(t)
	id, _ := m.Create(loc, Patch{Title: ptr("t")}, ByUser)
	if err := m.AddNote(loc, id, strings.Repeat("x", MaxNote+1), ByModel, "c1"); err == nil {
		t.Fatal("note too long accepted")
	}
	if err := m.AddNote(loc, id, "decided", ByModel, "c1"); err != nil {
		t.Fatal(err)
	}
	tk, _ := m.Get(loc, id)
	if n := tk.Notes[len(tk.Notes)-1]; n.Text != "decided" || n.ChatID != "c1" || n.Author != ByModel {
		t.Fatalf("note: %+v", n)
	}
	// Writing the plan by hand also moves the ticket to To do.
	if err := m.Update(loc, id, Patch{Plan: ptr("p")}, ByUser); err != nil {
		t.Fatal(err)
	}
	if tk, _ = m.Get(loc, id); tk.Status != Todo {
		t.Fatalf("status: %s", tk.Status)
	}
}

// An older base: Ready and Fix statuses, feedback kept as goals and notes.
func TestMigration(t *testing.T) {
	m, loc := newManager(t)
	if err := os.MkdirAll(loc.IdeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", "file:"+filepath.Join(loc.IdeDir, "kanban.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(schema + `
INSERT INTO tickets (id, title, status) VALUES (1, 'a', 'ready'), (2, 'b', 'fix');
INSERT INTO goals (ticket_id, text, done, source) VALUES (2, 'plan goal', 1, 'plan'), (2, 'empty file', 1, 'feedback');
INSERT INTO notes (ticket_id, kind, text) VALUES (2, 'feedback', 'empty file'), (2, 'feedback', 'nice'), (2, 'note', 'n');`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	a, _ := m.Get(loc, 1)
	b, err := m.Get(loc, 2)
	if err != nil {
		t.Fatal(err)
	}
	if a.Status != Todo || b.Status != Review || len(b.GoalList) != 1 || len(b.Notes) != 1 || len(b.FeedbackList) != 2 {
		t.Fatalf("migrated: %+v %+v", a.Summary, b)
	}
	if f := b.FeedbackList; f[0].Kind != "bug" || !f[0].Done || f[1].Kind != "info" || f[1].Text != "nice" || b.FeedbackOpen != 1 {
		t.Fatalf("feedback: %+v", f)
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
	m.RenameChat(loc, "c1", "Renamed")
	aid, err := m.AddAttachment(loc, id, "shot.png", "image/png", []byte("png"))
	if err != nil {
		t.Fatal(err)
	}
	tk, _ := m.Get(loc, id)
	if len(tk.ChatList) != 1 || tk.ChatList[0].Role != "plan" || tk.ChatList[0].Title != "Renamed" || tk.Chats != 1 {
		t.Fatalf("chats: %+v", tk.ChatList)
	}
	if len(tk.Attachments) != 1 {
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
