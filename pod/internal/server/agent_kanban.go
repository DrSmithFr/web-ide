package server

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/DrSmithFr/web-ide/pod/internal/agent"
	"github.com/DrSmithFr/web-ide/pod/internal/kanban"
	"github.com/DrSmithFr/web-ide/pod/internal/llm"
)

// Kanban tools of the assistant (docs/kanban.md): every conversation reads the tickets and
// creates some (in Briefing mode the conversation is linked to the first one); a conversation
// linked to a ticket changes that ticket only.

func kanbanLocOf(loc llm.ChatLocation) kanban.Location {
	return kanban.Location{Project: loc.Project, IdeDir: loc.IdeDir}
}

// kanbanGit is the git side of the kanban of a run (blockers need git).
func (s *Server) kanbanGit(r *agentRun) gctx {
	root, _ := s.Projects.Get(r.root)
	k := gctx{loc: kanbanLocOf(r.loc), root: root}
	if root == nil {
		return k
	}
	if rt, err := s.agentRuntime(r.root); err == nil {
		k.rt, k.git = rt.rt, kanban.Git{Run: rt.rt.Runner, Root: root.Path}
	}
	return k
}

// ticketPrompt is the ticket part of the system prompt of a conversation linked to a ticket.
func (s *Server) ticketPrompt(r *agentRun, link *agent.TicketLink) string {
	t, err := s.Kanban.Get(kanbanLocOf(r.loc), link.ID)
	if err != nil {
		return fmt.Sprintf("# Linked ticket\nTicket #%d cannot be found (%s).", link.ID, err)
	}
	return agent.TicketPrompt(t.ID, link.Role, t.Branch, t.Parent, link.Feedback, kanban.Markdown(t))
}

func tooLongDescription(text string) *toolResult {
	n := len([]rune(strings.TrimSpace(text)))
	if n <= agent.MaxDescription {
		return nil
	}
	return &toolResult{
		Content: fmt.Sprintf("Error: description too long (%d characters, %d max). Keep the context, the need and the acceptance criteria, in short sentences; decisions go in notes (kanban_add_note), the approach in the plan.", n, agent.MaxDescription),
		Summary: agent.T("description too long", nil).Raw(),
		Status:  "error",
	}
}

func (a toolArgs) strs(k string) []string {
	var out []string
	_ = json.Unmarshal(a[k], &out)
	return out
}

func (a toolArgs) optStr(k string) *string {
	if !a.has(k) {
		return nil
	}
	v := a.str(k)
	if v == "" {
		return nil
	}
	return &v
}

// links reads the lineage fields of a call: parent (0 takes it out) and depends_on.
func (a toolArgs) links(p *kanban.Patch) {
	if a.has("parent") {
		v := int64(a.num("parent"))
		p.Parent = &v
	}
	if a.has("depends_on") {
		var deps []int64
		_ = json.Unmarshal(a["depends_on"], &deps)
		if deps == nil {
			deps = []int64{}
		}
		p.DependsOn = &deps
	}
}

// attachDoodles joins the pages of the board not attached yet to a ticket, as PNG files: the
// doodles of the user (the page stores the PNG of a doodle when it sends it) and the pages
// drawn by the model.
func (s *Server) attachDoodles(r *agentRun, id int64) int {
	t, err := s.Kanban.Get(kanbanLocOf(r.loc), id)
	if err != nil {
		return 0
	}
	existing := map[string]bool{}
	for _, a := range t.Attachments {
		existing[a.Name] = true
	}
	r.mu.Lock()
	pages := agent.Pages(r.chat.Messages)
	r.mu.Unlock()
	seen := map[string]int{}
	added := 0
	for _, p := range pages {
		if p.PNG == "" {
			continue
		}
		name := p.Name
		if p.Kind == "model" {
			name = fmt.Sprintf("Page %d %s", p.Number, p.Name)
		} else if seen[p.Name]++; seen[p.Name] > 1 {
			// Doodle 1 of two messages: "Doodle 1.png", "Doodle 1 (2).png".
			name = fmt.Sprintf("%s (%d)", p.Name, seen[p.Name])
		}
		name += ".png"
		if existing[name] {
			continue
		}
		data, err := base64.StdEncoding.DecodeString(p.PNG[strings.Index(p.PNG, ",")+1:])
		if err != nil {
			continue
		}
		if _, err := s.Kanban.AddAttachment(kanbanLocOf(r.loc), id, name, "image/png", data); err == nil {
			added++
		}
	}
	return added
}

func doodlesNote(n int) string {
	if n == 0 {
		return ""
	}
	s := ""
	if n > 1 {
		s = "s"
	}
	return fmt.Sprintf(" %d doodle%s of the conversation attached to it as PNG files.", n, s)
}

func (s *Server) kanbanTool(r *agentRun, name string, a toolArgs, mode string) toolResult {
	res, err := s.kanbanCall(r, name, a, mode)
	if err != nil {
		return fail(r, err)
	}
	return res
}

func (s *Server) kanbanCall(r *agentRun, name string, a toolArgs, mode string) (toolResult, error) {
	loc := kanbanLocOf(r.loc)
	changed := func(id int64) { s.emitKanban(r.root, id, nil) }
	switch name {
	case "kanban_list":
		list, err := s.Kanban.List(loc)
		if err != nil {
			return toolResult{}, err
		}
		s.fillBlockers(r.ctx, s.kanbanGit(r), list)
		status, q := a.str("status"), strings.ToLower(a.str("query"))
		var lines []string
		for _, t := range list {
			if status != "" && t.Status != status || q != "" && !strings.Contains(strings.ToLower(t.Title), q) {
				continue
			}
			line := fmt.Sprintf("#%d [%s] (%s) %s", t.ID, kanban.StatusNames[t.Status], kanban.PriorityNames[t.Priority], t.Title)
			if t.Size != "" {
				line += " · size " + kanban.SizeNames[t.Size]
			}
			if t.Complexity != "" {
				line += " · complexity " + kanban.ComplexityNames[t.Complexity]
			}
			if t.Goals > 0 {
				line += fmt.Sprintf(" · goals %d/%d", t.GoalsDone, t.Goals)
			}
			if t.FeedbackOpen > 0 {
				line += fmt.Sprintf(" · open feedback %d", t.FeedbackOpen)
			}
			if t.Parent != 0 {
				line += fmt.Sprintf(" · child of #%d", t.Parent)
			}
			if len(t.Blockers) > 0 {
				line += " · blocked by " + kanban.BlockersText(t.Blockers)
			}
			lines = append(lines, line)
		}
		if len(lines) == 0 {
			return ok("No ticket.", agent.Tn(0, "{n} ticket", "{n} tickets", nil)), nil
		}
		return ok(strings.Join(lines, "\n"), agent.Tn(len(lines), "{n} ticket", "{n} tickets", nil)), nil
	case "kanban_get":
		t, err := s.Kanban.Get(loc, int64(a.num("id")))
		if err != nil {
			return toolResult{}, err
		}
		if kanban.Startable(t.Status) && (t.Parent != 0 || len(t.DependsOn) > 0) {
			t.Blockers, _ = s.ticketBlockers(r.ctx, s.kanbanGit(r), t.ID)
		}
		return okPlain(kanban.Markdown(t), fmt.Sprintf("#%d %s", t.ID, t.Title)), nil
	case "kanban_create":
		title := strings.TrimSpace(a.str("title"))
		if title == "" {
			return toolResult{}, usagef("title is missing")
		}
		if long := tooLongDescription(a.str("description")); long != nil {
			return *long, nil
		}
		p := kanban.Patch{Title: &title, Description: a.optStr("description"), Priority: a.optStr("priority"), AddFiles: a.strs("files")}
		a.links(&p)
		id, err := s.Kanban.Create(loc, p, kanban.ByModel)
		if err != nil {
			return toolResult{}, err
		}
		changed(id)
		done := fmt.Sprintf("Ticket #%d created in the backlog (status New).%s", id, doodlesNote(s.attachDoodles(r, id)))
		sum := agent.T("#{id} created", map[string]any{"id": id})
		if mode != agent.Briefing {
			return ok(done, sum), nil
		}
		// Briefing: the ticket lists this conversation; the first one created is linked to it.
		r.mu.Lock()
		defer r.mu.Unlock()
		if r.chat.Ticket == nil {
			r.chat.Ticket = &agent.TicketLink{ID: id, Role: "briefing"}
			s.publish(r, -1)
			return ok(done+" This conversation is now linked to it: kanban_update and kanban_add_note refine it.", sum), nil
		}
		_ = s.Kanban.LinkChat(loc, id, r.chat.ID, "briefing", r.chat.Title)
		return ok(fmt.Sprintf("%s It lists this conversation as its briefing; this conversation stays linked to ticket #%d.", done, r.chat.Ticket.ID), sum), nil
	}
	r.mu.Lock()
	link := r.chat.Ticket
	chatID := r.chat.ID
	r.mu.Unlock()
	if link == nil {
		return toolResult{Content: "Error: this conversation is not linked to a ticket; only kanban_list, kanban_get and kanban_create are available.", Summary: agent.T("no linked ticket", nil).Raw(), Status: "error"}, nil
	}
	id := link.ID
	defer changed(id)
	switch name {
	case "kanban_update":
		if long := tooLongDescription(a.str("description")); long != nil {
			return *long, nil
		}
		p := kanban.Patch{Title: a.optStr("title"), Description: a.optStr("description"), Priority: a.optStr("priority"), TestSummary: a.optStr("test_summary"),
			Size: a.optStr("size"), Complexity: a.optStr("complexity"), AddFiles: a.strs("add_files"), RemoveFiles: a.strs("remove_files")}
		a.links(&p)
		if err := s.Kanban.Update(loc, id, p, kanban.ByModel); err != nil {
			return toolResult{}, err
		}
		return ok(fmt.Sprintf("Ticket #%d updated.%s", id, doodlesNote(s.attachDoodles(r, id))), agent.T("#{id} updated", map[string]any{"id": id})), nil
	case "kanban_add_note":
		text := strings.TrimSpace(a.str("text"))
		if n := len([]rune(text)); n > agent.MaxNote {
			return toolResult{Content: fmt.Sprintf("Error: note too long (%d characters, %d max). Keep only what is worth remembering, in a few lines; the details belong in the description (kanban_update) or the plan.", n, agent.MaxNote),
				Summary: agent.T("note too long", nil).Raw(), Status: "error"}, nil
		}
		if err := s.Kanban.AddNote(loc, id, text, kanban.ByModel, chatID); err != nil {
			return toolResult{}, err
		}
		return ok("Note added.", agent.T("note added", nil)), nil
	case "kanban_set_plan":
		var goals []kanban.GoalInput
		_ = json.Unmarshal(a["goals"], &goals)
		var kept []kanban.GoalInput
		for _, g := range goals {
			if strings.TrimSpace(g.Title) != "" {
				kept = append(kept, g)
			}
		}
		plan := a.str("plan")
		if strings.TrimSpace(plan) == "" {
			return toolResult{}, usagef("empty plan")
		}
		size := a.str("size")
		if size == "" {
			return toolResult{}, usagef("size is required: s, m, l or xl")
		}
		complexity := a.str("complexity")
		if complexity == "" {
			return toolResult{}, usagef("complexity is required: low, medium or high")
		}
		if err := s.Kanban.Update(loc, id, kanban.Patch{Size: &size, Complexity: &complexity, PlanSize: true}, kanban.ByModel); err != nil {
			return toolResult{}, err
		}
		if err := s.Kanban.SetPlan(loc, id, plan, kept, kanban.ByModel); err != nil {
			return toolResult{}, err
		}
		t, err := s.Kanban.Get(loc, id)
		if err != nil {
			return toolResult{}, err
		}
		moved := ""
		if t.Status == kanban.Todo {
			moved = ` The ticket is now "To do".`
		}
		var lines []string
		for _, g := range t.GoalList {
			lines = append(lines, fmt.Sprintf("- (id %d) %s", g.ID, g.Text))
		}
		return ok(fmt.Sprintf("Plan saved with %d goal(s):%s\n%s", len(kept), moved, strings.Join(lines, "\n")), agent.Tn(len(kept), "plan · {n} goal", "plan · {n} goals", nil)), nil
	case "kanban_goal":
		switch action := a.str("action"); action {
		case "add":
			title := a.str("title")
			if title == "" {
				title = a.str("text")
			}
			if err := s.Kanban.Goal(loc, id, kanban.GoalOp{Op: "add", Text: title, Description: a.str("description"), Source: "plan"}); err != nil {
				return toolResult{}, err
			}
			t, _ := s.Kanban.Get(loc, id)
			gid := int64(0)
			if t != nil && len(t.GoalList) > 0 {
				gid = t.GoalList[len(t.GoalList)-1].ID
			}
			return ok(fmt.Sprintf("Goal added (id %d).", gid), agent.T("goal added", nil)), nil
		case "check", "uncheck":
			gid := int64(a.num("id"))
			if err := s.Kanban.Goal(loc, id, kanban.GoalOp{Op: "check", ID: gid, Done: action == "check"}); err != nil {
				return toolResult{}, err
			}
			t, err := s.Kanban.Get(loc, id)
			if err != nil {
				return toolResult{}, err
			}
			text, left := fmt.Sprint(gid), 0
			for _, g := range t.GoalList {
				if g.ID == gid {
					text = g.Text
				}
				if !g.Done {
					left++
				}
			}
			mark, word := "☑", "checked"
			if action == "uncheck" {
				mark, word = "☐", "unchecked"
			}
			return okPlain(fmt.Sprintf("Goal %s: %s. %d goal(s) left.", word, text, left), mark+" "+text), nil
		case "edit":
			if err := s.Kanban.GoalEdit(loc, id, int64(a.num("id")), a.str("title"), a.str("description")); err != nil {
				return toolResult{}, err
			}
			return ok("Goal edited.", agent.T("goal edited", nil)), nil
		case "delete":
			if err := s.Kanban.Goal(loc, id, kanban.GoalOp{Op: "delete", ID: int64(a.num("id"))}); err != nil {
				return toolResult{}, err
			}
			return ok("Goal deleted.", agent.T("goal deleted", nil)), nil
		default:
			return toolResult{}, usagef("unknown action: %s", action)
		}
	case "kanban_feedback":
		action := a.str("action")
		if action == "add" {
			if a.str("complexity") == "" {
				return toolResult{}, usagef("complexity is required: low, medium or high")
			}
			fid, err := s.Kanban.Feedback(loc, id, kanban.FeedbackOp{Op: "add", Kind: a.str("kind"), Text: a.str("text"), Complexity: a.str("complexity"), ChatID: chatID}, kanban.ByModel)
			if err != nil {
				return toolResult{}, err
			}
			return ok(fmt.Sprintf("Feedback added (id %d).", fid), agent.T("feedback added", nil)), nil
		}
		if action != "done" && action != "reopen" {
			return toolResult{}, usagef("unknown action: %s", action)
		}
		fid := int64(a.num("id"))
		if _, err := s.Kanban.Feedback(loc, id, kanban.FeedbackOp{Op: "check", ID: fid, Done: action == "done"}, kanban.ByModel); err != nil {
			return toolResult{}, err
		}
		t, err := s.Kanban.Get(loc, id)
		if err != nil {
			return toolResult{}, err
		}
		text, left := fmt.Sprint(fid), 0
		for _, f := range t.FeedbackList {
			if f.ID == fid {
				text = f.Text
			}
			if !f.Done {
				left++
			}
		}
		short := []rune(text)
		mark, word := "☑", "marked done"
		if action == "reopen" {
			mark, word = "☐", "reopened"
		}
		return okPlain(fmt.Sprintf("Feedback %s: %s. %d open feedback left.", word, string(short[:min(80, len(short))]), left), mark+" "+string(short[:min(60, len(short))])), nil
	case "kanban_move":
		status := a.str("status")
		if status == kanban.Review {
			summary := strings.TrimSpace(a.str("test_summary"))
			if summary == "" {
				return toolResult{}, failf(`test_summary is required to move to "To test"`)
			}
			if err := s.Kanban.Update(loc, id, kanban.Patch{TestSummary: &summary}, kanban.ByModel); err != nil {
				return toolResult{}, err
			}
		}
		if err := s.Kanban.Move(loc, id, status, kanban.ByModel, a.str("comment")); err != nil {
			return toolResult{}, err
		}
		t, err := s.Kanban.Get(loc, id)
		if err != nil {
			return toolResult{}, err
		}
		return ok(fmt.Sprintf(`Ticket #%d moved to "%s".`, id, kanban.StatusNames[t.Status]), agent.T(kanban.StatusNames[t.Status], nil).With("→ ", "")), nil
	}
	return toolResult{}, usagef("unknown tool: %s", name)
}

func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }
