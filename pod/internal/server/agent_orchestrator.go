package server

import (
	"fmt"
	"strings"
	"time"

	"github.com/DrSmithFr/web-ide/pod/internal/agent"
	"github.com/DrSmithFr/web-ide/pod/internal/kanban"
)

// Tools of the Orchestrator mode (agent/orchestrator.go): what to do next, what was done,
// the conversations of the project, action cards the user clicks, and the conversation the
// user is moved into.

func (s *Server) orchestratorTool(r *agentRun, ref *runtimeRef, name string, a toolArgs) (toolResult, error) {
	switch name {
	case "kanban_next":
		return s.kanbanNext(r)
	case "kanban_history":
		return s.kanbanHistory(r, a.str("from"), a.str("to"))
	case "list_conversations":
		return s.listConversations(r, a.str("query"), a.num("limit"))
	case "action_card":
		return s.actionCard(r, a)
	case "open_conversation":
		return s.openConversation(r, a)
	}
	return toolResult{}, failf("unknown tool: %s", name)
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

func (s *Server) kanbanNext(r *agentRun) (toolResult, error) {
	list, err := s.Kanban.List(kanbanLocOf(r.loc))
	if err != nil {
		return toolResult{}, err
	}
	s.fillBlockers(r.ctx, s.kanbanGit(r), list)
	next := kanban.Next(list)
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
			if t.Status == kanban.Review {
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
	return ok(strings.TrimSpace(b.String()), agent.Tn(len(next), "{n} ticket can start", "{n} tickets can start", nil)), nil
}

// parseDay reads "YYYY-MM-DD" or "YYYY-MM-DD HH:MM" in local time.
func parseDay(s string) (time.Time, error) {
	s = strings.TrimSpace(s)
	for _, layout := range []string{"2006-01-02 15:04", "2006-01-02T15:04", "2006-01-02"} {
		if t, err := time.ParseInLocation(layout, s, time.Local); err == nil {
			return t, nil
		}
	}
	return time.Time{}, failf("invalid date %q: use YYYY-MM-DD or YYYY-MM-DD HH:MM", s)
}

func (s *Server) kanbanHistory(r *agentRun, fromArg, toArg string) (toolResult, error) {
	now := time.UnixMilli(s.Kanban.Now()).In(time.Local)
	y, mo, d := now.Date()
	from, to := time.Date(y, mo, d-1, 0, 0, 0, 0, time.Local), now
	var err error
	if fromArg != "" {
		if from, err = parseDay(fromArg); err != nil {
			return toolResult{}, err
		}
	}
	if toArg != "" {
		if to, err = parseDay(toArg); err != nil {
			return toolResult{}, err
		}
	}
	list, err := s.Kanban.History(kanbanLocOf(r.loc), from.UnixMilli(), to.UnixMilli())
	if err != nil {
		return toolResult{}, err
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
	return ok(b.String(), agent.Tn(len(list), "{n} ticket", "{n} tickets", nil)), nil
}

func (s *Server) listConversations(r *agentRun, query string, limit int) (toolResult, error) {
	list, err := s.LLM.ListChats(r.loc)
	if err != nil {
		return toolResult{}, err
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
			mode = agent.Build
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
		}
		lines = append(lines, line)
		if len(lines) == limit {
			break
		}
	}
	if len(lines) == 0 {
		return ok("No conversation.", agent.Tn(0, "{n} conversation", "{n} conversations", nil)), nil
	}
	return ok(strings.Join(lines, "\n"), agent.Tn(len(lines), "{n} conversation", "{n} conversations", nil)), nil
}

func (s *Server) actionCard(r *agentRun, a toolArgs) (toolResult, error) {
	card := &agent.ActionCard{Kind: a.str("kind"), Ticket: int64(a.num("ticket")), Chat: a.str("chat"), Label: strings.TrimSpace(a.str("label")), Reason: strings.TrimSpace(a.str("reason"))}
	known := false
	for _, k := range agent.ActionKinds {
		known = known || k == card.Kind
	}
	if !known {
		return toolResult{}, failf("unknown kind %q (%s)", card.Kind, strings.Join(agent.ActionKinds, ", "))
	}
	if card.Label == "" {
		return toolResult{}, failf("label is missing")
	}
	if card.Kind == "open_conversation" {
		if _, err := s.loadChat(r.loc, card.Chat); err != nil {
			return toolResult{}, failf("unknown conversation %q (see list_conversations)", card.Chat)
		}
	} else if _, err := s.Kanban.Get(kanbanLocOf(r.loc), card.Ticket); err != nil {
		return toolResult{}, failf("ticket #%d not found", card.Ticket)
	}
	res := okPlain("Card shown to the user; the action runs only if they click it.", card.Label)
	res.Card = card
	return res, nil
}

func (s *Server) openConversation(r *agentRun, a toolArgs) (toolResult, error) {
	var id, title string
	if id = strings.TrimSpace(a.str("chat")); id != "" {
		c, err := s.loadChat(r.loc, id)
		if err != nil {
			return toolResult{}, failf("unknown conversation %q (see list_conversations)", id)
		}
		title = c.Title
	} else {
		mode := a.str("mode")
		switch mode {
		case "":
			mode = agent.Briefing
		case agent.Briefing, agent.Plan, agent.Build:
		default:
			return toolResult{}, failf("mode must be briefing, plan or build")
		}
		r.mu.Lock()
		server, model := r.chat.Server, r.chat.Model
		var opts *agent.Options
		if r.chat.Options != nil {
			o := *r.chat.Options
			opts = &o
		}
		r.mu.Unlock()
		c := &agent.Chat{ID: newID(), Title: strings.TrimSpace(a.str("title")), Server: server, Model: model, Mode: mode, Options: opts}
		if n := int64(a.num("ticket")); n > 0 {
			if mode == agent.Build {
				return toolResult{}, failf("to develop a ticket, propose action_card start_dev: it runs in the worktree of the ticket")
			}
			t, err := s.Kanban.Get(kanbanLocOf(r.loc), n)
			if err != nil {
				return toolResult{}, failf("ticket #%d not found", n)
			}
			c.Ticket = &agent.TicketLink{ID: n, Role: mode}
			if c.Title == "" {
				c.Title = fmt.Sprintf("#%d %s", n, t.Title)
			}
		}
		message := strings.TrimSpace(a.str("message"))
		if a.boolean("send", false) && message != "" {
			c.Messages = []*agent.Message{{Role: "user", Content: agent.String(message)}}
			s.startRun(r.loc, r.root, r.project, r.lang, c, 0)
		} else {
			c.Draft = message
			if c.Title == "" {
				c.Title = "Conversation"
			}
			if err := s.publishIdle(r.loc, r.root, c, 0); err != nil {
				return toolResult{}, err
			}
		}
		id, title = c.ID, c.Title
	}
	// The windows showing this conversation move to the other one.
	s.emitAgent(r.root, "agent.open", map[string]string{"chat": id, "from": r.id})
	res := ok(fmt.Sprintf("Conversation %s %q opened for the user: they go on there. This conversation stays in their history.", id, title), agent.T("opened: {title}", map[string]any{"title": title}))
	res.Opened = id
	return res, nil
}
