package acp

import (
	"bufio"
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"time"
)

// Turn is the record of a turn of an agent, kept in <data>/agents/<project>/turns.jsonl after
// its session is closed: the work time and the cost are summed from them.
type Turn struct {
	Session string `json:"session"`
	Agent   string `json:"agent"`
	Project string `json:"project"`
	// Ticket of the worktree the session ran in (0: none).
	Ticket int64 `json:"ticket,omitempty"`
	Start  int64 `json:"start"`
	End    int64 `json:"end"`
	// Wait is the time spent waiting for the user (open permissions) within the turn, ms.
	Wait   int64    `json:"wait,omitempty"`
	Tokens []Tokens `json:"tokens,omitempty"`
	// Reported is the cost the agent told (opencode, pi), used before the price table.
	Reported float64 `json:"reported,omitempty"`
	// Subscription: the agent runs on a subscription, the cost is an API equivalent.
	Subscription bool `json:"subscription,omitempty"`
}

// Totals sum turns. Work excludes the waits. Cost sums the turns priced; Unpriced counts the
// others (a local model, an unknown one), whose tokens still count.
type Totals struct {
	Turns    int     `json:"turns"`
	Work     int64   `json:"work"`
	Wait     int64   `json:"wait"`
	Tokens   int64   `json:"tokens"`
	Cost     float64 `json:"cost"`
	Unpriced int     `json:"unpriced,omitempty"`
	// APIEquivalent: some of the cost was spent under a subscription.
	APIEquivalent bool `json:"apiEquivalent,omitempty"`
}

func (t *Totals) add(r Turn) {
	t.Turns++
	t.Wait += r.Wait
	if w := r.End - r.Start - r.Wait; w > 0 {
		t.Work += w
	}
	for _, x := range r.Tokens {
		t.Tokens += x.total()
	}
	switch c, ok := cost(r.Tokens); {
	case r.Reported > 0:
		t.Cost += r.Reported
	case ok && len(r.Tokens) > 0:
		t.Cost += c
		t.APIEquivalent = t.APIEquivalent || r.Subscription
	default:
		t.Unpriced++
	}
}

var ticketOfProject = regexp.MustCompile(`-t(\d+)$`)

// ticketOf is the ticket of a ticket worktree project (`<parent>-t<n>`), else 0.
func ticketOf(project string) int64 {
	if m := ticketOfProject.FindStringSubmatch(project); m != nil {
		n, _ := strconv.ParseInt(m[1], 10, 64)
		return n
	}
	return 0
}

func (m *Manager) turnsFile() string { return filepath.Join(m.Dir, "turns.jsonl") }

func (m *Manager) record(r Turn) {
	data, err := json.Marshal(r)
	if err != nil {
		return
	}
	if err := os.MkdirAll(m.Dir, 0o700); err != nil {
		log.Printf("acp: %v", err)
		return
	}
	f, err := os.OpenFile(m.turnsFile(), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		log.Printf("acp: %v", err)
		return
	}
	defer f.Close()
	_, _ = f.Write(append(data, '\n'))
}

// Sum adds the turns of the projects of root (<data>/agents) that match, started since.
func Sum(root string, match func(project string) bool, since time.Time) Totals {
	var t Totals
	dirs, _ := os.ReadDir(root)
	for _, d := range dirs {
		if !d.IsDir() || !match(d.Name()) {
			continue
		}
		f, err := os.Open(filepath.Join(root, d.Name(), "turns.jsonl"))
		if err != nil {
			continue
		}
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 64*1024), 1<<20)
		for sc.Scan() {
			var r Turn
			if json.Unmarshal(sc.Bytes(), &r) == nil && r.Start >= since.UnixMilli() {
				t.add(r)
			}
		}
		f.Close()
	}
	return t
}
