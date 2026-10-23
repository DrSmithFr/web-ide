package kanban

import (
	"database/sql"
	"path/filepath"
	"testing"
)

// An older base (version 6) holds To test as `review`: opening it migrates the status.
func TestMigrateReviewToToTest(t *testing.T) {
	m, loc := newManager(t)
	id, err := m.Create(loc, Patch{Title: ptr("Old ticket")}, ByUser)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.db(loc); err != nil {
		t.Fatal(err)
	}
	m.Close()
	raw, err := sql.Open("sqlite", "file:"+filepath.Join(loc.IdeDir, "kanban.db"))
	if err != nil {
		t.Fatal(err)
	}
	// What the later versions added goes, as in a base of version 6.
	if _, err := raw.Exec(`UPDATE tickets SET status = 'review';
ALTER TABLE tickets DROP COLUMN objective_id; DROP TABLE objective_notes; DROP TABLE objectives;
ALTER TABLE tickets DROP COLUMN milestone_id;
DROP TABLE milestone_notes; DROP TABLE milestone_chats; DROP TABLE milestone_feedback; DROP TABLE stories; DROP TABLE milestones;
PRAGMA user_version = 6`); err != nil {
		t.Fatal(err)
	}
	raw.Close()

	tk, err := m.Get(loc, id)
	if err != nil {
		t.Fatal(err)
	}
	if tk.Status != ToTest {
		t.Fatalf("status after migration: %q", tk.Status)
	}
	var v int
	db, _ := m.db(loc)
	if err := db.QueryRow(`PRAGMA user_version`).Scan(&v); err != nil || v != len(migrations) {
		t.Fatalf("version: %d %v", v, err)
	}
}

// `review` stays an alias of to_test on input.
func TestReviewAlias(t *testing.T) {
	if NormStatus("review") != ToTest || NormStatus("todo") != Todo {
		t.Fatal("NormStatus")
	}
	if !CanMove(InProgress, "review", ByModel) || CanMove(Todo, "review", ByModel) {
		t.Fatal("CanMove with the alias")
	}
}
