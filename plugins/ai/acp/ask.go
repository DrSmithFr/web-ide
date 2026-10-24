package acp

import (
	"encoding/json"

	"antennae.space/ide/lib/i18n"
)

// The questions an agent asks the user: a permission (session/request_permission, its
// options) or a form (elicitation/create: a message and a JSON schema; claude-agent-acp asks
// Claude's AskUserQuestion this way). Both are entries of the thread, open until answered;
// the Questions tool of the page lists the open ones of every session (Manager.Questions).

// Ask is an open question of a session, for the Questions tool.
type Ask struct {
	Session string `json:"session"`
	// Name of the agent and Title of the session.
	Name  string `json:"name"`
	Title string `json:"title"`
	Index int    `json:"index"`
	Entry Entry  `json:"entry"`
	// Last is the last message of the agent before the question (its context).
	Last string `json:"last,omitempty"`
	// AutoOpen: the override of the session (nil: the setting of the page).
	AutoOpen *bool `json:"autoOpen,omitempty"`
}

// isOpenAsk: a question still waiting for the user.
func isOpenAsk(e Entry) bool {
	return (e.Kind == "permission" || e.Kind == "question") && e.Outcome == ""
}

// openAsks counts the questions waiting (under s.mu).
func (s *Session) openAsks() int {
	n := 0
	for _, e := range s.th.Entries {
		if isOpenAsk(e) {
			n++
		}
	}
	return n
}

// questionsChanged tells the windows that the open questions changed; asked names the
// session when one was just asked (a push notification).
func (s *Session) questionsChanged(asked bool) {
	data := map[string]any{"session": s.info.ID}
	if asked {
		data["asked"] = s.info.Title
	}
	s.m.emit("questions.changed", data)
}

// ask records a question of the agent and waits for the user (under s.mu).
func (s *Session) ask(e Entry) {
	s.entry(s.th.add(e))
	s.asking()
	s.setState(Asking, "")
	s.questionsChanged(true)
}

// closed: a question was answered or cancelled; the turn goes on when none is left (under
// s.mu).
func (s *Session) askClosed() {
	if s.openAsks() == 0 {
		s.answered()
		if s.info.State == Asking {
			s.setState(Busy, "")
		}
	}
	s.questionsChanged(false)
}

// elicitation records a form asked with elicitation/create (url mode is not offered, so it
// is cancelled).
func (s *Session) elicitation(m message) {
	var p struct {
		Mode            string          `json:"mode"`
		Message         string          `json:"message"`
		ToolCallID      string          `json:"toolCallId"`
		RequestedSchema json.RawMessage `json:"requestedSchema"`
	}
	_ = json.Unmarshal(m.Params, &p)
	if p.Mode == "url" || len(p.RequestedSchema) == 0 {
		_ = s.reply(m.ID, map[string]string{"action": "cancel"})
		return
	}
	s.ask(Entry{Kind: "question", ToolID: p.ToolCallID, Text: p.Message, Schema: p.RequestedSchema, Request: m.ID})
}

// cancelRequest: the agent withdrew a request ($/cancel_request), a question among them.
func (s *Session) cancelRequest(params json.RawMessage) {
	var p struct {
		RequestID json.RawMessage `json:"requestId"`
	}
	if json.Unmarshal(params, &p) != nil || len(p.RequestID) == 0 {
		return
	}
	for i := range s.th.Entries {
		e := &s.th.Entries[i]
		if isOpenAsk(*e) && string(e.Request) == string(p.RequestID) {
			e.Outcome = "cancelled"
			// A request cancelled still gets an answer (JSON-RPC: request cancelled).
			_ = s.write(map[string]any{"jsonrpc": "2.0", "id": e.Request, "error": rpcError{Code: -32800, Message: "request cancelled"}})
			s.entry(i)
			s.askClosed()
		}
	}
}

// cancelAsks answers the questions still open as cancelled (the turn stops).
func (s *Session) cancelAsks() {
	n := 0
	for i := range s.th.Entries {
		e := &s.th.Entries[i]
		if !isOpenAsk(*e) {
			continue
		}
		e.Outcome = "cancelled"
		if e.Kind == "question" {
			_ = s.reply(e.Request, map[string]string{"action": "cancel"})
		} else {
			_ = s.reply(e.Request, map[string]any{"outcome": map[string]string{"outcome": "cancelled"}})
		}
		s.entry(i)
		n++
	}
	s.answered()
	if n > 0 {
		s.questionsChanged(false)
	}
}

// openAsk returns the open question of entry index.
func (s *Session) openAsk(index int, kind string) (*Entry, error) {
	if index < 0 || index >= len(s.th.Entries) || s.th.Entries[index].Kind != kind || s.th.Entries[index].Outcome != "" {
		return nil, i18n.New("this question is no longer open")
	}
	return &s.th.Entries[index], nil
}

// AnswerForm answers the form of entry index: action accept (with the content of the form),
// decline (the user skips it) or cancel.
func (s *Session) AnswerForm(index int, action string, content json.RawMessage) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, err := s.openAsk(index, "question")
	if err != nil {
		return err
	}
	res := map[string]any{"action": action}
	switch action {
	case "accept":
		if len(content) == 0 {
			content = json.RawMessage("{}")
		}
		res["content"] = content
		e.Answer = content
	case "decline", "cancel":
	default:
		return i18n.Errorf("unknown answer: %s", action)
	}
	if err := s.reply(e.Request, res); err != nil {
		return err
	}
	e.Outcome = action
	s.entry(index)
	s.askClosed()
	return nil
}

// SetAutoOpen overrides, for this session, the setting that opens its questions by themselves
// (nil: follow the setting).
func (s *Session) SetAutoOpen(v *bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.info.AutoOpen = v
	s.changed()
	s.questionsChanged(false)
}

// asks returns the open questions of the session, each with the last message of the agent
// before it.
func (s *Session) asks() []Ask {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []Ask
	last := ""
	for i, e := range s.th.Entries {
		switch {
		case e.Kind == "agent":
			last = e.Text
		case e.Kind == "user":
			last = ""
		case isOpenAsk(e):
			out = append(out, Ask{Session: s.info.ID, Name: s.info.Name, Title: s.info.Title, Index: i, Entry: e, Last: last, AutoOpen: s.info.AutoOpen})
		}
	}
	return out
}

// Questions are the open questions of every session, oldest first.
func (m *Manager) Questions() []Ask {
	var out []Ask
	for _, i := range m.List() {
		if s, err := m.Get(i.ID); err == nil {
			out = append(out, s.asks()...)
		}
	}
	return out
}
