package kanban

import (
	"encoding/json"
	"sort"
)

// What the Orchestrator reads of the kanban: what happened in a period (history) and what
// can be done next.

// Activity is a ticket with the events of its history in a period.
type Activity struct {
	Summary
	Events []Note `json:"events"`
}

// History lists the tickets with events between from and to (Unix ms), the tickets closed in
// the period first, then the most recent activity first.
func (m *Manager) History(loc Location, from, to int64) ([]Activity, error) {
	db, err := m.db(loc)
	if err != nil {
		return nil, err
	}
	rows, err := db.Query(`SELECT ticket_id, id, author, text, created FROM notes WHERE kind = 'event' AND created >= ? AND created < ? ORDER BY created`, from, to)
	if err != nil {
		return nil, err
	}
	events := map[int64][]Note{}
	for rows.Next() {
		var id int64
		n := Note{Kind: "event"}
		if err := rows.Scan(&id, &n.ID, &n.Author, &n.Text, &n.Created); err != nil {
			rows.Close()
			return nil, err
		}
		events[id] = append(events[id], n)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	list, err := summaries(db, `ORDER BY t.id`)
	if err != nil {
		return nil, err
	}
	out := []Activity{}
	for _, t := range list {
		if evs := events[t.ID]; len(evs) > 0 {
			out = append(out, Activity{Summary: t, Events: evs})
		}
	}
	closedIn := func(a Activity) bool { return a.Closed >= from && a.Closed < to }
	last := func(a Activity) int64 { return a.Events[len(a.Events)-1].Created }
	sort.SliceStable(out, func(i, j int) bool {
		if ci, cj := closedIn(out[i]), closedIn(out[j]); ci != cj {
			return ci
		}
		return last(out[i]) > last(out[j])
	})
	return out, nil
}

// EventKey reads the key and parameters of an event (see EventText).
func EventKey(text string) (string, Params) {
	var v struct {
		Key    string `json:"key"`
		Params Params `json:"params"`
	}
	if json.Unmarshal([]byte(text), &v) != nil {
		return text, nil
	}
	return v.Key, v.Params
}

var (
	priorityRank = map[string]int{"critical": 0, "high": 1, "normal": 2, "low": 3}
	sizeRank     = map[string]int{"s": 0, "m": 1, "l": 2, "xl": 3, "": 4}
)

// Next orders the tickets that can start now (To do, no blocker; Blockers filled): the next
// step of a lineage in progress first, then by priority, smaller size, age.
func Next(list []Summary) []Summary {
	status := map[int64]string{}
	for _, t := range list {
		status[t.ID] = t.Status
	}
	var out []Summary
	for _, t := range list {
		if t.Status == Todo && len(t.Blockers) == 0 {
			out = append(out, t)
		}
	}
	step := func(t Summary) bool { return t.Parent != 0 && status[t.Parent] != Todo && status[t.Parent] != New }
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if sa, sb := step(a), step(b); sa != sb {
			return sa
		}
		if pa, pb := priorityRank[a.Priority], priorityRank[b.Priority]; pa != pb {
			return pa < pb
		}
		if za, zb := sizeRank[a.Size], sizeRank[b.Size]; za != zb {
			return za < zb
		}
		return a.Created < b.Created
	})
	return out
}

// Waiting lists the tickets waiting for the user: to test (validation), or in progress with
// open feedback.
func Waiting(list []Summary) []Summary {
	var out []Summary
	for _, t := range list {
		if t.Status == Review || t.Status == InProgress && t.FeedbackOpen > 0 {
			out = append(out, t)
		}
	}
	return out
}
