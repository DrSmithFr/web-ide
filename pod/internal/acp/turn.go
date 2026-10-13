package acp

import (
	"encoding/json"
	"time"
)

// turnState follows the turn running (saved with the session: a turn goes on across a restart
// of the pod).
type turnState struct {
	Start int64 `json:"start"`
	// Waited sums the waits for the user ended; AskSince starts the one open (ms, 0: none).
	Waited   int64 `json:"waited,omitempty"`
	AskSince int64 `json:"askSince,omitempty"`
	// CostAt is the cost the agent had told at the start (opencode tells a running total).
	CostAt float64 `json:"costAt,omitempty"`
}

func now() int64 { return time.Now().UnixMilli() }

// beginTurn starts the clock of a turn (under s.mu).
func (s *Session) beginTurn() {
	s.turn = &turnState{Start: now(), CostAt: s.reportedCost}
}

// asking: a question waits for the user (under s.mu).
func (s *Session) asking() {
	if s.turn != nil && s.turn.AskSince == 0 {
		s.turn.AskSince = now()
	}
}

// answered: no question waits anymore (under s.mu).
func (s *Session) answered() {
	if s.turn != nil && s.turn.AskSince != 0 {
		s.turn.Waited += now() - s.turn.AskSince
		s.turn.AskSince = 0
	}
}

// model is the current model of the session, as its config option tells it.
func (s *Session) model() string {
	for _, c := range s.info.Config {
		if c.Category == "model" {
			return c.CurrentValue
		}
	}
	return ""
}

// finishTurn records the turn that ended with result (nil: the agent stopped), and adds it to
// the totals of the session (under s.mu).
func (s *Session) finishTurn(result json.RawMessage) {
	if s.turn == nil {
		return
	}
	s.answered()
	t := s.turn
	s.turn = nil
	r := Turn{Session: s.info.ID, Agent: s.info.Agent, Project: s.m.Owner, Ticket: ticketOf(s.m.Owner), Start: t.Start, End: now(), Wait: t.Waited, Subscription: s.subscription}
	if result != nil {
		r.Tokens = promptUsage(result, s.model())
	}
	if len(r.Tokens) == 0 && s.info.Agent == "pi" {
		r.Tokens, r.Reported = piUsage(s.acp, r.Start, r.End)
	}
	if d := s.reportedCost - t.CostAt; r.Reported == 0 && d > 0 {
		r.Reported = d
	}
	s.m.record(r)
	if s.info.Stats == nil {
		s.info.Stats = &Totals{}
	}
	s.info.Stats.add(r)
}

// authStatus notes whether the agent runs on a subscription (claude-agent-acp tells it with
// _auth/status_update: kind "account" is a Claude plan, else an API key).
func (s *Session) authStatus(params json.RawMessage) {
	var p struct {
		AuthStatus struct {
			Kind string `json:"kind"`
		} `json:"authStatus"`
	}
	if json.Unmarshal(params, &p) == nil && p.AuthStatus.Kind != "" {
		s.subscription = p.AuthStatus.Kind == "account"
	}
}
