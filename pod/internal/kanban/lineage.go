package kanban

import (
	"database/sql"
	"sort"

	"github.com/DrSmithFr/web-ide/pod/internal/i18n"
)

// Lineages and dependencies (docs/kanban.md). The children of a ticket form its lineage:
// they are developed one after the other in the worktree and on the branch of their parent,
// which is merged once they are all finished. A ticket may also depend on tickets of other
// lineages: it cannot start before they are merged or done.

// Blocker is a reason why a ticket cannot start yet.
type Blocker struct {
	ID   int64  `json:"id"`
	Kind string `json:"kind"`
}

// Kinds of blockers.
const (
	BlockParent    = "parent"    // the parent is not started
	BlockPrevious  = "previous"  // the previous step of the lineage is not validated
	BlockDepends   = "depends"   // a dependency is neither merged nor done
	BlockAbandoned = "abandoned" // a dependency was abandoned: it never resolves
)

// BlockerNames are the English texts of the blockers (models, errors).
var BlockerNames = map[string]string{
	BlockParent:    "parent not started",
	BlockPrevious:  "previous step not validated",
	BlockDepends:   "dependency not merged",
	BlockAbandoned: "dependency abandoned",
}

func finished(status string) bool { return status == Done || status == Abandoned }

// Startable tells whether a ticket is waiting for its development (New or To do).
func Startable(status string) bool { return status == New || status == Todo }

// Blockers lists what keeps t from starting. all holds every ticket; merged tells whether
// the branch of a lineage root is merged into its base.
func Blockers(t *Summary, all []Summary, merged func(root *Summary) bool) []Blocker {
	byID := map[int64]*Summary{}
	for i := range all {
		byID[all[i].ID] = &all[i]
	}
	var out []Blocker
	if p := byID[t.Parent]; p != nil {
		var prev *Summary
		for _, s := range Lineage(p.ID, all) {
			if s.ID == t.ID {
				break
			}
			prev = s
		}
		switch {
		case prev != nil && !finished(prev.Status):
			out = append(out, Blocker{prev.ID, BlockPrevious})
		case prev == nil && Startable(p.Status):
			out = append(out, Blocker{p.ID, BlockParent})
		case prev == nil && !p.StepDone:
			out = append(out, Blocker{p.ID, BlockPrevious})
		}
	}
	for _, id := range t.DependsOn {
		d := byID[id]
		if d == nil {
			continue
		}
		root := d
		if r := byID[d.Parent]; r != nil {
			root = r
		}
		switch {
		case d.Status == Abandoned || root.Status == Abandoned:
			out = append(out, Blocker{id, BlockAbandoned})
		case root.Status == Done || merged != nil && merged(root):
		default:
			out = append(out, Blocker{id, BlockDepends})
		}
	}
	return out
}

// Lineage lists the children of a ticket in their order.
func Lineage(parent int64, all []Summary) []*Summary {
	var out []*Summary
	for i := range all {
		if all[i].Parent == parent {
			out = append(out, &all[i])
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Pos < out[j].Pos || out[i].Pos == out[j].Pos && out[i].ID < out[j].ID
	})
	return out
}

// OpenChildren lists the children of a ticket that are neither done nor abandoned.
func OpenChildren(t *Ticket) []int64 {
	var out []int64
	for _, c := range t.Children {
		if !finished(c.Status) {
			out = append(out, c.ID)
		}
	}
	return out
}

// setLinks applies the parent and the dependencies of a patch, then checks the whole graph.
func setLinks(tx *sql.Tx, id int64, p Patch, by string, now int64) error {
	if p.Parent == nil && p.DependsOn == nil {
		return nil
	}
	var parent int64
	var status string
	if err := tx.QueryRow(`SELECT parent_id, status FROM tickets WHERE id = ?`, id).Scan(&parent, &status); err != nil {
		return err
	}
	if p.Parent != nil && *p.Parent != parent {
		if !Startable(status) {
			return i18n.New("the lineage of a started ticket cannot change")
		}
		np := *p.Parent
		pos := 0
		if np != 0 {
			var pst string
			if err := tx.QueryRow(`SELECT status FROM tickets WHERE id = ?`, np).Scan(&pst); err != nil {
				return i18n.Errorf("ticket #%d not found", np)
			}
			if finished(pst) {
				return i18n.Errorf("ticket #%d is closed: it cannot get children", np)
			}
			if err := tx.QueryRow(`SELECT COALESCE(MAX(pos), 0) + 1 FROM tickets WHERE parent_id = ?`, np).Scan(&pos); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(`UPDATE tickets SET parent_id = ?, pos = ? WHERE id = ?`, np, pos, id); err != nil {
			return err
		}
		if np == 0 {
			if err := event(tx, id, by, "Removed from the lineage of #{parent}", Params{"parent": parent}, now); err != nil {
				return err
			}
		} else if err := event(tx, id, by, "Child of #{parent}", Params{"parent": np}, now); err != nil {
			return err
		}
	}
	if p.DependsOn != nil {
		old := map[int64]bool{}
		rows, err := tx.Query(`SELECT dep_id FROM deps WHERE ticket_id = ?`, id)
		if err != nil {
			return err
		}
		for rows.Next() {
			var d int64
			if err := rows.Scan(&d); err != nil {
				rows.Close()
				return err
			}
			old[d] = true
		}
		rows.Close()
		want := map[int64]bool{}
		for _, d := range *p.DependsOn {
			want[d] = true
		}
		for _, d := range sortedKeys(want) {
			if old[d] {
				continue
			}
			var n int
			if err := tx.QueryRow(`SELECT COUNT(*) FROM tickets WHERE id = ?`, d).Scan(&n); err != nil || n == 0 {
				return i18n.Errorf("ticket #%d not found", d)
			}
			if _, err := tx.Exec(`INSERT INTO deps (ticket_id, dep_id) VALUES (?, ?)`, id, d); err != nil {
				return err
			}
			if err := event(tx, id, by, "Depends on #{dep}", Params{"dep": d}, now); err != nil {
				return err
			}
		}
		for _, d := range sortedKeys(old) {
			if want[d] {
				continue
			}
			if _, err := tx.Exec(`DELETE FROM deps WHERE ticket_id = ? AND dep_id = ?`, id, d); err != nil {
				return err
			}
			if err := event(tx, id, by, "No longer depends on #{dep}", Params{"dep": d}, now); err != nil {
				return err
			}
		}
	}
	return checkGraph(tx)
}

func sortedKeys(m map[int64]bool) []int64 {
	out := make([]int64, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// checkGraph refuses a lineage deeper than one level and dependencies that loop or stay
// inside a lineage (its order already says it).
func checkGraph(tx *sql.Tx) error {
	parent := map[int64]int64{}
	rows, err := tx.Query(`SELECT id, parent_id FROM tickets`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var id, p int64
		if err := rows.Scan(&id, &p); err != nil {
			rows.Close()
			return err
		}
		parent[id] = p
	}
	rows.Close()
	for id, p := range parent {
		if p == id {
			return i18n.New("a ticket cannot be its own parent")
		}
		if p != 0 && parent[p] != 0 {
			return i18n.Errorf("#%d is already the child of #%d: a lineage has one level", p, parent[p])
		}
	}
	root := func(id int64) int64 {
		if p := parent[id]; p != 0 {
			return p
		}
		return id
	}
	edges := map[int64][]int64{}
	rows, err = tx.Query(`SELECT ticket_id, dep_id FROM deps`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var id, d int64
		if err := rows.Scan(&id, &d); err != nil {
			rows.Close()
			return err
		}
		if root(id) == root(d) {
			rows.Close()
			return i18n.Errorf("#%d and #%d are in the same lineage: its order already applies", id, d)
		}
		edges[root(id)] = append(edges[root(id)], root(d))
	}
	rows.Close()
	// Depth-first search over the lineages: a lineage met again on the way is a cycle.
	const (
		seen = 1
		done = 2
	)
	state := map[int64]int{}
	var visit func(n int64) error
	visit = func(n int64) error {
		state[n] = seen
		for _, m := range edges[n] {
			switch state[m] {
			case seen:
				return i18n.Errorf("these dependencies make a loop (#%d ↔ #%d)", n, m)
			case 0:
				if err := visit(m); err != nil {
					return err
				}
			}
		}
		state[n] = done
		return nil
	}
	for _, n := range sortedKeys(func() map[int64]bool {
		m := map[int64]bool{}
		for k := range edges {
			m[k] = true
		}
		return m
	}()) {
		if state[n] == 0 {
			if err := visit(n); err != nil {
				return err
			}
		}
	}
	return nil
}

// MoveChild moves a child one place up (delta < 0) or down in its lineage; both children
// must still wait for their development.
func (m *Manager) MoveChild(loc Location, id int64, delta int, by string) error {
	return m.tx(loc, id, func(tx *sql.Tx, now int64) error {
		var parent int64
		var pos int
		var status string
		if err := tx.QueryRow(`SELECT parent_id, pos, status FROM tickets WHERE id = ?`, id).Scan(&parent, &pos, &status); err != nil {
			return err
		}
		if parent == 0 {
			return i18n.New("this ticket has no parent")
		}
		q := `SELECT id, pos, status FROM tickets WHERE parent_id = ? AND (pos > ? OR pos = ? AND id > ?) ORDER BY pos, id LIMIT 1`
		if delta < 0 {
			q = `SELECT id, pos, status FROM tickets WHERE parent_id = ? AND (pos < ? OR pos = ? AND id < ?) ORDER BY pos DESC, id DESC LIMIT 1`
		}
		var other int64
		var opos int
		var ostatus string
		if err := tx.QueryRow(q, parent, pos, pos, id).Scan(&other, &opos, &ostatus); err != nil {
			return nil // already first or last
		}
		if !Startable(status) || !Startable(ostatus) {
			return i18n.New("only children not started yet can change places")
		}
		if opos == pos {
			opos = pos + 1
			if delta < 0 {
				opos = pos - 1
			}
		}
		if _, err := tx.Exec(`UPDATE tickets SET pos = ? WHERE id = ?`, opos, id); err != nil {
			return err
		}
		if _, err := tx.Exec(`UPDATE tickets SET pos = ? WHERE id = ?`, pos, other); err != nil {
			return err
		}
		return event(tx, id, by, "Moved in the lineage of #{parent}", Params{"parent": parent}, now)
	})
}

// ValidateStep marks the own work of a parent as validated: its first child may start. The
// parent stays To test, its worktree kept, until its children are finished.
func (m *Manager) ValidateStep(loc Location, id int64, by string) error {
	return m.tx(loc, id, func(tx *sql.Tx, now int64) error {
		var status string
		var children int
		if err := tx.QueryRow(`SELECT status, (SELECT COUNT(*) FROM tickets c WHERE c.parent_id = t.id) FROM tickets t WHERE id = ?`, id).Scan(&status, &children); err != nil {
			return err
		}
		if children == 0 {
			return i18n.New("this ticket has no children")
		}
		if status != Review {
			return i18n.New("the step of a ticket is validated once it is “To test”")
		}
		if _, err := tx.Exec(`UPDATE tickets SET step_done = 1 WHERE id = ?`, id); err != nil {
			return err
		}
		return event(tx, id, by, "Step validated: the lineage goes on", nil, now)
	})
}
