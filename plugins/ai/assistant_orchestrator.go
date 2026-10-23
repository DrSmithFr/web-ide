package ai

import (
	"fmt"
	"strings"
	"time"

	"github.com/DrSmithFr/web-ide/plugins/ai/engine"
	"github.com/DrSmithFr/web-ide/plugins/kanban/kanban"
	"github.com/DrSmithFr/web-ide/pod/assistant"
)

// Tools of the Orchestrator mode (assistant/orchestrator.go): what to do next, what was done,
// the conversations of the project, action cards the user clicks, and the conversation the
// user is moved into.

func (s *A) orchestratorTool(r *assistantRun, ref *runtimeRef, name string, a assistant.Args) (assistant.ToolResult, error) {
	switch name {
	case "kanban_next":
		return s.kanbanNext(r)
	case "kanban_history":
		return s.kanbanHistory(r, a.Str("from"), a.Str("to"))
	case "list_conversations":
		return s.listConversations(r, a.Str("query"), a.Num("limit"))
	case "action_card":
		return s.actionCard(r, a)
	case "open_conversation":
		return s.openConversation(r, a)
	}
	return assistant.ToolResult{}, assistant.Usagef("unknown tool: %s", name)
}

func ticketLine(t kanban.Summary) string {
	line := fmt.Sprintf("#%d [%s] (%s) %s", t.ID, kanban.StatusNames[t.Status], kanban.PriorityNames[t.Priority], t.Title)
	if t.Size != "" {
		line += " · size " + kanban.SizeNames[t.Size]
	}
	if t.Parent != 0 {
		line += fmt.Sprintf(" · step of #%d", t.Parent)
	}
	return line
}

func (s *A) kanbanNext(r *assistantRun) (assistant.ToolResult, error) {
	list, err := s.kb().Kanban.List(kanbanLocOf(r.loc))
	if err != nil {
		return assistant.ToolResult{}, err
	}
	s.kb().FillBlockers(r.ctx, s.kanbanGit(r), list)
	objectives, err := s.kb().Kanban.Objectives(kanbanLocOf(r.loc))
	if err != nil {
		return assistant.ToolResult{}, err
	}
	next := kanban.Next(list, objectives)
	var b strings.Builder
	b.WriteString("Can start now, in order:\n")
	if len(next) == 0 {
		b.WriteString("(none)\n")
	}
	for _, t := range next {
		b.WriteString("- " + ticketLine(t) + "\n")
	}
	if w := kanban.Waiting(list); len(w) > 0 {
		b.WriteString("\nWaiting for the user:\n")
		for _, t := range w {
			line := "- " + ticketLine(t)
			if t.Status == kanban.ToTest {
				line += " · to test and validate"
			}
			if t.FeedbackOpen > 0 {
				line += fmt.Sprintf(" · %d feedback to handle", t.FeedbackOpen)
			}
			b.WriteString(line + "\n")
		}
	}
	var blocked []string
	for _, t := range list {
		if t.Status == kanban.Todo && len(t.Blockers) > 0 {
			blocked = append(blocked, fmt.Sprintf("#%d (waits for %s)", t.ID, kanban.BlockersText(t.Blockers)))
		}
	}
	if len(blocked) > 0 {
		b.WriteString("\nBlocked: " + strings.Join(blocked, ", ") + "\n")
	}
	return assistant.OK(strings.TrimSpace(b.String()), assistant.Tn(len(next), "{n} ticket can start", "{n} tickets can start", nil)), nil
}

// parseDay reads "YYYY-MM-DD" or "YYYY-MM-DD HH:MM" in local time.
func parseDay(s string) (time.Time, error) {
	s = strings.TrimSpace(s)
	for _, layout := range []string{"2006-01-02 15:04", "2006-01-02T15:04", "2006-01-02"} {
		if t, err := time.ParseInLocation(layout, s, time.Local); err == nil {
			return t, nil
		}
	}
	return time.Time{}, assistant.Usagef("invalid date %q: use YYYY-MM-DD or YYYY-MM-DD HH:MM", s)
}

func (s *A) kanbanHistory(r *assistantRun, fromArg, toArg string) (assistant.ToolResult, error) {
	now := time.UnixMilli(s.kb().Kanban.Now()).In(time.Local)
	y, mo, d := now.Date()
	from, to := time.Date(y, mo, d-1, 0, 0, 0, 0, time.Local), now
	var err error
	if fromArg != "" {
		if from, err = parseDay(fromArg); err != nil {
			return assistant.ToolResult{}, err
		}
	}
	if toArg != "" {
		if to, err = parseDay(toArg); err != nil {
			return assistant.ToolResult{}, err
		}
	}
	list, err := s.kb().Kanban.History(kanbanLocOf(r.loc), from.UnixMilli(), to.UnixMilli())
	if err != nil {
		return assistant.ToolResult{}, err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Tickets that moved from %s to %s (closed ones first):", from.Format("2006-01-02 15:04"), to.Format("2006-01-02 15:04"))
	if len(list) == 0 {
		b.WriteString("\n(none)")
	}
	for _, t := range list {
		b.WriteString("\n- " + ticketLine(t.Summary))
		for _, e := range t.Events {
			key, p := kanban.EventKey(e.Text)
			text := key
			for k, v := range p {
				val := fmt.Sprint(v)
				if name, ok := kanban.StatusNames[val]; ok && (k == "from" || k == "to") {
					val = name
				}
				text = strings.ReplaceAll(text, "{"+k+"}", val)
			}
			fmt.Fprintf(&b, "\n  %s %s (%s)", time.UnixMilli(e.Created).In(time.Local).Format("01-02 15:04"), text, e.Author)
		}
	}
	return assistant.OK(b.String(), assistant.Tn(len(list), "{n} ticket", "{n} tickets", nil)), nil
}

func (s *A) listConversations(r *assistantRun, query string, limit int) (assistant.ToolResult, error) {
	list, err := s.LLM.ListChats(r.loc)
	if err != nil {
		return assistant.ToolResult{}, err
	}
	if limit <= 0 {
		limit = 20
	}
	q := strings.ToLower(strings.TrimSpace(query))
	var lines []string
	for _, c := range list {
		if c.ID == r.id || q != "" && !strings.Contains(strings.ToLower(c.Title), q) {
			continue
		}
		mode := c.Mode
		if mode == "" {
			mode = engine.Build
		}
		line := fmt.Sprintf("- %s %q · %s · %s", c.ID, c.Title, mode, time.UnixMilli(c.Updated).In(time.Local).Format("2006-01-02 15:04"))
		if c.Ticket != 0 {
			line += fmt.Sprintf(" · ticket #%d", c.Ticket)
		}
		if c.Parent != "" {
			line += fmt.Sprintf(" · sub-agent of %s (%s)", c.Parent, c.Status)
		}
		if state, _ := s.stateOf(c.ID); state != "idle" {
			line += " · " + state
		} else if c.Waiting {
			line += " · waiting for the user"
		} else if c.Failed {
			line += " · failed (agent_resume)"
		}
		lines = append(lines, line)
		if len(lines) == limit {
			break
		}
	}
	if len(lines) == 0 {
		return assistant.OK("No conversation.", assistant.Tn(0, "{n} conversation", "{n} conversations", nil)), nil
	}
	return assistant.OK(strings.Join(lines, "\n"), assistant.Tn(len(lines), "{n} conversation", "{n} conversations", nil)), nil
}

func (s *A) actionCard(r *assistantRun, a assistant.Args) (assistant.ToolResult, error) {
	card := &assistant.ActionCard{Kind: a.Str("kind"), Ticket: int64(a.Num("ticket")), Chat: a.Str("chat"), Label: strings.TrimSpace(a.Str("label")), Reason: strings.TrimSpace(a.Str("reason"))}
	known := false
	for _, k := range engine.ActionKinds {
		known = known || k == card.Kind
	}
	if !known {
		return assistant.ToolResult{}, assistant.Usagef("unknown kind %q (%s)", card.Kind, strings.Join(engine.ActionKinds, ", "))
	}
	if card.Label == "" {
		return assistant.ToolResult{}, assistant.Usagef("label is missing")
	}
	if card.Kind == "open_conversation" {
		if _, err := s.loadChat(r.loc, card.Chat); err != nil {
			return assistant.ToolResult{}, assistant.Usagef("unknown conversation %q (see list_conversations)", card.Chat)
		}
	} else if _, err := s.kb().Kanban.Get(kanbanLocOf(r.loc), card.Ticket); err != nil {
		return assistant.ToolResult{}, failf("ticket #%d not found", card.Ticket)
	}
	res := assistant.OKPlain("Card shown to the user; the action runs only if they click it.", card.Label)
	res.Card = card
	return res, nil
}

func (s *A) openConversation(r *assistantRun, a assistant.Args) (assistant.ToolResult, error) {
	var id, title string
	if id = strings.TrimSpace(a.Str("chat")); id != "" {
		c, err := s.loadChat(r.loc, id)
		if err != nil {
			return assistant.ToolResult{}, assistant.Usagef("unknown conversation %q (see list_conversations)", id)
		}
		title = c.Title
	} else {
		mode := a.Str("mode")
		switch mode {
		case "":
			mode = engine.Briefing
		case engine.Briefing, engine.Plan, engine.Build:
		default:
			return assistant.ToolResult{}, assistant.Usagef("mode must be briefing, plan or build")
		}
		r.mu.Lock()
		server, model := r.chat.Server, r.chat.Model
		var opts *assistant.Options
		if r.chat.Options != nil {
			o := *r.chat.Options
			opts = &o
		}
		r.mu.Unlock()
		c := &engine.Chat{ID: assistant.NewID(), Title: strings.TrimSpace(a.Str("title")), Server: server, Model: model, Mode: mode, Options: opts}
		if n := int64(a.Num("ticket")); n > 0 {
			if mode == engine.Build {
				return assistant.ToolResult{}, failf("to develop a ticket, propose action_card start_dev: it runs in the worktree of the ticket")
			}
			t, err := s.kb().Kanban.Get(kanbanLocOf(r.loc), n)
			if err != nil {
				return assistant.ToolResult{}, failf("ticket #%d not found", n)
			}
			c.Ticket = &engine.TicketLink{ID: n, Role: mode}
			if c.Title == "" {
				c.Title = fmt.Sprintf("#%d %s", n, t.Title)
			}
		}
		message := strings.TrimSpace(a.Str("message"))
		if a.Bool("send", false) && message != "" {
			c.Messages = []*engine.Message{{Role: "user", Content: assistant.String(message)}}
			s.startRun(r.loc, r.root, r.project, r.lang, c, 0)
		} else {
			c.Draft = message
			if c.Title == "" {
				c.Title = "Conversation"
			}
			if err := s.publishIdle(r.loc, r.root, c, 0); err != nil {
				return assistant.ToolResult{}, err
			}
		}
		id, title = c.ID, c.Title
	}
	// The windows showing this conversation move to the other one.
	s.emitAssistant(r.root, "assistant.open", map[string]string{"chat": id, "from": r.id})
	res := assistant.OK(fmt.Sprintf("Conversation %s %q opened for the user: they go on there. This conversation stays in their history.", id, title), assistant.T("opened: {title}", map[string]any{"title": title}))
	res.Opened = id
	return res, nil
}
