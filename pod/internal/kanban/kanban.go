// Package kanban keeps the tickets of a project in a SQLite base: <project>/.ide/kanban.db
// for a local project (ignored by git), ~/.web-ide/kanban/<project>.db for a remote one.
// See docs/kanban.md for the workflow.
package kanban

import (
	"database/sql"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"

	"github.com/DrSmithFr/web-ide/pod/internal/i18n"
	"github.com/DrSmithFr/web-ide/pod/internal/store"
)

// Statuses of a ticket.
const (
	New        = "new"         // backlog without a plan
	Todo       = "todo"        // backlog with a plan, shown as “To do”
	InProgress = "in_progress" // en cours
	Review     = "review"      // shown as “To test”
	Done       = "done"        // closed
	Abandoned  = "abandoned"
)

var (
	Statuses      = []string{New, Todo, InProgress, Review, Done, Abandoned}
	Priorities    = []string{"low", "normal", "high", "critical"}
	ChatRoles     = []string{"briefing", "plan", "dev", "correction", "resolve"}
	FeedbackKinds = []string{"info", "bug", "feature"}
)

// Longest texts, in characters: the model tends to be verbose.
const (
	MaxDescription = 1500
	MaxNote        = 1000
)

// Actors of a change: the model may only do some transitions.
const (
	ByUser  = "user"
	ByModel = "model"
)

// modelMoves are the transitions the model may do (docs/kanban.md). New → To do also
// happens by itself when a plan is written.
var modelMoves = map[[2]string]bool{
	{New, Todo}: true, {InProgress, Review}: true,
}

// userMoves are the transitions of the buttons. Done and Abandoned are reached through
// Close and Abandon.
var userMoves = map[[2]string]bool{
	{New, Todo}: true, {Todo, New}: true, {Todo, InProgress}: true, {InProgress, Review}: true,
	{Review, InProgress}: true, {Review, Done}: true, {Done, Review}: true, {Abandoned, New}: true,
}

// CanMove tells whether an actor may move a ticket from one status to another.
func CanMove(from, to, by string) bool {
	if to == Abandoned && by == ByUser {
		return from != Done && from != Abandoned
	}
	if by == ByModel {
		return modelMoves[[2]string{from, to}]
	}
	return userMoves[[2]string{from, to}]
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

var projectPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// Location says where the base of a project lives.
type Location struct {
	Project string
	// IdeDir is the .ide folder of a local project ("" for a remote one).
	IdeDir string
}

type Manager struct {
	st  *store.Store
	mu  sync.Mutex
	dbs map[string]*sql.DB
	// Now is replaced by the tests.
	Now func() int64
}

func NewManager(st *store.Store) *Manager {
	return &Manager{st: st, dbs: map[string]*sql.DB{}, Now: func() int64 { return time.Now().UnixMilli() }}
}

const schema = `
PRAGMA journal_mode = WAL;
CREATE TABLE IF NOT EXISTS tickets (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  title TEXT NOT NULL DEFAULT '',
  type TEXT NOT NULL DEFAULT 'feature',
  priority TEXT NOT NULL DEFAULT 'normal',
  status TEXT NOT NULL DEFAULT 'new',
  description TEXT NOT NULL DEFAULT '',
  plan TEXT NOT NULL DEFAULT '',
  test_summary TEXT NOT NULL DEFAULT '',
  branch TEXT NOT NULL DEFAULT '',
  base TEXT NOT NULL DEFAULT '',
  worktree TEXT NOT NULL DEFAULT '',
  setup TEXT NOT NULL DEFAULT '',
  setup_log TEXT NOT NULL DEFAULT '',
  snapshot TEXT NOT NULL DEFAULT '',
  created INTEGER NOT NULL DEFAULT 0,
  updated INTEGER NOT NULL DEFAULT 0,
  closed INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE IF NOT EXISTS goals (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  ticket_id INTEGER NOT NULL REFERENCES tickets(id) ON DELETE CASCADE,
  pos INTEGER NOT NULL DEFAULT 0,
  text TEXT NOT NULL,
  done INTEGER NOT NULL DEFAULT 0,
  source TEXT NOT NULL DEFAULT 'plan',
  created INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE IF NOT EXISTS notes (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  ticket_id INTEGER NOT NULL REFERENCES tickets(id) ON DELETE CASCADE,
  kind TEXT NOT NULL DEFAULT 'note',
  author TEXT NOT NULL DEFAULT 'user',
  text TEXT NOT NULL,
  created INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE IF NOT EXISTS files (
  ticket_id INTEGER NOT NULL REFERENCES tickets(id) ON DELETE CASCADE,
  path TEXT NOT NULL,
  PRIMARY KEY (ticket_id, path)
);
CREATE TABLE IF NOT EXISTS chats (
  ticket_id INTEGER NOT NULL REFERENCES tickets(id) ON DELETE CASCADE,
  chat_id TEXT NOT NULL,
  role TEXT NOT NULL,
  title TEXT NOT NULL DEFAULT '',
  created INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (ticket_id, chat_id)
);
CREATE TABLE IF NOT EXISTS commits (
  ticket_id INTEGER NOT NULL REFERENCES tickets(id) ON DELETE CASCADE,
  hash TEXT NOT NULL,
  subject TEXT NOT NULL DEFAULT '',
  created INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (ticket_id, hash)
);
CREATE TABLE IF NOT EXISTS attachments (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  ticket_id INTEGER NOT NULL REFERENCES tickets(id) ON DELETE CASCADE,
  name TEXT NOT NULL,
  mime TEXT NOT NULL DEFAULT '',
  size INTEGER NOT NULL DEFAULT 0,
  data BLOB NOT NULL,
  created INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE IF NOT EXISTS meta (
  key TEXT PRIMARY KEY,
  value TEXT NOT NULL
);
`

// migrations bring an older base up to date, one per version (PRAGMA user_version).
var migrations = []string{
	// 1: statuses New, To do, In progress, To test (Ready → To do, Fix → To test); test
	// feedback in its own table (feedback goals and notes moved there); goals get a
	// description, notes the conversation that wrote them.
	`ALTER TABLE goals ADD COLUMN description TEXT NOT NULL DEFAULT '';
ALTER TABLE notes ADD COLUMN chat_id TEXT NOT NULL DEFAULT '';
CREATE TABLE feedback (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  ticket_id INTEGER NOT NULL REFERENCES tickets(id) ON DELETE CASCADE,
  kind TEXT NOT NULL DEFAULT 'info',
  text TEXT NOT NULL,
  done INTEGER NOT NULL DEFAULT 0,
  author TEXT NOT NULL DEFAULT 'user',
  chat_id TEXT NOT NULL DEFAULT '',
  created INTEGER NOT NULL DEFAULT 0
);
UPDATE tickets SET status = 'todo' WHERE status = 'ready';
UPDATE tickets SET status = 'review' WHERE status = 'fix';
INSERT INTO feedback (ticket_id, kind, text, done, created) SELECT ticket_id, 'bug', text, done, created FROM goals WHERE source = 'feedback';
INSERT INTO feedback (ticket_id, kind, text, author, created) SELECT n.ticket_id, 'info', n.text, n.author, n.created FROM notes n
  WHERE n.kind = 'feedback' AND NOT EXISTS (SELECT 1 FROM goals g WHERE g.ticket_id = n.ticket_id AND g.source = 'feedback' AND g.text = TRIM(n.text));
DELETE FROM goals WHERE source = 'feedback';
DELETE FROM notes WHERE kind = 'feedback';`,
}

func migrate(db *sql.DB) error {
	var version int
	if err := db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
		return err
	}
	for ; version < len(migrations); version++ {
		tx, err := db.Begin()
		if err != nil {
			return err
		}
		if _, err := tx.Exec(migrations[version]); err != nil {
			tx.Rollback()
			return err
		}
		if _, err := tx.Exec(`PRAGMA user_version = ` + strconv.Itoa(version+1)); err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

func (m *Manager) db(loc Location) (*sql.DB, error) {
	if !projectPattern.MatchString(loc.Project) {
		return nil, i18n.New("invalid project")
	}
	path := m.st.Path("kanban", loc.Project+".db")
	if loc.IdeDir != "" {
		if err := EnsureIgnored(loc.IdeDir); err == nil {
			path = filepath.Join(loc.IdeDir, "kanban.db")
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if db := m.dbs[path]; db != nil {
		return db, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, err
	}
	if err := migrate(db); err != nil {
		db.Close()
		return nil, err
	}
	m.dbs[path] = db
	return db, nil
}

// ignored lists what .ide/.gitignore keeps out of the repository for the kanban.
var ignored = []string{"kanban.db", "kanban.db-*", "worktrees/"}

// EnsureIgnored creates .ide and adds the kanban base and the worktrees to its .gitignore.
func EnsureIgnored(dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	gi := filepath.Join(dir, ".gitignore")
	data, err := os.ReadFile(gi)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if text, changed := WithIgnored(string(data)); changed {
		return os.WriteFile(gi, []byte(text), 0o644)
	}
	return nil
}

// WithIgnored returns the .gitignore text with the kanban entries, and whether it changed.
func WithIgnored(text string) (string, bool) {
	lines := map[string]bool{}
	for _, l := range strings.Split(text, "\n") {
		lines[strings.TrimSpace(l)] = true
	}
	var missing []string
	for _, l := range ignored {
		if !lines[l] {
			missing = append(missing, l)
		}
	}
	if len(missing) == 0 {
		return text, false
	}
	if text != "" && !strings.HasSuffix(text, "\n") {
		text += "\n"
	}
	return text + "# Kanban of the IDE and worktrees of its tickets (local only)\n" + strings.Join(missing, "\n") + "\n", true
}

// Close closes the bases.
func (m *Manager) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for p, db := range m.dbs {
		db.Close()
		delete(m.dbs, p)
	}
}
