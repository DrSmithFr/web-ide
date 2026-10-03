package llm

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	_ "modernc.org/sqlite"

	"github.com/DrSmithFr/web-ide/pod/internal/i18n"
)

// Conversations are kept in a SQLite base: <project>/.ide/chats.db for a local project
// (ignored by git through .ide/.gitignore), ~/.web-ide/chats/<project>.db for a remote one.
// The page owns the message format; the pod stores each message as JSON.

var idPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

type ChatInfo struct {
	ID      string `json:"id"`
	Title   string `json:"title"`
	Updated int64  `json:"updated"`
	Model   string `json:"model,omitempty"`
}

// ChatLocation says where the conversations of a project live.
type ChatLocation struct {
	Project string
	// IdeDir is the .ide folder of a local project ("" for a remote one).
	IdeDir string
}

const schema = `
PRAGMA journal_mode = WAL;
CREATE TABLE IF NOT EXISTS chats (
  id TEXT PRIMARY KEY,
  title TEXT NOT NULL DEFAULT '',
  created INTEGER NOT NULL DEFAULT 0,
  updated INTEGER NOT NULL DEFAULT 0,
  server TEXT NOT NULL DEFAULT '',
  model TEXT NOT NULL DEFAULT '',
  extra TEXT NOT NULL DEFAULT '{}'
);
CREATE TABLE IF NOT EXISTS messages (
  chat_id TEXT NOT NULL REFERENCES chats(id) ON DELETE CASCADE,
  seq INTEGER NOT NULL,
  role TEXT NOT NULL,
  data TEXT NOT NULL,
  PRIMARY KEY (chat_id, seq)
);
CREATE INDEX IF NOT EXISTS chats_updated ON chats(updated DESC);
`

func (m *Manager) chatDB(loc ChatLocation) (*sql.DB, error) {
	if !idPattern.MatchString(loc.Project) {
		return nil, i18n.New("invalid project")
	}
	path := m.st.Path("chats", loc.Project+".db")
	if loc.IdeDir != "" {
		if err := ensureIdeDir(loc.IdeDir); err == nil {
			path = filepath.Join(loc.IdeDir, "chats.db")
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
	m.dbs[path] = db
	m.importJSON(db, loc.Project)
	return db, nil
}

// ensureIdeDir creates .ide with a .gitignore keeping the base out of the repository.
func ensureIdeDir(dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	gi := filepath.Join(dir, ".gitignore")
	data, err := os.ReadFile(gi)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if strings.Contains(string(data), "chats.db") {
		return nil
	}
	text := string(data)
	if text != "" && !strings.HasSuffix(text, "\n") {
		text += "\n"
	}
	text += "# Conversations of the AI assistant (local only)\nchats.db\nchats.db-*\n"
	return os.WriteFile(gi, []byte(text), 0o644)
}

// importJSON moves the conversations of the first versions (chats/<project>/*.json).
func (m *Manager) importJSON(db *sql.DB, project string) {
	dir := m.st.Path("chats", project)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		p := filepath.Join(dir, e.Name())
		data, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		if saveChat(db, data) == nil {
			os.Remove(p)
		}
	}
	os.Remove(dir) // only when empty
}

func (m *Manager) ListChats(loc ChatLocation) ([]ChatInfo, error) {
	db, err := m.chatDB(loc)
	if err != nil {
		return nil, err
	}
	rows, err := db.Query(`SELECT id, title, updated, model FROM chats ORDER BY updated DESC LIMIT 500`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	list := []ChatInfo{}
	for rows.Next() {
		var c ChatInfo
		if err := rows.Scan(&c.ID, &c.Title, &c.Updated, &c.Model); err != nil {
			return nil, err
		}
		list = append(list, c)
	}
	return list, rows.Err()
}

func (m *Manager) GetChat(loc ChatLocation, id string) (json.RawMessage, error) {
	db, err := m.chatDB(loc)
	if err != nil {
		return nil, err
	}
	var title, server, model, extra string
	var created, updated int64
	err = db.QueryRow(`SELECT title, created, updated, server, model, extra FROM chats WHERE id = ?`, id).Scan(&title, &created, &updated, &server, &model, &extra)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, i18n.New("conversation not found")
	}
	if err != nil {
		return nil, err
	}
	chat := map[string]any{}
	_ = json.Unmarshal([]byte(extra), &chat)
	rows, err := db.Query(`SELECT data FROM messages WHERE chat_id = ? ORDER BY seq`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	msgs := []json.RawMessage{}
	for rows.Next() {
		var d string
		if err := rows.Scan(&d); err != nil {
			return nil, err
		}
		msgs = append(msgs, json.RawMessage(d))
	}
	chat["id"], chat["title"], chat["created"], chat["updated"] = id, title, created, updated
	chat["server"], chat["model"], chat["messages"] = server, model, msgs
	return json.Marshal(chat)
}

func (m *Manager) SaveChat(loc ChatLocation, chat json.RawMessage) error {
	db, err := m.chatDB(loc)
	if err != nil {
		return err
	}
	return saveChat(db, chat)
}

func saveChat(db *sql.DB, data json.RawMessage) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	var c struct {
		ID       string            `json:"id"`
		Title    string            `json:"title"`
		Created  int64             `json:"created"`
		Updated  int64             `json:"updated"`
		Server   string            `json:"server"`
		Model    string            `json:"model"`
		Messages []json.RawMessage `json:"messages"`
	}
	if err := json.Unmarshal(data, &c); err != nil {
		return err
	}
	if !idPattern.MatchString(c.ID) {
		return i18n.New("invalid conversation id")
	}
	for _, k := range []string{"id", "title", "created", "updated", "server", "model", "messages"} {
		delete(fields, k)
	}
	extra, _ := json.Marshal(fields)
	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`INSERT INTO chats (id, title, created, updated, server, model, extra) VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET title = excluded.title, updated = excluded.updated, server = excluded.server, model = excluded.model, extra = excluded.extra`,
		c.ID, c.Title, c.Created, c.Updated, c.Server, c.Model, string(extra)); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM messages WHERE chat_id = ?`, c.ID); err != nil {
		return err
	}
	stmt, err := tx.Prepare(`INSERT INTO messages (chat_id, seq, role, data) VALUES (?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for i, msg := range c.Messages {
		var r struct{ Role string }
		_ = json.Unmarshal(msg, &r)
		if _, err := stmt.Exec(c.ID, i, r.Role, string(msg)); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (m *Manager) RenameChat(loc ChatLocation, id, title string) error {
	db, err := m.chatDB(loc)
	if err != nil {
		return err
	}
	_, err = db.Exec(`UPDATE chats SET title = ? WHERE id = ?`, strings.TrimSpace(title), id)
	return err
}

func (m *Manager) DeleteChat(loc ChatLocation, id string) error {
	db, err := m.chatDB(loc)
	if err != nil {
		return err
	}
	_, err = db.Exec(`DELETE FROM chats WHERE id = ?`, id)
	return err
}

// Close closes the conversation bases.
func (m *Manager) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for p, db := range m.dbs {
		db.Close()
		delete(m.dbs, p)
	}
}
