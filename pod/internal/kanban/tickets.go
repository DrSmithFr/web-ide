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
	Author  string `json:"author"` // user | model | claude
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
	// Time of the answers (filled by the server): with the sub-agents, of the conversation
	// alone, and spent thinking.
	GenerationMs int64 `json:"generationMs,omitempty"`
	OwnMs        int64 `json:"ownMs,omitempty"`
	ThinkMs      int64 `json:"thinkMs,omitempty"`
}

// GoalTitle is a goal as a card of the board lists it.
type GoalTitle struct {
	Text string `json:"text"`
	Done bool   `json:"done"`
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
	Created      int64 `json:"created"`
	Updated      int64 `json:"updated"`
	Closed       int64 `json:"closed,omitempty"`
	// Lineage: the parent (0: none), the place among its children, and for a parent its
	// own step validated (its children may start).
	// Size: estimated effort, s | m | l | xl ("" when not estimated yet).
	Size      string  `json:"size,omitempty"`
	Parent    int64   `json:"parent,omitempty"`
	Pos       int     `json:"pos,omitempty"`
	StepDone  bool    `json:"stepDone,omitempty"`
	DependsOn []int64 `json:"dependsOn"`
	// Blockers keep a ticket from starting (filled by the server for the tickets that may
	// start: it needs git).
	Blockers []Blocker `json:"blockers,omitempty"`
	// The board lists the goals; ChatIDs are its linked conversations, whose answers make
	// GenerationMs (filled by the server).
	GoalTitles   []GoalTitle `json:"goalTitles,omitempty"`
	ChatIDs      []string    `json:"-"`
	GenerationMs int64       `json:"generationMs,omitempty"`
}

type Ticket struct {
	Summary
	Description  string       `json:"description"`
	Plan         string       `json:"plan"`
	TestSummary  string       `json:"testSummary"`
	PR           string       `json:"pr,omitempty"`
	Base         string       `json:"base"`
	Setup        string       `json:"setup"`
	SetupLog     string       `json:"setupLog,omitempty"`
	Snapshot     *Snapshot    `json:"snapshot,omitempty"`
	GoalList     []Goal       `json:"goalList"`
	Notes        []Note       `json:"notes"`
	FeedbackList []Feedback   `json:"feedbackList"`
	Files        []string     `json:"files"`
	ChatList     []ChatLink   `json:"chatList"`
	Attachments  []Attachment `json:"attachments"`
	// Children of the lineage, in order.
	Children []Summary `json:"children"`
}

var ErrNotFound = i18n.New("ticket not found")

const summaryCols = `t.id, t.title, t.priority, t.status, t.branch, t.worktree, t.created, t.updated, t.closed,
  (SELECT COUNT(*) FROM goals g WHERE g.ticket_id = t.id AND g.done = 1),
  (SELECT COUNT(*) FROM goals g WHERE g.ticket_id = t.id),
  (SELECT COUNT(*) FROM chats c WHERE c.ticket_id = t.id),
  (SELECT COUNT(*) FROM feedback f WHERE f.ticket_id = t.id AND f.done = 0),
  t.parent_id, t.pos, t.step_done, t.size`

func scanSummary(row interface{ Scan(...any) error }, s *Summary) error {
	s.DependsOn = []int64{}
	return row.Scan(&s.ID, &s.Title, &s.Priority, &s.Status, &s.Branch, &s.Worktree, &s.Created, &s.Updated, &s.Closed, &s.GoalsDone, &s.Goals, &s.Chats, &s.FeedbackOpen,
		&s.Parent, &s.Pos, &s.StepDone, &s.Size)
}

// summaries reads tickets with their dependencies (where: an SQL condition on t).
func summaries(q interface {
	Query(string, ...any) (*sql.Rows, error)
}, where string, args ...any) ([]Summary, error) {
	rows, err := q.Query(`SELECT `+summaryCols+` FROM tickets t `+where, args...)
	if err != nil {
		return nil, err
	}
	list := []Summary{}
	for rows.Next() {
		var s Summary
		if err := scanSummary(rows, &s); err != nil {
			rows.Close()
			return nil, err
		}
		list = append(list, s)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	deps, err := q.Query(`SELECT ticket_id, dep_id FROM deps ORDER BY dep_id`)
	if err != nil {
		return nil, err
	}
	defer deps.Close()
	byID := map[int64]*Summary{}
	for i := range list {
		byID[list[i].ID] = &list[i]
	}
	for deps.Next() {
		var id, dep int64
		if err := deps.Scan(&id, &dep); err != nil {
			return nil, err
		}
		if s := byID[id]; s != nil {
			s.DependsOn = append(s.DependsOn, dep)
		}
	}
	return list, deps.Err()
}

func (m *Manager) List(loc Location) ([]Summary, error) {
	db, err := m.db(loc)
	if err != nil {
		return nil, err
	}
	list, err := summaries(db, `ORDER BY t.updated DESC`)
	if err != nil {
		return nil, err
	}
	byID := map[int64]*Summary{}
	for i := range list {
		byID[list[i].ID] = &list[i]
	}
	if err := eachRow(db, `SELECT ticket_id, text, done FROM goals ORDER BY ticket_id, pos, id`, func(r *sql.Rows) error {
		var id int64
		var g GoalTitle
		if err := r.Scan(&id, &g.Text, &g.Done); err != nil {
			return err
		}
		if s := byID[id]; s != nil {
			s.GoalTitles = append(s.GoalTitles, g)
		}
		return nil
	}); err != nil {
		return nil, err
	}
	err = eachRow(db, `SELECT ticket_id, chat_id FROM chats`, func(r *sql.Rows) error {
		var id int64
		var chat string
		if err := r.Scan(&id, &chat); err != nil {
			return err
		}
		if s := byID[id]; s != nil {
			s.ChatIDs = append(s.ChatIDs, chat)
		}
		return nil
	})
	return list, err
}

// eachRow runs f on each row of a query.
func eachRow(db *sql.DB, query string, f func(*sql.Rows) error) error {
	rows, err := db.Query(query)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		if err := f(rows); err != nil {
			return err
		}
	}
	return rows.Err()
}

func (m *Manager) Get(loc Location, id int64) (*Ticket, error) {
	db, err := m.db(loc)
	if err != nil {
		return nil, err
	}
	return get(db, id)
}

func get(db *sql.DB, id int64) (*Ticket, error) {
	t := &Ticket{GoalList: []Goal{}, Notes: []Note{}, FeedbackList: []Feedback{}, Files: []string{}, ChatList: []ChatLink{}, Attachments: []Attachment{}}
	var snap string
	row := db.QueryRow(`SELECT `+summaryCols+`, t.description, t.plan, t.test_summary, t.pr, t.base, t.setup, t.setup_log, t.snapshot FROM tickets t WHERE t.id = ?`, id)
	err := row.Scan(&t.ID, &t.Title, &t.Priority, &t.Status, &t.Branch, &t.Worktree, &t.Created, &t.Updated, &t.Closed, &t.GoalsDone, &t.Goals, &t.Chats, &t.FeedbackOpen,
		&t.Parent, &t.Pos, &t.StepDone, &t.Size,
		&t.Description, &t.Plan, &t.TestSummary, &t.PR, &t.Base, &t.Setup, &t.SetupLog, &snap)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if t.Children, err = summaries(db, `WHERE t.parent_id = ? ORDER BY t.pos, t.id`, id); err != nil {
		return nil, err
	}
	t.DependsOn = []int64{}
	if deps, err := db.Query(`SELECT dep_id FROM deps WHERE ticket_id = ? ORDER BY dep_id`, id); err == nil {
		for deps.Next() {
			var d int64
			if deps.Scan(&d) == nil {
				t.DependsOn = append(t.DependsOn, d)
			}
		}
		deps.Close()
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
	Size        *string   `json:"size"`
	Description *string   `json:"description"`
	Plan        *string   `json:"plan"`
	TestSummary *string   `json:"testSummary"`
	Base        *string   `json:"base"`
	Files       *[]string `json:"files"`
	AddFiles    []string  `json:"addFiles"`
	RemoveFiles []string  `json:"removeFiles"`
	// Parent: 0 takes the ticket out of its lineage. DependsOn replaces the dependencies.
	Parent    *int64   `json:"parent"`
	DependsOn *[]int64 `json:"dependsOn"`
	// PlanSize: the size comes with a plan, kept as is once the estimate is fixed.
	PlanSize bool `json:"-"`
}

// Estimated tells whether the priority and the size of a ticket in this status may still
// change: they are fixed once its development started.
func Estimated(status string) bool { return status == "new" || status == "todo" }

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
	if p.Size != nil && *p.Size != "" && !contains(Sizes, *p.Size) {
		return i18n.Errorf("unknown size: %s (%s)", *p.Size, strings.Join(Sizes, ", "))
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
		if err := event(tx, id, by, "Ticket created", nil, now); err != nil {
			return err
		}
		return setLinks(tx, id, p, by, now)
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
		var status, priority, size string
		if err := tx.QueryRow(`SELECT status, priority, size FROM tickets WHERE id = ?`, id).Scan(&status, &priority, &size); err != nil {
			return err
		}
		if !Estimated(status) {
			if p.Size != nil && *p.Size != size && p.PlanSize {
				p.Size = nil
			}
			if (p.Priority != nil && *p.Priority != priority) || (p.Size != nil && *p.Size != size) {
				return i18n.New("the priority and the size are fixed once the development started")
			}
		}
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
		if err := setLinks(tx, id, p, by, now); err != nil {
			return err
		}
		if err := setSize(tx, id, p.Size, by, now); err != nil {
			return err
		}
		if p.Plan != nil {
			return planned(tx, id, *p.Plan, by, now)
		}
		return nil
	})
}

// SizeNames are the names of the sizes, as shown and read by the models.
var SizeNames = map[string]string{"s": "S", "m": "M", "l": "L", "xl": "XL"}

// setSize changes the estimated size of a ticket, with a line in its history.
func setSize(tx *sql.Tx, id int64, size *string, by string, now int64) error {
	if size == nil {
		return nil
	}
	var cur string
	if err := tx.QueryRow(`SELECT size FROM tickets WHERE id = ?`, id).Scan(&cur); err != nil || cur == *size {
		return err
	}
	if _, err := tx.Exec(`UPDATE tickets SET size = ? WHERE id = ?`, *size, id); err != nil {
		return err
	}
	return event(tx, id, by, "Size: {size}", Params{"size": SizeNames[*size]}, now)
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
			if IsAgent(by) {
				return i18n.Errorf("the model cannot move the ticket from “%s” to “%s”", i18n.Text(StatusNames[from]), i18n.Text(StatusNames[to]))
			}
			return i18n.Errorf("a ticket cannot go from “%s” to “%s”", i18n.Text(StatusNames[from]), i18n.Text(StatusNames[to]))
		}
		if err := lineageMove(tx, id, from, to); err != nil {
			return err
		}
		if to == Done {
			if err := feedbackHandled(tx, id); err != nil {
				return err
			}
		}
		return setStatus(tx, id, from, to, by, comment, now)
	})
}

// lineageMove keeps a lineage in order: a parent is closed after its children, and goes
// back to In progress only while none of them started (its validated step is then undone).
func lineageMove(tx *sql.Tx, id int64, from, to string) error {
	var open, started int
	if err := tx.QueryRow(`SELECT COUNT(*) FILTER (WHERE status NOT IN ('done', 'abandoned')), COUNT(*) FILTER (WHERE status NOT IN ('new', 'todo')) FROM tickets WHERE parent_id = ?`, id).Scan(&open, &started); err != nil {
		return err
	}
	if to == Done && open > 0 {
		return i18n.Errorf("%d child ticket(s) of this lineage are not finished", open)
	}
	if from == Review && to == InProgress {
		if started > 0 {
			return i18n.New("its children have started: the lineage goes on in them")
		}
		_, err := tx.Exec(`UPDATE tickets SET step_done = 0 WHERE id = ?`, id)
		return err
	}
	return nil
}

// feedbackHandled refuses to validate a ticket (or its step) while a test feedback is open.
func feedbackHandled(tx *sql.Tx, id int64) error {
	var open int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM feedback WHERE ticket_id = ? AND done = 0`, id).Scan(&open); err != nil {
		return err
	}
	if open > 0 {
		return i18n.Errorf("%d test feedback(s) not handled yet", open)
	}
	return nil
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
	var children int
	if err := db.QueryRow(`SELECT COUNT(*) FROM tickets WHERE parent_id = ?`, id).Scan(&children); err != nil {
		return err
	}
	if children > 0 {
		return i18n.New("this ticket has children: take them out of its lineage first")
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

// GoalEdit is the edit of a goal by a model: an empty title or description keeps the current one.
func (m *Manager) GoalEdit(loc Location, id, gid int64, title, description string) error {
	t, err := m.Get(loc, id)
	if err != nil {
		return err
	}
	for _, g := range t.GoalList {
		if g.ID == gid {
			if strings.TrimSpace(title) == "" {
				title = g.Text
			}
			if strings.TrimSpace(description) == "" {
				description = g.Description
			}
			return m.Goal(loc, id, GoalOp{Op: "edit", ID: gid, Text: title, Description: description})
		}
	}
	return i18n.Errorf("goal %d not found", gid)
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
