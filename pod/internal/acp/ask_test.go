package acp

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// lastAsk is the last entry of the thread, a question.
func lastAsk(t *testing.T, s *Session) (int, Entry) {
	t.Helper()
	waitFor(t, "a question", func() bool { _, e := s.Thread(); return len(e) > 0 && isOpenAsk(e[len(e)-1]) })
	_, e := s.Thread()
	return len(e) - 1, e[len(e)-1]
}

func lastText(s *Session) string {
	_, e := s.Thread()
	for i := len(e) - 1; i >= 0; i-- {
		if e[i].Kind == "agent" {
			return e[i].Text
		}
	}
	return ""
}

func TestElicitationForm(t *testing.T) {
	dir := t.TempDir()
	m, rec := newManager(t, dir, nil)
	defer m.CloseAll()
	s := open(t, m)

	_ = s.Prompt("ask me")
	waitState(t, s, Asking)
	i, q := lastAsk(t, s)
	var schema struct {
		Properties map[string]json.RawMessage `json:"properties"`
	}
	if q.Kind != "question" || q.Text != "Which color?" || q.ToolID != "ask-1" || json.Unmarshal(q.Schema, &schema) != nil || len(schema.Properties) != 5 {
		t.Fatalf("question: %+v", q)
	}
	// Listed for the Questions tool, with the message of the agent before it.
	asks := m.Questions()
	if len(asks) != 1 || asks[0].Index != i || asks[0].Last != "I need to know your **favourite color**." || asks[0].Name != "Claude Code" {
		t.Fatalf("questions: %+v", asks)
	}
	rec.mu.Lock()
	all := strings.Join(rec.events, "\n")
	rec.mu.Unlock()
	if !strings.Contains(all, `questions.changed {"asked":"Claude Code"`) {
		t.Fatalf("a question asked is announced:\n%s", all)
	}
	if err := s.Answer(i, "allow"); err == nil {
		t.Fatal("a form is not a permission")
	}
	time.Sleep(50 * time.Millisecond) // a wait the turn counts
	if err := s.AnswerForm(i, "accept", json.RawMessage(`{"question_0":"Blue","count":2}`)); err != nil {
		t.Fatal(err)
	}
	waitState(t, s, Ready)
	if got := lastText(s); got != `You chose {"question_0":"Blue","count":2}.` {
		t.Fatalf("the answer reaches the agent: %q", got)
	}
	_, e := s.Thread()
	if e[i].Outcome != "accept" || string(e[i].Answer) != `{"question_0":"Blue","count":2}` || len(m.Questions()) != 0 {
		t.Fatalf("answered: %+v", e[i])
	}
	if st := s.Info().Stats; st == nil || st.Wait < 50 {
		t.Fatalf("the open question counts as waiting: %+v", st)
	}

	// Declined: the agent goes on without the answer.
	_ = s.Prompt("ask me again")
	i, _ = lastAsk(t, s)
	if err := s.AnswerForm(i, "decline", nil); err != nil {
		t.Fatal(err)
	}
	waitState(t, s, Ready)
	if got := lastText(s); got != "Skipped." {
		t.Fatalf("declined: %q", got)
	}
}

func TestQuestionWithdrawnOrStopped(t *testing.T) {
	dir := t.TempDir()
	m, _ := newManager(t, dir, nil)
	defer m.CloseAll()
	s := open(t, m)

	// The agent withdraws its question: it leaves the list.
	_ = s.Prompt("ask then withdraw")
	i, _ := lastAsk(t, s)
	waitState(t, s, Ready)
	_, e := s.Thread()
	if e[i].Outcome != "cancelled" || len(m.Questions()) != 0 || lastText(s) != "Withdrawn." {
		t.Fatalf("withdrawn: %+v", e[i])
	}
	if err := s.AnswerForm(i, "accept", nil); err == nil {
		t.Fatal("a question withdrawn is closed")
	}

	// Stop cancels the question open.
	_ = s.Prompt("ask me")
	i, _ = lastAsk(t, s)
	if err := s.Cancel(); err != nil {
		t.Fatal(err)
	}
	waitState(t, s, Ready)
	_, e = s.Thread()
	if e[i].Outcome != "cancelled" || len(m.Questions()) != 0 {
		t.Fatalf("stopped: %+v", e[i])
	}
}

func TestAutoOpenOverride(t *testing.T) {
	dir := t.TempDir()
	m, _ := newManager(t, dir, nil)
	defer m.CloseAll()
	s := open(t, m)
	off := false
	s.SetAutoOpen(&off)
	_ = s.Prompt("permission please")
	lastAsk(t, s)
	if q := m.Questions(); len(q) != 1 || q[0].AutoOpen == nil || *q[0].AutoOpen || q[0].Entry.Kind != "permission" {
		t.Fatalf("override: %+v", q)
	}
	s.SetAutoOpen(nil)
	if q := m.Questions(); q[0].AutoOpen != nil {
		t.Fatalf("back to the setting: %+v", q)
	}
}
