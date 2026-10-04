package kanban

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/DrSmithFr/web-ide/pod/internal/i18n"
)

type Goal struct {
	ID          int64  `json:"id"`
	Text        string `json:"text"` // title
	Description string `json:"description"`
	Done        bool   `json:"done"`
	Source      string `json:"source"` // plan | user
}

type Note struct {
	ID      int64  `json:"id"`
	Kind    string `json:"kind"`   // note | event
	Author  string `json:"author"` // user | model
	Text    string `json:"text"`
	ChatID  string `json:"chatId,omitempty"`
	Created int64  `json:"created"`
}

// Feedback is a return of the user testing the ticket; the model marks it done once handled.
type Feedback struct {
	ID      int64  `json:"id"`
	Kind    string `json:"kind"` // info | bug | feature
	Text    string `json:"text"`
	Done    bool   `json:"done"`
	Author  string `json:"author"`
	ChatID  string `json:"chatId,omitempty"`
	Created int64  `json:"created"`
}

type ChatLink struct {
	ChatID  string `json:"chatId"`
	Role    string `json:"role"`
	Title   string `json:"title"`
	Created int64  `json:"created"`
}

type CommitLink struct {
	Hash    string `json:"hash"`
	Subject string `json:"subject"`
}

type Attachment struct {
	ID      int64  `json:"id"`
	Name    string `json:"name"`
	Mime    string `json:"mime"`
	Size    int64  `json:"size"`
	Created int64  `json:"created"`
}

// Snapshot is the change of a ticket frozen when it is closed (its worktree is removed).
type Snapshot struct {
	Base  string     `json:"base"`
	Head  string     `json:"head"`
	Files []DiffFile `json:"files"`
	Patch string     `json:"patch"`
}

type DiffFile struct {
	Path    string `json:"path"`
	Status  string `json:"status"` // A M D R…
	Added   int    `json:"added"`
	Removed int    `json:"removed"`
}

// Summary is a ticket as the board shows it.
type Summary struct {
	ID        int64  `json:"id"`
	Title     string `json:"title"`
	Priority  string `json:"priority"`
	Status    string `json:"status"`
	Branch    string `json:"branch,omitempty"`
	Worktree  string `json:"worktree,omitempty"`
	GoalsDone int    `json:"goalsDone"`
	Goals     int    `json:"goals"`
	Chats     int    `json:"chats"`
	// Feedback not handled yet.
	FeedbackOpen int   `json:"feedbackOpen"`
	Created   int64  `json:"created"`
	Updated   int64  `json:"updated"`
	Closed    int64  `json:"closed,omitempty"`
}

type Ticket struct {
	Summary
	Description string       `json:"description"`
	Plan        string       `json:"plan"`
	TestSummary string       `json:"testSummary"`
	PR          string       `json:"pr,omitempty"`
	Base        string       `json:"base"`
	Setup       string       `json:"setup"`
	SetupLog    string       `json:"setupLog,omitempty"`
	Snapshot    *Snapshot    `json:"snapshot,omitempty"`
	GoalList    []Goal       `json:"goalList"`
	Notes       []Note       `json:"notes"`
	FeedbackList []Feedback  `json:"feedbackList"`
	Files       []string     `json:"files"`
	ChatList    []ChatLink   `json:"chatList"`
	Commits     []CommitLink `json:"commits"`
	Attachments []Attachment `json:"attachments"`
}

var ErrNotFound = i18n.New("ticket not found")

const summaryCols = `t.id, t.title, t.priority, t.status, t.branch, t.worktree, t.created, t.updated, t.closed,
  (SELECT COUNT(*) FROM goals g WHERE g.ticket_id = t.id AND g.done = 1),
  (SELECT COUNT(*) FROM goals g WHERE g.ticket_id = t.id),
  (SELECT COUNT(*) FROM chats c WHERE c.ticket_id = t.id),
  (SELECT COUNT(*) FROM feedback f WHERE f.ticket_id = t.id AND f.done = 0)`

func scanSummary(row interface{ Scan(...any) error }, s *Summary) error {
	return row.Scan(&s.ID, &s.Title, &s.Priority, &s.Status, &s.Branch, &s.Worktree, &s.Created, &s.Updated, &s.Closed, &s.GoalsDone, &s.Goals, &s.Chats, &s.FeedbackOpen)
}

func (m *Manager) List(loc Location) ([]Summary, error) {
	db, err := m.db(loc)
	if err != nil {
		return nil, err
	}
	rows, err := db.Query(`SELECT ` + summaryCols + ` FROM tickets t ORDER BY t.updated DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	list := []Summary{}
	for rows.Next() {
		var s Summary
		if err := scanSummary(rows, &s); err != nil {
			return nil, err
		}
		list = append(list, s)
	}
	return list, rows.Err()
}

func (m *Manager) Get(loc Location, id int64) (*Ticket, error) {
	db, err := m.db(loc)
	if err != nil {
		return nil, err
	}
	return get(db, id)
}

func get(db *sql.DB, id int64) (*Ticket, error) {
	t := &Ticket{GoalList: []Goal{}, Notes: []Note{}, FeedbackList: []Feedback{}, Files: []string{}, ChatList: []ChatLink{}, Commits: []CommitLink{}, Attachments: []Attachment{}}
	var snap string
	row := db.QueryRow(`SELECT `+summaryCols+`, t.description, t.plan, t.test_summary, t.pr, t.base, t.setup, t.setup_log, t.snapshot FROM tickets t WHERE t.id = ?`, id)
	err := row.Scan(&t.ID, &t.Title, &t.Priority, &t.Status, &t.Branch, &t.Worktree, &t.Created, &t.Updated, &t.Closed, &t.GoalsDone, &t.Goals, &t.Chats, &t.FeedbackOpen,
		&t.Description, &t.Plan, &t.TestSummary, &t.PR, &t.Base, &t.Setup, &t.SetupLog, &snap)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if snap != "" {
		t.Snapshot = &Snapshot{}
		_ = json.Unmarshal([]byte(snap), t.Snapshot)
	}
	each := func(q string, scan func(*sql.Rows) error) error {
		rows, err := db.Query(q, id)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			if err := scan(rows); err != nil {
				return err
			}
		}
		return rows.Err()
	}
	if err := each(`SELECT id, text, description, done, source FROM goals WHERE ticket_id = ? ORDER BY pos, id`, func(r *sql.Rows) error {
		var g Goal
		err := r.Scan(&g.ID, &g.Text, &g.Description, &g.Done, &g.Source)
		t.GoalList = append(t.GoalList, g)
		return err
	}); err != nil {
		return nil, err
	}
	if err := each(`SELECT id, kind, author, text, chat_id, created FROM notes WHERE ticket_id = ? ORDER BY created, id`, func(r *sql.Rows) error {
		var n Note
		err := r.Scan(&n.ID, &n.Kind, &n.Author, &n.Text, &n.ChatID, &n.Created)
		t.Notes = append(t.Notes, n)
		return err
	}); err != nil {
		return nil, err
	}
	if err := each(`SELECT id, kind, text, done, author, chat_id, created FROM feedback WHERE ticket_id = ? ORDER BY created, id`, func(r *sql.Rows) error {
		var f Feedback
		err := r.Scan(&f.ID, &f.Kind, &f.Text, &f.Done, &f.Author, &f.ChatID, &f.Created)
		t.FeedbackList = append(t.FeedbackList, f)
		return err
	}); err != nil {
		return nil, err
	}
	if err := each(`SELECT path FROM files WHERE ticket_id = ? ORDER BY path`, func(r *sql.Rows) error {
		var p string
		err := r.Scan(&p)
		t.Files = append(t.Files, p)
		return err
	}); err != nil {
		return nil, err
	}
	if err := each(`SELECT chat_id, role, title, created FROM chats WHERE ticket_id = ? ORDER BY created`, func(r *sql.Rows) error {
		var c ChatLink
		err := r.Scan(&c.ChatID, &c.Role, &c.Title, &c.Created)
		t.ChatList = append(t.ChatList, c)
		return err
	}); err != nil {
		return nil, err
	}
	if err := each(`SELECT hash, subject FROM commits WHERE ticket_id = ? ORDER BY created`, func(r *sql.Rows) error {
		var c CommitLink
		err := r.Scan(&c.Hash, &c.Subject)
		t.Commits = append(t.Commits, c)
		return err
	}); err != nil {
		return nil, err
	}
	if err := each(`SELECT id, name, mime, size, created FROM attachments WHERE ticket_id = ? ORDER BY id`, func(r *sql.Rows) error {
		var a Attachment
		err := r.Scan(&a.ID, &a.Name, &a.Mime, &a.Size, &a.Created)
		t.Attachments = append(t.Attachments, a)
		return err
	}); err != nil {
		return nil, err
	}
	return t, nil
}

// Patch holds the fields to change (nil: unchanged).
type Patch struct {
	Title       *string   `json:"title"`
	Priority    *string   `json:"priority"`
	Description *string   `json:"description"`
	Plan        *string   `json:"plan"`
	TestSummary *string   `json:"testSummary"`
	Base        *string   `json:"base"`
	Files       *[]string `json:"files"`
	AddFiles    []string  `json:"addFiles"`
	RemoveFiles []string  `json:"removeFiles"`
}

func (p *Patch) validate() error {
	if p.Title != nil && strings.TrimSpace(*p.Title) == "" {
		return i18n.New("empty title")
	}
	if p.Description != nil {
		if n := utf8.RuneCountInString(strings.TrimSpace(*p.Description)); n > MaxDescription {
			return i18n.Errorf("description too long (%d characters, %d max)", n, MaxDescription)
		}
	}
	if p.Priority != nil && !contains(Priorities, *p.Priority) {
		return i18n.Errorf("unknown priority: %s (%s)", *p.Priority, strings.Join(Priorities, ", "))
	}
	return nil
}

// tx runs f in a transaction and marks the ticket updated.
func (m *Manager) tx(loc Location, id int64, f func(tx *sql.Tx, now int64) error) error {
	db, err := m.db(loc)
	if err != nil {
		return err
	}
	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := m.Now()
	if id != 0 {
		res, err := tx.Exec(`UPDATE tickets SET updated = ? WHERE id = ?`, now, id)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return ErrNotFound
		}
	}
	if err := f(tx, now); err != nil {
		return err
	}
	return tx.Commit()
}

// Params are the values of a history line; EventText stores it as {"key", "params"}: the
// page translates the key (English text with {name} placeholders).
type Params map[string]any

func EventText(key string, p Params) string {
	data, _ := json.Marshal(struct {
		Key    string `json:"key"`
		Params Params `json:"params,omitempty"`
	}{key, p})
	return string(data)
}

func event(tx *sql.Tx, id int64, by, key string, p Params, now int64) error {
	_, err := tx.Exec(`INSERT INTO notes (ticket_id, kind, author, text, created) VALUES (?, 'event', ?, ?, ?)`, id, by, EventText(key, p), now)
	return err
}

// Create adds a ticket in the New status and returns its number.
func (m *Manager) Create(loc Location, p Patch, by string) (int64, error) {
	if p.Title == nil {
		return 0, i18n.New("title is missing")
	}
	if err := p.validate(); err != nil {
		return 0, err
	}
	var id int64
	err := m.tx(loc, 0, func(tx *sql.Tx, now int64) error {
		prio := "normal"
		if p.Priority != nil {
			prio = *p.Priority
		}
		desc := ""
		if p.Description != nil {
			desc = strings.TrimSpace(*p.Description)
		}
		res, err := tx.Exec(`INSERT INTO tickets (title, priority, status, description, created, updated) VALUES (?, ?, 'new', ?, ?, ?)`,
			strings.TrimSpace(*p.Title), prio, desc, now, now)
		if err != nil {
			return err
		}
		id, _ = res.LastInsertId()
		if err := setFiles(tx, id, p); err != nil {
			return err
		}
		return event(tx, id, by, "Ticket created", nil, now)
	})
	return id, err
}

func setFiles(tx *sql.Tx, id int64, p Patch) error {
	if p.Files != nil {
		if _, err := tx.Exec(`DELETE FROM files WHERE ticket_id = ?`, id); err != nil {
			return err
		}
		p.AddFiles = append(append([]string{}, *p.Files...), p.AddFiles...)
	}
	for _, f := range p.AddFiles {
		if f = strings.TrimSpace(f); f != "" {
			if _, err := tx.Exec(`INSERT OR IGNORE INTO files (ticket_id, path) VALUES (?, ?)`, id, f); err != nil {
				return err
			}
		}
	}
	for _, f := range p.RemoveFiles {
		if _, err := tx.Exec(`DELETE FROM files WHERE ticket_id = ? AND path = ?`, id, f); err != nil {
			return err
		}
	}
	return nil
}

func (m *Manager) Update(loc Location, id int64, p Patch, by string) error {
	if err := p.validate(); err != nil {
		return err
	}
	return m.tx(loc, id, func(tx *sql.Tx, now int64) error {
		set := func(col string, v *string) error {
			if v == nil {
				return nil
			}
			val := *v
			if col == "title" || col == "description" {
				val = strings.TrimSpace(val)
			}
			_, err := tx.Exec(`UPDATE tickets SET `+col+` = ? WHERE id = ?`, val, id)
			return err
		}
		for col, v := range map[string]*string{"title": p.Title, "priority": p.Priority, "description": p.Description,
			"plan": p.Plan, "test_summary": p.TestSummary, "base": p.Base} {
			if err := set(col, v); err != nil {
				return err
			}
		}
		if err := setFiles(tx, id, p); err != nil {
			return err
		}
		if p.Plan != nil {
			return planned(tx, id, *p.Plan, by, now)
		}
		return nil
	})
}

// StatusNames are the English names of the statuses (translated in messages).
var StatusNames = map[string]string{
	New: "New", Todo: "To do", InProgress: "In progress", Review: "To test", Done: "Done", Abandoned: "Abandoned",
}

// Move changes the status of a ticket if the actor may do this transition.
func (m *Manager) Move(loc Location, id int64, to, by, comment string) error {
	if !contains(Statuses, to) {
		return i18n.Errorf("unknown status: %s", to)
	}
	return m.tx(loc, id, func(tx *sql.Tx, now int64) error {
		var from string
		if err := tx.QueryRow(`SELECT status FROM tickets WHERE id = ?`, id).Scan(&from); err != nil {
			return err
		}
		if from == to {
			return nil
		}
		if !CanMove(from, to, by) {
			if by == ByModel {
				return i18n.Errorf("the model cannot move the ticket from “%s” to “%s”", i18n.Text(StatusNames[from]), i18n.Text(StatusNames[to]))
			}
			return i18n.Errorf("a ticket cannot go from “%s” to “%s”", i18n.Text(StatusNames[from]), i18n.Text(StatusNames[to]))
		}
		return setStatus(tx, id, from, to, by, comment, now)
	})
}

func setStatus(tx *sql.Tx, id int64, from, to, by, comment string, now int64) error {
	closed := int64(0)
	if to == Done || to == Abandoned {
		closed = now
	}
	if _, err := tx.Exec(`UPDATE tickets SET status = ?, closed = ? WHERE id = ?`, to, closed, id); err != nil {
		return err
	}
	if comment = strings.TrimSpace(comment); comment != "" {
		return event(tx, id, by, "{from} → {to}: {comment}", Params{"from": from, "to": to, "comment": comment}, now)
	}
	return event(tx, id, by, "{from} → {to}", Params{"from": from, "to": to}, now)
}

// planned moves a New ticket to To do once it has a plan.
func planned(tx *sql.Tx, id int64, plan, by string, now int64) error {
	var status string
	if err := tx.QueryRow(`SELECT status FROM tickets WHERE id = ?`, id).Scan(&status); err != nil {
		return err
	}
	if status != New || strings.TrimSpace(plan) == "" {
		return nil
	}
	return setStatus(tx, id, New, Todo, by, "", now)
}

func (m *Manager) Delete(loc Location, id int64) error {
	db, err := m.db(loc)
	if err != nil {
		return err
	}
	res, err := db.Exec(`DELETE FROM tickets WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// AddNote adds a note; chatID is the conversation that wrote it ("" for the user).
func (m *Manager) AddNote(loc Location, id int64, text, by, chatID string) error {
	text = strings.TrimSpace(text)
	if text == "" {
		return i18n.New("empty note")
	}
	if n := utf8.RuneCountInString(text); n > MaxNote {
		return i18n.Errorf("note too long (%d characters, %d max)", n, MaxNote)
	}
	return m.tx(loc, id, func(tx *sql.Tx, now int64) error {
		_, err := tx.Exec(`INSERT INTO notes (ticket_id, kind, author, text, chat_id, created) VALUES (?, 'note', ?, ?, ?, ?)`, id, by, text, chatID, now)
		return err
	})
}

func (m *Manager) DeleteNote(loc Location, id, noteID int64) error {
	return m.tx(loc, id, func(tx *sql.Tx, now int64) error {
		_, err := tx.Exec(`DELETE FROM notes WHERE ticket_id = ? AND id = ? AND kind != 'event'`, id, noteID)
		return err
	})
}

func addGoal(tx *sql.Tx, id int64, g GoalInput, source string, now int64) error {
	_, err := tx.Exec(`INSERT INTO goals (ticket_id, pos, text, description, source, created) VALUES (?, (SELECT COALESCE(MAX(pos), 0) + 1 FROM goals WHERE ticket_id = ?), ?, ?, ?, ?)`,
		id, id, strings.TrimSpace(g.Title), strings.TrimSpace(g.Description), source, now)
	return err
}

// GoalInput is a goal to write: a title and a description. A plain string is a title.
type GoalInput struct {
	Title       string `json:"title"`
	Description string `json:"description"`
}

func (g *GoalInput) UnmarshalJSON(data []byte) error {
	if len(data) > 0 && data[0] == '"' {
		return json.Unmarshal(data, &g.Title)
	}
	type plain GoalInput
	return json.Unmarshal(data, (*plain)(g))
}

// SetPlan writes the plan and replaces the goals that come from a plan (the goals added by
// the user stay). A New ticket moves to To do.
func (m *Manager) SetPlan(loc Location, id int64, plan string, goals []GoalInput, by string) error {
	return m.tx(loc, id, func(tx *sql.Tx, now int64) error {
		if _, err := tx.Exec(`UPDATE tickets SET plan = ? WHERE id = ?`, plan, id); err != nil {
			return err
		}
		if goals != nil {
			if _, err := tx.Exec(`DELETE FROM goals WHERE ticket_id = ? AND source = 'plan'`, id); err != nil {
				return err
			}
			for _, g := range goals {
				if strings.TrimSpace(g.Title) != "" {
					if err := addGoal(tx, id, g, "plan", now); err != nil {
						return err
					}
				}
			}
		}
		if err := event(tx, id, by, "Plan updated", nil, now); err != nil {
			return err
		}
		return planned(tx, id, plan, by, now)
	})
}

// GoalOp changes the goals: add (Text, Description), check (ID, Done), edit (ID, Text,
// Description), delete (ID).
type GoalOp struct {
	Op          string `json:"op"`
	ID          int64  `json:"id"`
	Text        string `json:"text"`
	Description string `json:"description"`
	Done        bool   `json:"done"`
	Source      string `json:"source"`
}

func (m *Manager) Goal(loc Location, id int64, op GoalOp) error {
	return m.tx(loc, id, func(tx *sql.Tx, now int64) error {
		var res sql.Result
		var err error
		switch op.Op {
		case "add":
			if strings.TrimSpace(op.Text) == "" {
				return i18n.New("empty goal")
			}
			src := op.Source
			if src == "" {
				src = "user"
			}
			return addGoal(tx, id, GoalInput{op.Text, op.Description}, src, now)
		case "check":
			res, err = tx.Exec(`UPDATE goals SET done = ? WHERE ticket_id = ? AND id = ?`, op.Done, id, op.ID)
		case "edit":
			if strings.TrimSpace(op.Text) == "" {
				return i18n.New("empty goal")
			}
			res, err = tx.Exec(`UPDATE goals SET text = ?, description = ? WHERE ticket_id = ? AND id = ?`, strings.TrimSpace(op.Text), strings.TrimSpace(op.Description), id, op.ID)
		case "delete":
			res, err = tx.Exec(`DELETE FROM goals WHERE ticket_id = ? AND id = ?`, id, op.ID)
		default:
			return i18n.Errorf("unknown operation: %s", op.Op)
		}
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return i18n.Errorf("goal %d not found", op.ID)
		}
		return nil
	})
}

// FeedbackOp changes the test feedback: add (Kind, Text, ChatID), check (ID, Done), chat
// (ID, ChatID: the conversation handling it), delete (ID).
type FeedbackOp struct {
	Op     string `json:"op"`
	ID     int64  `json:"id"`
	Kind   string `json:"kind"`
	Text   string `json:"text"`
	Done   bool   `json:"done"`
	ChatID string `json:"chatId"`
}

// Feedback changes the test feedback of a ticket; it returns the id of an added one.
func (m *Manager) Feedback(loc Location, id int64, op FeedbackOp, by string) (int64, error) {
	var fid int64
	err := m.tx(loc, id, func(tx *sql.Tx, now int64) error {
		var res sql.Result
		var err error
		switch op.Op {
		case "add":
			text := strings.TrimSpace(op.Text)
			if text == "" {
				return i18n.New("empty feedback")
			}
			if n := utf8.RuneCountInString(text); n > MaxNote {
				return i18n.Errorf("feedback too long (%d characters, %d max)", n, MaxNote)
			}
			if !contains(FeedbackKinds, op.Kind) {
				return i18n.Errorf("unknown feedback kind: %s (%s)", op.Kind, strings.Join(FeedbackKinds, ", "))
			}
			res, err = tx.Exec(`INSERT INTO feedback (ticket_id, kind, text, author, chat_id, created) VALUES (?, ?, ?, ?, ?, ?)`, id, op.Kind, text, by, op.ChatID, now)
			if err == nil {
				fid, _ = res.LastInsertId()
			}
		case "check":
			res, err = tx.Exec(`UPDATE feedback SET done = ? WHERE ticket_id = ? AND id = ?`, op.Done, id, op.ID)
		case "chat":
			res, err = tx.Exec(`UPDATE feedback SET chat_id = ? WHERE ticket_id = ? AND id = ?`, op.ChatID, id, op.ID)
		case "delete":
			res, err = tx.Exec(`DELETE FROM feedback WHERE ticket_id = ? AND id = ?`, id, op.ID)
		default:
			return i18n.Errorf("unknown operation: %s", op.Op)
		}
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return i18n.Errorf("feedback %d not found", op.ID)
		}
		return nil
	})
	return fid, err
}

func (m *Manager) LinkChat(loc Location, id int64, chatID, role, title string) error {
	if !contains(ChatRoles, role) {
		return i18n.Errorf("unknown conversation role: %s", role)
	}
	return m.tx(loc, id, func(tx *sql.Tx, now int64) error {
		_, err := tx.Exec(`INSERT INTO chats (ticket_id, chat_id, role, title, created) VALUES (?, ?, ?, ?, ?)
			ON CONFLICT(ticket_id, chat_id) DO UPDATE SET role = excluded.role, title = CASE WHEN excluded.title != '' THEN excluded.title ELSE chats.title END`,
			id, chatID, role, title, now)
		return err
	})
}

func (m *Manager) UnlinkChat(loc Location, id int64, chatID string) error {
	return m.tx(loc, id, func(tx *sql.Tx, now int64) error {
		_, err := tx.Exec(`DELETE FROM chats WHERE ticket_id = ? AND chat_id = ?`, id, chatID)
		return err
	})
}

// RenameChat follows the title of a conversation in every ticket linking it.
func (m *Manager) RenameChat(loc Location, chatID, title string) {
	if db, err := m.db(loc); err == nil {
		_, _ = db.Exec(`UPDATE chats SET title = ? WHERE chat_id = ?`, title, chatID)
	}
}

func (m *Manager) LinkCommit(loc Location, id int64, hash, subject string) error {
	hash = strings.TrimSpace(hash)
	if hash == "" {
		return i18n.New("empty commit")
	}
	return m.tx(loc, id, func(tx *sql.Tx, now int64) error {
		_, err := tx.Exec(`INSERT INTO commits (ticket_id, hash, subject, created) VALUES (?, ?, ?, ?)
			ON CONFLICT(ticket_id, hash) DO UPDATE SET subject = excluded.subject`, id, hash, subject, now)
		return err
	})
}

func (m *Manager) UnlinkCommit(loc Location, id int64, hash string) error {
	return m.tx(loc, id, func(tx *sql.Tx, now int64) error {
		_, err := tx.Exec(`DELETE FROM commits WHERE ticket_id = ? AND hash = ?`, id, hash)
		return err
	})
}

const MaxAttachment = 20 << 20

func (m *Manager) AddAttachment(loc Location, id int64, name, mime string, data []byte) (int64, error) {
	if len(data) > MaxAttachment {
		return 0, i18n.New("attachment too large (20 MB max)")
	}
	var aid int64
	err := m.tx(loc, id, func(tx *sql.Tx, now int64) error {
		res, err := tx.Exec(`INSERT INTO attachments (ticket_id, name, mime, size, data, created) VALUES (?, ?, ?, ?, ?, ?)`, id, name, mime, len(data), data, now)
		if err == nil {
			aid, _ = res.LastInsertId()
		}
		return err
	})
	return aid, err
}

func (m *Manager) Attachment(loc Location, id, aid int64) (*Attachment, []byte, error) {
	db, err := m.db(loc)
	if err != nil {
		return nil, nil, err
	}
	a := &Attachment{}
	var data []byte
	err = db.QueryRow(`SELECT id, name, mime, size, created, data FROM attachments WHERE ticket_id = ? AND id = ?`, id, aid).Scan(&a.ID, &a.Name, &a.Mime, &a.Size, &a.Created, &data)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil, i18n.New("attachment not found")
	}
	return a, data, err
}

func (m *Manager) DeleteAttachment(loc Location, id, aid int64) error {
	return m.tx(loc, id, func(tx *sql.Tx, now int64) error {
		_, err := tx.Exec(`DELETE FROM attachments WHERE ticket_id = ? AND id = ?`, id, aid)
		return err
	})
}

// GitState records the branch, base and worktree of a ticket (step "in progress").
type GitState struct {
	Branch   *string   `json:"branch"`
	Base     *string   `json:"base"`
	Worktree *string   `json:"worktree"`
	Setup    *string   `json:"setup"`
	SetupLog *string   `json:"setupLog"`
	PR       *string   `json:"pr"`
	Snapshot *Snapshot `json:"snapshot"`
}

func (m *Manager) SetGit(loc Location, id int64, g GitState) error {
	return m.tx(loc, id, func(tx *sql.Tx, now int64) error {
		for col, v := range map[string]*string{"branch": g.Branch, "base": g.Base, "worktree": g.Worktree, "setup": g.Setup, "setup_log": g.SetupLog, "pr": g.PR} {
			if v != nil {
				if _, err := tx.Exec(`UPDATE tickets SET `+col+` = ? WHERE id = ?`, *v, id); err != nil {
					return err
				}
			}
		}
		if g.Snapshot != nil {
			data, _ := json.Marshal(g.Snapshot)
			if _, err := tx.Exec(`UPDATE tickets SET snapshot = ? WHERE id = ?`, string(data), id); err != nil {
				return err
			}
		}
		return nil
	})
}

// Event adds a line to the history of a ticket (see EventText).
func (m *Manager) Event(loc Location, id int64, by, key string, p Params) error {
	return m.tx(loc, id, func(tx *sql.Tx, now int64) error { return event(tx, id, by, key, p, now) })
}

// Meta reads and writes the settings of the kanban of a project (worktree setup command…).
func (m *Manager) Meta(loc Location) (map[string]string, error) {
	db, err := m.db(loc)
	if err != nil {
		return nil, err
	}
	rows, err := db.Query(`SELECT key, value FROM meta`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, err
		}
		out[k] = v
	}
	return out, rows.Err()
}

func (m *Manager) SetMeta(loc Location, values map[string]string) error {
	db, err := m.db(loc)
	if err != nil {
		return err
	}
	for k, v := range values {
		if _, err := db.Exec(`INSERT INTO meta (key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value`, k, v); err != nil {
			return err
		}
	}
	return nil
}
