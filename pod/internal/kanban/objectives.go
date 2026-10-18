package kanban

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/DrSmithFr/web-ide/pod/internal/i18n"
)

// Objectives (docs/design/kanban.md): named deadlines (MVP, a launch…) in order. A milestone
// says what (one feature), an objective says when: it takes tickets of several milestones and
// lone ones. A ticket has one objective at most, apart from its milestone; only the user
// reaches or abandons an objective.

const (
	ObjectiveOpen      = "open"
	ObjectiveReached   = "reached"
	ObjectiveAbandoned = "abandoned"
)

var ObjectiveStateNames = map[string]string{ObjectiveOpen: "Open", ObjectiveReached: "Reached", ObjectiveAbandoned: "Abandoned"}

var ErrObjectiveNotFound = i18n.New("objective not found")

// ObjectiveSummary is an objective as the list shows it.
type ObjectiveSummary struct {
	ID    int64  `json:"id"`
	Title string `json:"title"`
	// Due: the date aimed at, YYYY-MM-DD ("" when none).
	Due     string `json:"due,omitempty"`
	Pos     int    `json:"pos"`
	State   string `json:"state"`
	Created int64  `json:"created"`
	Updated int64  `json:"updated"`
	Closed  int64  `json:"closed,omitempty"`
	// Tickets: all of them, finished (Done or Abandoned), and To test.
	Tickets     int `json:"tickets"`
	TicketsDone int `json:"ticketsDone"`
	ToTest      int `json:"toTest"`
}

type Objective struct {
	ObjectiveSummary
	Description string    `json:"description"`
	TicketList  []Summary `json:"ticketList"`
	// MilestoneList: the milestones whose default objective it is, or holding one of its
	// tickets.
	MilestoneList []MilestoneSummary `json:"milestoneList"`
	Notes         []Note             `json:"notes"`
}

// ObjectivePatch holds the fields to change (nil: unchanged).
type ObjectivePatch struct {
	Title       *string `json:"title"`
	Description *string `json:"description"`
	Due         *string `json:"due"`
}

func (p *ObjectivePatch) validate() error {
	if p.Title != nil && strings.TrimSpace(*p.Title) == "" {
		return i18n.New("empty title")
	}
	if p.Description != nil {
		if n := utf8.RuneCountInString(strings.TrimSpace(*p.Description)); n > MaxDescription {
			return i18n.Errorf("description too long (%d characters, %d max)", n, MaxDescription)
		}
	}
	if p.Due != nil && strings.TrimSpace(*p.Due) != "" {
		if _, err := time.Parse("2006-01-02", strings.TrimSpace(*p.Due)); err != nil {
			return i18n.Errorf("invalid date %q: use YYYY-MM-DD", *p.Due)
		}
	}
	return nil
}

const objectiveCols = `o.id, o.title, o.due, o.pos, o.state, o.created, o.updated, o.closed,
  (SELECT COUNT(*) FROM tickets t WHERE t.objective_id = o.id),
  (SELECT COUNT(*) FROM tickets t WHERE t.objective_id = o.id AND t.status IN ('done', 'abandoned')),
  (SELECT COUNT(*) FROM tickets t WHERE t.objective_id = o.id AND t.status = 'to_test')`

func scanObjective(row interface{ Scan(...any) error }, s *ObjectiveSummary, more ...any) error {
	return row.Scan(append([]any{&s.ID, &s.Title, &s.Due, &s.Pos, &s.State, &s.Created, &s.Updated, &s.Closed, &s.Tickets, &s.TicketsDone, &s.ToTest}, more...)...)
}

// Objectives lists the objectives in their order, the open ones first.
func (m *Manager) Objectives(loc Location) ([]ObjectiveSummary, error) {
	db, err := m.db(loc)
	if err != nil {
		return nil, err
	}
	list := []ObjectiveSummary{}
	err = eachRow(db, `SELECT `+objectiveCols+` FROM objectives o ORDER BY o.state != 'open', o.pos, o.id`, func(r *sql.Rows) error {
		var s ObjectiveSummary
		err := scanObjective(r, &s)
		list = append(list, s)
		return err
	})
	return list, err
}

func (m *Manager) GetObjective(loc Location, id int64) (*Objective, error) {
	db, err := m.db(loc)
	if err != nil {
		return nil, err
	}
	o := &Objective{MilestoneList: []MilestoneSummary{}, Notes: []Note{}}
	err = scanObjective(db.QueryRow(`SELECT `+objectiveCols+`, o.description FROM objectives o WHERE o.id = ?`, id), &o.ObjectiveSummary, &o.Description)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrObjectiveNotFound
	}
	if err != nil {
		return nil, err
	}
	if o.TicketList, err = summaries(db, `WHERE t.objective_id = ? ORDER BY t.id`, id); err != nil {
		return nil, err
	}
	if err := eachRow(db, `SELECT `+milestoneCols+` FROM milestones m WHERE m.objective_id = ? OR m.id IN (SELECT milestone_id FROM tickets WHERE objective_id = ?) ORDER BY m.state != 'open', m.id`, func(r *sql.Rows) error {
		var s MilestoneSummary
		err := scanMilestone(r, &s)
		o.MilestoneList = append(o.MilestoneList, s)
		return err
	}, id, id); err != nil {
		return nil, err
	}
	rows, err := db.Query(`SELECT id, kind, author, text, chat_id, created FROM objective_notes WHERE objective_id = ? ORDER BY created, id`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var n Note
		if err := rows.Scan(&n.ID, &n.Kind, &n.Author, &n.Text, &n.ChatID, &n.Created); err != nil {
			return nil, err
		}
		o.Notes = append(o.Notes, n)
	}
	return o, rows.Err()
}

// otx runs f in a transaction and marks the objective updated.
func (m *Manager) otx(loc Location, id int64, f func(tx *sql.Tx, now int64) error) error {
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
		res, err := tx.Exec(`UPDATE objectives SET updated = ? WHERE id = ?`, now, id)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return ErrObjectiveNotFound
		}
	}
	if err := f(tx, now); err != nil {
		return err
	}
	return tx.Commit()
}

func objectiveEvent(tx *sql.Tx, id int64, by, key string, p Params, now int64) error {
	_, err := tx.Exec(`INSERT INTO objective_notes (objective_id, kind, author, text, created) VALUES (?, 'event', ?, ?, ?)`, id, by, EventText(key, p), now)
	return err
}

// CreateObjective adds an open objective at the end of the list and returns its number.
func (m *Manager) CreateObjective(loc Location, p ObjectivePatch, by string) (int64, error) {
	if p.Title == nil {
		return 0, i18n.New("title is missing")
	}
	if err := p.validate(); err != nil {
		return 0, err
	}
	var id int64
	err := m.otx(loc, 0, func(tx *sql.Tx, now int64) error {
		res, err := tx.Exec(`INSERT INTO objectives (title, description, due, pos, created, updated) VALUES (?, ?, ?, (SELECT COALESCE(MAX(pos), 0) + 1 FROM objectives), ?, ?)`,
			strings.TrimSpace(*p.Title), trimmed(p.Description), trimmed(p.Due), now, now)
		if err != nil {
			return err
		}
		id, _ = res.LastInsertId()
		return objectiveEvent(tx, id, by, "Objective created", nil, now)
	})
	return id, err
}

func (m *Manager) UpdateObjective(loc Location, id int64, p ObjectivePatch) error {
	if err := p.validate(); err != nil {
		return err
	}
	return m.otx(loc, id, func(tx *sql.Tx, now int64) error {
		for col, v := range map[string]*string{"title": p.Title, "description": p.Description, "due": p.Due} {
			if v != nil {
				if _, err := tx.Exec(`UPDATE objectives SET `+col+` = ? WHERE id = ?`, strings.TrimSpace(*v), id); err != nil {
					return err
				}
			}
		}
		return nil
	})
}

// DeleteObjective removes an objective; its tickets and milestones stay, without objective.
func (m *Manager) DeleteObjective(loc Location, id int64) error {
	return m.otx(loc, id, func(tx *sql.Tx, now int64) error {
		for _, q := range []string{`UPDATE tickets SET objective_id = 0 WHERE objective_id = ?`, `UPDATE milestones SET objective_id = 0 WHERE objective_id = ?`, `DELETE FROM objectives WHERE id = ?`} {
			if _, err := tx.Exec(q, id); err != nil {
				return err
			}
		}
		return nil
	})
}

// MoveObjective swaps an objective with its neighbour in the order (delta < 0: the one before).
func (m *Manager) MoveObjective(loc Location, id int64, delta int) error {
	return m.otx(loc, id, func(tx *sql.Tx, now int64) error {
		var ids []int64
		if err := eachRow(tx, `SELECT id FROM objectives ORDER BY state != 'open', pos, id`, func(r *sql.Rows) error {
			var o int64
			err := r.Scan(&o)
			ids = append(ids, o)
			return err
		}); err != nil {
			return err
		}
		at := -1
		for i, o := range ids {
			if o == id {
				at = i
			}
		}
		to := at + 1
		if delta < 0 {
			to = at - 1
		}
		if at < 0 || to < 0 || to >= len(ids) {
			return nil
		}
		ids[at], ids[to] = ids[to], ids[at]
		for i, o := range ids {
			if _, err := tx.Exec(`UPDATE objectives SET pos = ? WHERE id = ?`, i+1, o); err != nil {
				return err
			}
		}
		return nil
	})
}

// ReachObjective closes an objective as reached: the user only, once every ticket of it is
// finished.
func (m *Manager) ReachObjective(loc Location, id int64, by string) error {
	if by != ByUser {
		return i18n.New("only the user reaches an objective")
	}
	return m.otx(loc, id, func(tx *sql.Tx, now int64) error {
		var s ObjectiveSummary
		if err := scanObjective(tx.QueryRow(`SELECT `+objectiveCols+` FROM objectives o WHERE o.id = ?`, id), &s); err != nil {
			return err
		}
		if s.State != ObjectiveOpen {
			return i18n.New("the objective is closed")
		}
		if n := s.Tickets - s.TicketsDone; n > 0 {
			return i18n.Errorf("%d ticket(s) of this objective are not finished", n)
		}
		return setObjectiveState(tx, id, s.State, ObjectiveReached, by, now)
	})
}

// SetObjectiveState reopens a closed objective or abandons an open one: the user only.
func (m *Manager) SetObjectiveState(loc Location, id int64, to, by string) error {
	if by != ByUser {
		return i18n.New("only the user closes or reopens an objective")
	}
	return m.otx(loc, id, func(tx *sql.Tx, now int64) error {
		var from string
		if err := tx.QueryRow(`SELECT state FROM objectives WHERE id = ?`, id).Scan(&from); err != nil {
			return err
		}
		if from == to {
			return nil
		}
		switch {
		case to == ObjectiveReached:
			return i18n.New("an objective is reached once its tickets are finished")
		case to != ObjectiveOpen && to != ObjectiveAbandoned:
			return i18n.Errorf("unknown objective state: %s", to)
		case to == ObjectiveAbandoned && from != ObjectiveOpen:
			return i18n.New("only an open objective can be abandoned")
		}
		return setObjectiveState(tx, id, from, to, by, now)
	})
}

func setObjectiveState(tx *sql.Tx, id int64, from, to, by string, now int64) error {
	closed := int64(0)
	if to != ObjectiveOpen {
		closed = now
	}
	if _, err := tx.Exec(`UPDATE objectives SET state = ?, closed = ? WHERE id = ?`, to, closed, id); err != nil {
		return err
	}
	return objectiveEvent(tx, id, by, "{from} → {to}", Params{"from": from, "to": to}, now)
}

// AddObjectiveNote adds a note to an objective; chatID is the conversation that wrote it.
func (m *Manager) AddObjectiveNote(loc Location, id int64, text, by, chatID string) error {
	text = strings.TrimSpace(text)
	if text == "" {
		return i18n.New("empty note")
	}
	if n := utf8.RuneCountInString(text); n > MaxNote {
		return i18n.Errorf("note too long (%d characters, %d max)", n, MaxNote)
	}
	return m.otx(loc, id, func(tx *sql.Tx, now int64) error {
		_, err := tx.Exec(`INSERT INTO objective_notes (objective_id, kind, author, text, chat_id, created) VALUES (?, 'note', ?, ?, ?, ?)`, id, by, text, chatID, now)
		return err
	})
}

func (m *Manager) DeleteObjectiveNote(loc Location, id, noteID int64) error {
	return m.otx(loc, id, func(tx *sql.Tx, now int64) error {
		_, err := tx.Exec(`DELETE FROM objective_notes WHERE objective_id = ? AND id = ? AND kind != 'event'`, id, noteID)
		return err
	})
}

// openObjective reads the title of an objective a ticket or a milestone is put in: it must
// exist and be open.
func openObjective(tx *sql.Tx, id int64) (string, error) {
	var title, state string
	if err := tx.QueryRow(`SELECT title, state FROM objectives WHERE id = ?`, id).Scan(&title, &state); err != nil {
		return "", i18n.Errorf("objective %d not found", id)
	}
	if state != ObjectiveOpen {
		return "", i18n.Errorf("objective %d is closed", id)
	}
	return title, nil
}

func objectiveTitle(tx *sql.Tx, id int64) (string, error) {
	var title string
	err := tx.QueryRow(`SELECT title FROM objectives WHERE id = ?`, id).Scan(&title)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return title, err
}

// setObjective puts a ticket and its steps in an objective (0: out of it). A step takes the
// objective of its root.
func setObjective(tx *sql.Tx, id int64, o *int64, by string, now int64) error {
	if o == nil {
		return nil
	}
	var parent, cur int64
	if err := tx.QueryRow(`SELECT parent_id, objective_id FROM tickets WHERE id = ?`, id).Scan(&parent, &cur); err != nil {
		return err
	}
	if cur == *o {
		return nil
	}
	if parent != 0 {
		return i18n.Errorf("a step takes the objective of its root (#%d)", parent)
	}
	var title string
	var err error
	if *o != 0 {
		title, err = openObjective(tx, *o)
	} else {
		title, err = objectiveTitle(tx, cur)
	}
	if err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE tickets SET objective_id = ? WHERE id = ? OR parent_id = ?`, *o, id, id); err != nil {
		return err
	}
	if *o == 0 {
		return event(tx, id, by, "Removed from the objective “{objective}”", Params{"objective": title}, now)
	}
	return event(tx, id, by, "Objective: “{objective}”", Params{"objective": title}, now)
}

// setMilestoneObjective sets the default objective of a milestone (0: none). Its open
// tickets that had the former default, or none, follow; those put elsewhere by hand stay.
func setMilestoneObjective(tx *sql.Tx, ms int64, o *int64, by string, now int64) error {
	if o == nil {
		return nil
	}
	var cur int64
	if err := tx.QueryRow(`SELECT objective_id FROM milestones WHERE id = ?`, ms).Scan(&cur); err != nil {
		return err
	}
	if cur == *o {
		return nil
	}
	title := ""
	if *o != 0 {
		var err error
		if title, err = openObjective(tx, *o); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(`UPDATE milestones SET objective_id = ? WHERE id = ?`, *o, ms); err != nil {
		return err
	}
	var roots []int64
	if err := eachRow(tx, `SELECT id FROM tickets WHERE milestone_id = ? AND parent_id = 0 AND status NOT IN ('done', 'abandoned') AND objective_id IN (0, ?)`, func(r *sql.Rows) error {
		var id int64
		err := r.Scan(&id)
		roots = append(roots, id)
		return err
	}, ms, cur); err != nil {
		return err
	}
	for _, id := range roots {
		if err := setObjective(tx, id, o, by, now); err != nil {
			return err
		}
	}
	if *o == 0 {
		return milestoneEvent(tx, ms, by, "No default objective", nil, now)
	}
	return milestoneEvent(tx, ms, by, "Default objective: “{objective}”", Params{"objective": title}, now)
}
