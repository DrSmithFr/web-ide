package kanban

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/DrSmithFr/web-ide/pod/internal/i18n"
)

// Milestones (docs/design/kanban.md): the tickets of one feature, across lineages, with its
// spec, its user stories and its feedback. Its tickets may be chained without the user, but
// only the user validates it.

const (
	MilestoneOpen      = "open"
	MilestoneValidated = "validated"
	MilestoneAbandoned = "abandoned"
	// MaxStory: the longest user story, in characters.
	MaxStory = 500
)

var MilestoneStateNames = map[string]string{MilestoneOpen: "Open", MilestoneValidated: "Validated", MilestoneAbandoned: "Abandoned"}

var ErrMilestoneNotFound = i18n.New("milestone not found")

// Story is a user story of a milestone: what a user can do once it is done. Only the user
// checks it; the stories are the base of the review of the e2e coverage.
type Story struct {
	ID   int64  `json:"id"`
	Text string `json:"text"`
	Done bool   `json:"done"`
}

// MilestoneFeedback is a remark on a milestone; Ticket is the ticket made from it, which
// marks it handled once Done.
type MilestoneFeedback struct {
	Feedback
	Ticket int64 `json:"ticket,omitempty"`
}

// MilestoneSummary is a milestone as the list shows it.
type MilestoneSummary struct {
	ID      int64  `json:"id"`
	Title   string `json:"title"`
	State   string `json:"state"`
	Created int64  `json:"created"`
	Updated int64  `json:"updated"`
	Closed  int64  `json:"closed,omitempty"`
	// Objective: the default objective of its tickets (0: none).
	Objective int64 `json:"objective,omitempty"`
	// Tickets: all of them, finished (Done or Abandoned), and To test.
	Tickets     int `json:"tickets"`
	TicketsDone int `json:"ticketsDone"`
	ToTest      int `json:"toTest"`
	Stories     int `json:"stories"`
	StoriesDone int `json:"storiesDone"`
	// Feedback not handled yet.
	FeedbackOpen int `json:"feedbackOpen"`
}

type Milestone struct {
	MilestoneSummary
	Description string `json:"description"`
	// Spec: a path of the repository or an address.
	Spec         string              `json:"spec"`
	StoryList    []Story             `json:"storyList"`
	FeedbackList []MilestoneFeedback `json:"feedbackList"`
	ChatList     []ChatLink          `json:"chatList"`
	Notes        []Note              `json:"notes"`
	TicketList   []Summary           `json:"ticketList"`
}

// MilestonePatch holds the fields to change (nil: unchanged).
type MilestonePatch struct {
	Title       *string `json:"title"`
	Description *string `json:"description"`
	Spec        *string `json:"spec"`
	// Objective: the default objective (0: none); its open tickets go into it.
	Objective *int64 `json:"objective"`
}

func (p *MilestonePatch) validate() error {
	if p.Title != nil && strings.TrimSpace(*p.Title) == "" {
		return i18n.New("empty title")
	}
	if p.Description != nil {
		if n := utf8.RuneCountInString(strings.TrimSpace(*p.Description)); n > MaxDescription {
			return i18n.Errorf("description too long (%d characters, %d max)", n, MaxDescription)
		}
	}
	return nil
}

const milestoneCols = `m.id, m.title, m.state, m.created, m.updated, m.closed, m.objective_id,
  (SELECT COUNT(*) FROM tickets t WHERE t.milestone_id = m.id),
  (SELECT COUNT(*) FROM tickets t WHERE t.milestone_id = m.id AND t.status IN ('done', 'abandoned')),
  (SELECT COUNT(*) FROM tickets t WHERE t.milestone_id = m.id AND t.status = 'to_test'),
  (SELECT COUNT(*) FROM stories s WHERE s.milestone_id = m.id),
  (SELECT COUNT(*) FROM stories s WHERE s.milestone_id = m.id AND s.done = 1),
  (SELECT COUNT(*) FROM milestone_feedback f WHERE f.milestone_id = m.id AND f.done = 0)`

func scanMilestone(row interface{ Scan(...any) error }, s *MilestoneSummary, more ...any) error {
	return row.Scan(append([]any{&s.ID, &s.Title, &s.State, &s.Created, &s.Updated, &s.Closed, &s.Objective, &s.Tickets, &s.TicketsDone, &s.ToTest, &s.Stories, &s.StoriesDone, &s.FeedbackOpen}, more...)...)
}

// Milestones lists the milestones, the open ones first.
func (m *Manager) Milestones(loc Location) ([]MilestoneSummary, error) {
	db, err := m.db(loc)
	if err != nil {
		return nil, err
	}
	list := []MilestoneSummary{}
	err = eachRow(db, `SELECT `+milestoneCols+` FROM milestones m ORDER BY m.state != 'open', m.id`, func(r *sql.Rows) error {
		var s MilestoneSummary
		err := scanMilestone(r, &s)
		list = append(list, s)
		return err
	})
	return list, err
}

func (m *Manager) GetMilestone(loc Location, id int64) (*Milestone, error) {
	db, err := m.db(loc)
	if err != nil {
		return nil, err
	}
	ms := &Milestone{StoryList: []Story{}, FeedbackList: []MilestoneFeedback{}, ChatList: []ChatLink{}, Notes: []Note{}}
	err = scanMilestone(db.QueryRow(`SELECT `+milestoneCols+`, m.description, m.spec FROM milestones m WHERE m.id = ?`, id), &ms.MilestoneSummary, &ms.Description, &ms.Spec)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrMilestoneNotFound
	}
	if err != nil {
		return nil, err
	}
	if ms.TicketList, err = summaries(db, `WHERE t.milestone_id = ? ORDER BY t.id`, id); err != nil {
		return nil, err
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
	if err := each(`SELECT id, text, done FROM stories WHERE milestone_id = ? ORDER BY pos, id`, func(r *sql.Rows) error {
		var s Story
		err := r.Scan(&s.ID, &s.Text, &s.Done)
		ms.StoryList = append(ms.StoryList, s)
		return err
	}); err != nil {
		return nil, err
	}
	if err := each(`SELECT id, kind, text, done, author, chat_id, created, complexity, ticket_id FROM milestone_feedback WHERE milestone_id = ? ORDER BY created, id`, func(r *sql.Rows) error {
		var f MilestoneFeedback
		err := r.Scan(&f.ID, &f.Kind, &f.Text, &f.Done, &f.Author, &f.ChatID, &f.Created, &f.Complexity, &f.Ticket)
		ms.FeedbackList = append(ms.FeedbackList, f)
		return err
	}); err != nil {
		return nil, err
	}
	if err := each(`SELECT chat_id, role, title, created FROM milestone_chats WHERE milestone_id = ? ORDER BY created`, func(r *sql.Rows) error {
		var c ChatLink
		err := r.Scan(&c.ChatID, &c.Role, &c.Title, &c.Created)
		ms.ChatList = append(ms.ChatList, c)
		return err
	}); err != nil {
		return nil, err
	}
	if err := each(`SELECT id, kind, author, text, chat_id, created FROM milestone_notes WHERE milestone_id = ? ORDER BY created, id`, func(r *sql.Rows) error {
		var n Note
		err := r.Scan(&n.ID, &n.Kind, &n.Author, &n.Text, &n.ChatID, &n.Created)
		ms.Notes = append(ms.Notes, n)
		return err
	}); err != nil {
		return nil, err
	}
	return ms, nil
}

// mtx runs f in a transaction and marks the milestone updated.
func (m *Manager) mtx(loc Location, id int64, f func(tx *sql.Tx, now int64) error) error {
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
		res, err := tx.Exec(`UPDATE milestones SET updated = ? WHERE id = ?`, now, id)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return ErrMilestoneNotFound
		}
	}
	if err := f(tx, now); err != nil {
		return err
	}
	return tx.Commit()
}

func milestoneEvent(tx *sql.Tx, id int64, by, key string, p Params, now int64) error {
	_, err := tx.Exec(`INSERT INTO milestone_notes (milestone_id, kind, author, text, created) VALUES (?, 'event', ?, ?, ?)`, id, by, EventText(key, p), now)
	return err
}

// CreateMilestone adds an open milestone and returns its number.
func (m *Manager) CreateMilestone(loc Location, p MilestonePatch, by string) (int64, error) {
	if p.Title == nil {
		return 0, i18n.New("title is missing")
	}
	if err := p.validate(); err != nil {
		return 0, err
	}
	var id int64
	err := m.mtx(loc, 0, func(tx *sql.Tx, now int64) error {
		res, err := tx.Exec(`INSERT INTO milestones (title, description, spec, created, updated) VALUES (?, ?, ?, ?, ?)`,
			strings.TrimSpace(*p.Title), trimmed(p.Description), trimmed(p.Spec), now, now)
		if err != nil {
			return err
		}
		id, _ = res.LastInsertId()
		return milestoneEvent(tx, id, by, "Milestone created", nil, now)
	})
	return id, err
}

func trimmed(s *string) string {
	if s == nil {
		return ""
	}
	return strings.TrimSpace(*s)
}

func (m *Manager) UpdateMilestone(loc Location, id int64, p MilestonePatch, by string) error {
	if err := p.validate(); err != nil {
		return err
	}
	return m.mtx(loc, id, func(tx *sql.Tx, now int64) error {
		if err := setMilestoneObjective(tx, id, p.Objective, by, now); err != nil {
			return err
		}
		for col, v := range map[string]*string{"title": p.Title, "description": p.Description, "spec": p.Spec} {
			if v != nil {
				if _, err := tx.Exec(`UPDATE milestones SET `+col+` = ? WHERE id = ?`, strings.TrimSpace(*v), id); err != nil {
					return err
				}
			}
		}
		return nil
	})
}

// DeleteMilestone removes a milestone; its tickets stay, without milestone.
func (m *Manager) DeleteMilestone(loc Location, id int64) error {
	return m.mtx(loc, id, func(tx *sql.Tx, now int64) error {
		if _, err := tx.Exec(`UPDATE tickets SET milestone_id = 0 WHERE milestone_id = ?`, id); err != nil {
			return err
		}
		_, err := tx.Exec(`DELETE FROM milestones WHERE id = ?`, id)
		return err
	})
}

// StoryOp changes the user stories: add (Text), edit (ID, Text), check (ID, Done: the user
// only), move (ID, Delta: one place up when < 0), delete (ID).
type StoryOp struct {
	Op    string `json:"op"`
	ID    int64  `json:"id"`
	Text  string `json:"text"`
	Done  bool   `json:"done"`
	Delta int    `json:"delta"`
}

// Story changes the user stories of a milestone; it returns the id of an added one.
func (m *Manager) Story(loc Location, id int64, op StoryOp, by string) (int64, error) {
	text := strings.TrimSpace(op.Text)
	if op.Op == "add" || op.Op == "edit" {
		if text == "" {
			return 0, i18n.New("empty user story")
		}
		if n := utf8.RuneCountInString(text); n > MaxStory {
			return 0, i18n.Errorf("user story too long (%d characters, %d max)", n, MaxStory)
		}
	}
	if op.Op == "check" && by != ByUser {
		return 0, i18n.New("only the user validates a user story")
	}
	var sid int64
	err := m.mtx(loc, id, func(tx *sql.Tx, now int64) error {
		var res sql.Result
		var err error
		switch op.Op {
		case "add":
			res, err = tx.Exec(`INSERT INTO stories (milestone_id, pos, text, created) VALUES (?, (SELECT COALESCE(MAX(pos), 0) + 1 FROM stories WHERE milestone_id = ?), ?, ?)`, id, id, text, now)
			if err == nil {
				sid, _ = res.LastInsertId()
			}
		case "edit":
			res, err = tx.Exec(`UPDATE stories SET text = ? WHERE milestone_id = ? AND id = ?`, text, id, op.ID)
		case "check":
			res, err = tx.Exec(`UPDATE stories SET done = ? WHERE milestone_id = ? AND id = ?`, op.Done, id, op.ID)
		case "move":
			return moveStory(tx, id, op.ID, op.Delta)
		case "delete":
			res, err = tx.Exec(`DELETE FROM stories WHERE milestone_id = ? AND id = ?`, id, op.ID)
		default:
			return i18n.Errorf("unknown operation: %s", op.Op)
		}
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return i18n.Errorf("user story %d not found", op.ID)
		}
		return nil
	})
	return sid, err
}

// moveStory swaps a story with its neighbour (delta < 0: the one before).
func moveStory(tx *sql.Tx, id, sid int64, delta int) error {
	var ids []int64
	rows, err := tx.Query(`SELECT id FROM stories WHERE milestone_id = ? ORDER BY pos, id`, id)
	if err != nil {
		return err
	}
	for rows.Next() {
		var s int64
		if err := rows.Scan(&s); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, s)
	}
	rows.Close()
	at := -1
	for i, s := range ids {
		if s == sid {
			at = i
		}
	}
	if at < 0 {
		return i18n.Errorf("user story %d not found", sid)
	}
	to := at + 1
	if delta < 0 {
		to = at - 1
	}
	if to < 0 || to >= len(ids) {
		return nil
	}
	ids[at], ids[to] = ids[to], ids[at]
	for i, s := range ids {
		if _, err := tx.Exec(`UPDATE stories SET pos = ? WHERE id = ?`, i+1, s); err != nil {
			return err
		}
	}
	return nil
}

// MilestoneFeedback changes the feedback of a milestone (the operations of the feedback of a
// ticket); it returns the id of an added one.
func (m *Manager) MilestoneFeedback(loc Location, id int64, op FeedbackOp, by string) (int64, error) {
	var fid int64
	err := m.mtx(loc, id, func(tx *sql.Tx, now int64) error {
		var res sql.Result
		var err error
		switch op.Op {
		case "add":
			text := strings.TrimSpace(op.Text)
			if err := checkFeedback(text, op.Kind, op.Complexity); err != nil {
				return err
			}
			res, err = tx.Exec(`INSERT INTO milestone_feedback (milestone_id, kind, text, author, chat_id, created, complexity) VALUES (?, ?, ?, ?, ?, ?, ?)`, id, op.Kind, text, by, op.ChatID, now, op.Complexity)
			if err == nil {
				fid, _ = res.LastInsertId()
			}
		case "check":
			res, err = tx.Exec(`UPDATE milestone_feedback SET done = ? WHERE milestone_id = ? AND id = ?`, op.Done, id, op.ID)
		case "chat":
			res, err = tx.Exec(`UPDATE milestone_feedback SET chat_id = ? WHERE milestone_id = ? AND id = ?`, op.ChatID, id, op.ID)
		case "complexity":
			if op.Complexity != "" && !contains(Complexities, op.Complexity) {
				return i18n.Errorf("unknown complexity: %s (%s)", op.Complexity, strings.Join(Complexities, ", "))
			}
			res, err = tx.Exec(`UPDATE milestone_feedback SET complexity = ? WHERE milestone_id = ? AND id = ?`, op.Complexity, id, op.ID)
		case "delete":
			res, err = tx.Exec(`DELETE FROM milestone_feedback WHERE milestone_id = ? AND id = ?`, id, op.ID)
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

// checkFeedback checks the text, the kind and the complexity of a new feedback.
func checkFeedback(text, kind, complexity string) error {
	if text == "" {
		return i18n.New("empty feedback")
	}
	if n := utf8.RuneCountInString(text); n > MaxNote {
		return i18n.Errorf("feedback too long (%d characters, %d max)", n, MaxNote)
	}
	if !contains(FeedbackKinds, kind) {
		return i18n.Errorf("unknown feedback kind: %s (%s)", kind, strings.Join(FeedbackKinds, ", "))
	}
	if complexity != "" && !contains(Complexities, complexity) {
		return i18n.Errorf("unknown complexity: %s (%s)", complexity, strings.Join(Complexities, ", "))
	}
	return nil
}

// FeedbackTicket makes a ticket of the milestone from an open feedback: its first line as the
// title, its text as the description. The feedback is handled once that ticket is Done.
func (m *Manager) FeedbackTicket(loc Location, id, fid int64, by string) (int64, error) {
	var tid int64
	err := m.mtx(loc, id, func(tx *sql.Tx, now int64) error {
		var text, complexity, state string
		var done bool
		var ticket int64
		err := tx.QueryRow(`SELECT f.text, f.complexity, f.done, f.ticket_id, m.state FROM milestone_feedback f JOIN milestones m ON m.id = f.milestone_id WHERE f.milestone_id = ? AND f.id = ?`, id, fid).
			Scan(&text, &complexity, &done, &ticket, &state)
		if errors.Is(err, sql.ErrNoRows) {
			return i18n.Errorf("feedback %d not found", fid)
		}
		if err != nil {
			return err
		}
		if state != MilestoneOpen {
			return i18n.New("the milestone is closed")
		}
		if done || ticket != 0 {
			return i18n.Errorf("feedback %d is handled already", fid)
		}
		title := []rune(strings.TrimSpace(strings.SplitN(text, "\n", 2)[0]))
		if len(title) > 80 {
			title = append(title[:79], '…')
		}
		t, d := string(title), text
		if tid, err = createTicket(tx, Patch{Title: &t, Description: &d}, by, now); err != nil {
			return err
		}
		if complexity != "" {
			if _, err := tx.Exec(`UPDATE tickets SET complexity = ? WHERE id = ?`, complexity, tid); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(`UPDATE tickets SET milestone_id = ? WHERE id = ?`, id, tid); err != nil {
			return err
		}
		if err := event(tx, tid, by, "Made from a feedback of the milestone {milestone}", Params{"milestone": id}, now); err != nil {
			return err
		}
		_, err = tx.Exec(`UPDATE milestone_feedback SET ticket_id = ? WHERE id = ?`, tid, fid)
		return err
	})
	return tid, err
}

// ValidateMilestone closes a milestone: the user only, once every ticket is finished, every
// story checked and no feedback open.
func (m *Manager) ValidateMilestone(loc Location, id int64, by string) error {
	if by != ByUser {
		return i18n.New("only the user validates a milestone")
	}
	return m.mtx(loc, id, func(tx *sql.Tx, now int64) error {
		var s MilestoneSummary
		if err := scanMilestone(tx.QueryRow(`SELECT `+milestoneCols+` FROM milestones m WHERE m.id = ?`, id), &s); err != nil {
			return err
		}
		if s.State != MilestoneOpen {
			return i18n.New("the milestone is closed")
		}
		if n := s.Tickets - s.TicketsDone; n > 0 {
			return i18n.Errorf("%d ticket(s) of this milestone are not finished", n)
		}
		if n := s.Stories - s.StoriesDone; n > 0 {
			return i18n.Errorf("%d user story(ies) not validated yet", n)
		}
		if s.FeedbackOpen > 0 {
			return i18n.Errorf("%d feedback(s) of this milestone not handled yet", s.FeedbackOpen)
		}
		return setMilestoneState(tx, id, s.State, MilestoneValidated, by, now)
	})
}

// SetMilestoneState reopens a closed milestone or abandons an open one: the user only.
func (m *Manager) SetMilestoneState(loc Location, id int64, to, by string) error {
	if by != ByUser {
		return i18n.New("only the user closes or reopens a milestone")
	}
	return m.mtx(loc, id, func(tx *sql.Tx, now int64) error {
		var from string
		if err := tx.QueryRow(`SELECT state FROM milestones WHERE id = ?`, id).Scan(&from); err != nil {
			return err
		}
		if from == to {
			return nil
		}
		switch {
		case to == MilestoneValidated:
			return i18n.New("a milestone is validated once its tickets, stories and feedback are done")
		case to != MilestoneOpen && to != MilestoneAbandoned:
			return i18n.Errorf("unknown milestone state: %s", to)
		case to == MilestoneAbandoned && from != MilestoneOpen:
			return i18n.New("only an open milestone can be abandoned")
		}
		return setMilestoneState(tx, id, from, to, by, now)
	})
}

func setMilestoneState(tx *sql.Tx, id int64, from, to, by string, now int64) error {
	closed := int64(0)
	if to != MilestoneOpen {
		closed = now
	}
	if _, err := tx.Exec(`UPDATE milestones SET state = ?, closed = ? WHERE id = ?`, to, closed, id); err != nil {
		return err
	}
	return milestoneEvent(tx, id, by, "{from} → {to}", Params{"from": from, "to": to}, now)
}

func (m *Manager) LinkMilestoneChat(loc Location, id int64, chatID, role, title string) error {
	if role != "briefing" {
		return i18n.Errorf("unknown conversation role: %s", role)
	}
	return m.mtx(loc, id, func(tx *sql.Tx, now int64) error {
		_, err := tx.Exec(`INSERT INTO milestone_chats (milestone_id, chat_id, role, title, created) VALUES (?, ?, ?, ?, ?)
			ON CONFLICT(milestone_id, chat_id) DO UPDATE SET role = excluded.role, title = CASE WHEN excluded.title != '' THEN excluded.title ELSE milestone_chats.title END`,
			id, chatID, role, title, now)
		return err
	})
}

func (m *Manager) UnlinkMilestoneChat(loc Location, id int64, chatID string) error {
	return m.mtx(loc, id, func(tx *sql.Tx, now int64) error {
		_, err := tx.Exec(`DELETE FROM milestone_chats WHERE milestone_id = ? AND chat_id = ?`, id, chatID)
		return err
	})
}

// MilestoneLinked tells whether a conversation is linked to a milestone.
func (m *Manager) MilestoneLinked(loc Location, id int64, chatID string) bool {
	db, err := m.db(loc)
	if err != nil {
		return false
	}
	var n int
	_ = db.QueryRow(`SELECT COUNT(*) FROM milestone_chats WHERE milestone_id = ? AND chat_id = ?`, id, chatID).Scan(&n)
	return n > 0
}

// AddMilestoneNote adds a note to a milestone; chatID is the conversation that wrote it.
func (m *Manager) AddMilestoneNote(loc Location, id int64, text, by, chatID string) error {
	text = strings.TrimSpace(text)
	if text == "" {
		return i18n.New("empty note")
	}
	if n := utf8.RuneCountInString(text); n > MaxNote {
		return i18n.Errorf("note too long (%d characters, %d max)", n, MaxNote)
	}
	return m.mtx(loc, id, func(tx *sql.Tx, now int64) error {
		_, err := tx.Exec(`INSERT INTO milestone_notes (milestone_id, kind, author, text, chat_id, created) VALUES (?, 'note', ?, ?, ?, ?)`, id, by, text, chatID, now)
		return err
	})
}

func (m *Manager) DeleteMilestoneNote(loc Location, id, noteID int64) error {
	return m.mtx(loc, id, func(tx *sql.Tx, now int64) error {
		_, err := tx.Exec(`DELETE FROM milestone_notes WHERE milestone_id = ? AND id = ? AND kind != 'event'`, id, noteID)
		return err
	})
}

// setMilestone puts a ticket and its steps in a milestone (0: out of it). A step takes the
// milestone of its root.
func setMilestone(tx *sql.Tx, id int64, ms *int64, by string, now int64) error {
	if ms == nil {
		return nil
	}
	var parent, cur int64
	if err := tx.QueryRow(`SELECT parent_id, milestone_id FROM tickets WHERE id = ?`, id).Scan(&parent, &cur); err != nil {
		return err
	}
	if cur == *ms {
		return nil
	}
	if parent != 0 {
		return i18n.Errorf("a step takes the milestone of its root (#%d)", parent)
	}
	title, err := milestoneTitle(tx, cur)
	if *ms != 0 {
		var state string
		if err := tx.QueryRow(`SELECT title, state FROM milestones WHERE id = ?`, *ms).Scan(&title, &state); err != nil {
			return i18n.Errorf("milestone %d not found", *ms)
		}
		if state != MilestoneOpen {
			return i18n.Errorf("milestone %d is closed", *ms)
		}
	} else if err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE tickets SET milestone_id = ? WHERE id = ? OR parent_id = ?`, *ms, id, id); err != nil {
		return err
	}
	if *ms == 0 {
		return event(tx, id, by, "Removed from the milestone “{milestone}”", Params{"milestone": title}, now)
	}
	if err := event(tx, id, by, "Milestone: “{milestone}”", Params{"milestone": title}, now); err != nil {
		return err
	}
	// A ticket without objective takes the default objective of its milestone.
	var own, def int64
	if err := tx.QueryRow(`SELECT t.objective_id, m.objective_id FROM tickets t, milestones m WHERE t.id = ? AND m.id = ?`, id, *ms).Scan(&own, &def); err != nil {
		return err
	}
	if own != 0 || def == 0 {
		return nil
	}
	return setObjective(tx, id, &def, by, now)
}

func milestoneTitle(tx *sql.Tx, id int64) (string, error) {
	var title string
	err := tx.QueryRow(`SELECT title FROM milestones WHERE id = ?`, id).Scan(&title)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return title, err
}
