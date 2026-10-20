package ai

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	kbp "github.com/DrSmithFr/web-ide/plugins/kanban"
	"strings"

	"github.com/DrSmithFr/web-ide/pod/assistant"
	"github.com/DrSmithFr/web-ide/pod/kanban"
	"github.com/DrSmithFr/web-ide/pod/llm"
)

// Kanban tools of the assistant (docs/design/kanban.md): every conversation reads the tickets and
// creates some (in Briefing mode the conversation is linked to the first one); a conversation
// linked to a ticket changes that ticket only.

func kanbanLocOf(loc llm.ChatLocation) kanban.Location {
	return kanban.Location{Project: loc.Project, IdeDir: loc.IdeDir}
}

// kanbanGit is the git side of the kanban of a run (blockers need git).
func (s *A) kanbanGit(r *assistantRun) kbp.GitCtx {
	root, _ := s.Projects.Get(r.root)
	if root != nil {
		if rt, err := s.assistantRuntime(r.root); err == nil {
			return kbp.NewGitCtx(kanbanLocOf(r.loc), root, rt.rt, kanban.Git{Run: rt.rt.Runner, Root: root.Path})
		}
	}
	return kbp.NewGitCtx(kanbanLocOf(r.loc), root, nil, kanban.Git{})
}

// ticketPrompt is the ticket part of the system prompt of a conversation linked to a ticket.
func (s *A) ticketPrompt(r *assistantRun, link *assistant.TicketLink) string {
	t, err := s.Kanban.Get(kanbanLocOf(r.loc), link.ID)
	if err != nil {
		return fmt.Sprintf("# Linked ticket\nTicket #%d cannot be found (%s).", link.ID, err)
	}
	return assistant.TicketPrompt(t.ID, link.Role, t.Branch, t.Parent, link.Feedback, kanban.Markdown(t))
}

func tooLongDescription(text string) *assistant.ToolResult {
	n := len([]rune(strings.TrimSpace(text)))
	if n <= assistant.MaxDescription {
		return nil
	}
	return &assistant.ToolResult{
		Content: fmt.Sprintf("Error: description too long (%d characters, %d max). Keep the context, the need and the acceptance criteria, in short sentences; decisions go in notes (kanban_add_note), the approach in the plan.", n, assistant.MaxDescription),
		Summary: assistant.T("description too long", nil).Raw(),
		Status:  "error",
	}
}

// links sets the lineage, the milestone and the objective given in a on p.
func links(a assistant.Args, p *kanban.Patch) {
	if a.Has("parent") {
		v := int64(a.Num("parent"))
		p.Parent = &v
	}
	if a.Has("depends_on") {
		var deps []int64
		_ = json.Unmarshal(a["depends_on"], &deps)
		if deps == nil {
			deps = []int64{}
		}
		p.DependsOn = &deps
	}
	if a.Has("milestone") {
		v := int64(a.Num("milestone"))
		p.Milestone = &v
	}
	if a.Has("objective") {
		v := int64(a.Num("objective"))
		p.Objective = &v
	}
}

// attachDoodles joins the pages of the board not attached yet to a ticket, as PNG files: the
// doodles of the user (the page stores the PNG of a doodle when it sends it) and the pages
// drawn by the model.
func (s *A) attachDoodles(r *assistantRun, id int64) int {
	t, err := s.Kanban.Get(kanbanLocOf(r.loc), id)
	if err != nil {
		return 0
	}
	existing := map[string]bool{}
	for _, a := range t.Attachments {
		existing[a.Name] = true
	}
	r.mu.Lock()
	pages := assistant.Pages(r.chat.Messages)
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

func (s *A) kanbanTool(r *assistantRun, name string, a assistant.Args, mode string) assistant.ToolResult {
	res, err := s.kanbanCall(r, name, a, mode)
	if err != nil {
		return fail(r, err)
	}
	return res
}

func (s *A) kanbanCall(r *assistantRun, name string, a assistant.Args, mode string) (assistant.ToolResult, error) {
	loc := kanbanLocOf(r.loc)
	changed := func(id int64) { s.kb().Emit(r.root, id) }
	switch name {
	case "kanban_list":
		list, err := s.Kanban.List(loc)
		if err != nil {
			return assistant.ToolResult{}, err
		}
		s.kb().FillBlockers(r.ctx, s.kanbanGit(r), list)
		status, q := kanban.NormStatus(a.Str("status")), strings.ToLower(a.Str("query"))
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
				line += fmt.Sprintf(" · step of #%d", t.Parent)
			}
			if len(t.Blockers) > 0 {
				line += " · blocked by " + kanban.BlockersText(t.Blockers)
			}
			lines = append(lines, line)
		}
		if len(lines) == 0 {
			return assistant.OK("No ticket.", assistant.Tn(0, "{n} ticket", "{n} tickets", nil)), nil
		}
		return assistant.OK(strings.Join(lines, "\n"), assistant.Tn(len(lines), "{n} ticket", "{n} tickets", nil)), nil
	case "kanban_get":
		t, err := s.Kanban.Get(loc, int64(a.Num("id")))
		if err != nil {
			return assistant.ToolResult{}, err
		}
		if kanban.Startable(t.Status) && (t.Parent != 0 || len(t.DependsOn) > 0) {
			t.Blockers, _ = s.kb().TicketBlockers(r.ctx, s.kanbanGit(r), t.ID)
		}
		return assistant.OKPlain(kanban.Markdown(t), fmt.Sprintf("#%d %s", t.ID, t.Title)), nil
	case "kanban_create":
		title := strings.TrimSpace(a.Str("title"))
		if title == "" {
			return assistant.ToolResult{}, assistant.Usagef("title is missing")
		}
		if long := tooLongDescription(a.Str("description")); long != nil {
			return *long, nil
		}
		p := kanban.Patch{Title: &title, Description: a.OptStr("description"), Priority: a.OptStr("priority"), AddFiles: a.Strs("files")}
		links(a, &p)
		id, err := s.Kanban.Create(loc, p, kanban.ByModel)
		if err != nil {
			return assistant.ToolResult{}, err
		}
		changed(id)
		done := fmt.Sprintf("Ticket #%d created in the backlog (status New).%s", id, doodlesNote(s.attachDoodles(r, id)))
		sum := assistant.T("#{id} created", map[string]any{"id": id})
		if mode != assistant.Briefing {
			return assistant.OK(done, sum), nil
		}
		// Briefing: the ticket lists this conversation; the first one created is linked to it.
		r.mu.Lock()
		defer r.mu.Unlock()
		if r.chat.Ticket == nil {
			r.chat.Ticket = &assistant.TicketLink{ID: id, Role: "briefing"}
			s.publish(r, -1)
			return assistant.OK(done+" This conversation is now linked to it: kanban_update and kanban_add_note refine it.", sum), nil
		}
		_ = s.Kanban.LinkChat(loc, id, r.chat.ID, "briefing", r.chat.Title)
		return assistant.OK(fmt.Sprintf("%s It lists this conversation as its briefing; this conversation stays linked to ticket #%d.", done, r.chat.Ticket.ID), sum), nil
	}
	r.mu.Lock()
	link := r.chat.Ticket
	chatID := r.chat.ID
	chatTitle := r.chat.Title
	r.mu.Unlock()
	if name == "kanban_milestone" {
		return s.assistantMilestone(r, loc, a, mode, chatID, chatTitle)
	}
	if name == "kanban_objective" {
		var args kbp.ObjectiveArgs
		raw, _ := json.Marshal(a)
		if err := json.Unmarshal(raw, &args); err != nil {
			return assistant.ToolResult{}, assistant.Usagef("invalid arguments: %v", err)
		}
		out, err := s.kb().ObjectiveTool(r.root, loc, args, kanban.ByModel, chatID)
		if err != nil {
			return assistant.ToolResult{}, err
		}
		return assistant.OK(out, assistant.T("objective", nil)), nil
	}
	if link == nil {
		return assistant.ToolResult{Content: "Error: this conversation is not linked to a ticket; only kanban_list, kanban_get and kanban_create are available.", Summary: assistant.T("no linked ticket", nil).Raw(), Status: "error"}, nil
	}
	id := link.ID
	defer changed(id)
	switch name {
	case "kanban_update":
		if long := tooLongDescription(a.Str("description")); long != nil {
			return *long, nil
		}
		p := kanban.Patch{Title: a.OptStr("title"), Description: a.OptStr("description"), Priority: a.OptStr("priority"), TestSummary: a.OptStr("test_summary"),
			Size: a.OptStr("size"), Complexity: a.OptStr("complexity"), AddFiles: a.Strs("add_files"), RemoveFiles: a.Strs("remove_files")}
		links(a, &p)
		if err := s.Kanban.Update(loc, id, p, kanban.ByModel); err != nil {
			return assistant.ToolResult{}, err
		}
		return assistant.OK(fmt.Sprintf("Ticket #%d updated.%s", id, doodlesNote(s.attachDoodles(r, id))), assistant.T("#{id} updated", map[string]any{"id": id})), nil
	case "kanban_add_note":
		text := strings.TrimSpace(a.Str("text"))
		if n := len([]rune(text)); n > assistant.MaxNote {
			return assistant.ToolResult{Content: fmt.Sprintf("Error: note too long (%d characters, %d max). Keep only what is worth remembering, in a few lines; the details belong in the description (kanban_update) or the plan.", n, assistant.MaxNote),
				Summary: assistant.T("note too long", nil).Raw(), Status: "error"}, nil
		}
		if err := s.Kanban.AddNote(loc, id, text, kanban.ByModel, chatID); err != nil {
			return assistant.ToolResult{}, err
		}
		return assistant.OK("Note added.", assistant.T("note added", nil)), nil
	case "kanban_set_plan":
		var goals []kanban.GoalInput
		_ = json.Unmarshal(a["goals"], &goals)
		var kept []kanban.GoalInput
		for _, g := range goals {
			if strings.TrimSpace(g.Title) != "" {
				kept = append(kept, g)
			}
		}
		plan := a.Str("plan")
		if strings.TrimSpace(plan) == "" {
			return assistant.ToolResult{}, assistant.Usagef("empty plan")
		}
		size := a.Str("size")
		if size == "" {
			return assistant.ToolResult{}, assistant.Usagef("size is required: s, m, l or xl")
		}
		complexity := a.Str("complexity")
		if complexity == "" {
			return assistant.ToolResult{}, assistant.Usagef("complexity is required: low, medium or high")
		}
		if err := s.Kanban.Update(loc, id, kanban.Patch{Size: &size, Complexity: &complexity, PlanSize: true}, kanban.ByModel); err != nil {
			return assistant.ToolResult{}, err
		}
		if err := s.Kanban.SetPlan(loc, id, plan, kept, kanban.ByModel); err != nil {
			return assistant.ToolResult{}, err
		}
		t, err := s.Kanban.Get(loc, id)
		if err != nil {
			return assistant.ToolResult{}, err
		}
		moved := ""
		if t.Status == kanban.Todo {
			moved = ` The ticket is now "To do".`
		}
		var lines []string
		for _, g := range t.GoalList {
			lines = append(lines, fmt.Sprintf("- (id %d) %s", g.ID, g.Text))
		}
		return assistant.OK(fmt.Sprintf("Plan saved with %d goal(s):%s\n%s", len(kept), moved, strings.Join(lines, "\n")), assistant.Tn(len(kept), "plan · {n} goal", "plan · {n} goals", nil)), nil
	case "kanban_goal":
		switch action := a.Str("action"); action {
		case "add":
			title := a.Str("title")
			if title == "" {
				title = a.Str("text")
			}
			if err := s.Kanban.Goal(loc, id, kanban.GoalOp{Op: "add", Text: title, Description: a.Str("description"), Source: "plan"}); err != nil {
				return assistant.ToolResult{}, err
			}
			t, _ := s.Kanban.Get(loc, id)
			gid := int64(0)
			if t != nil && len(t.GoalList) > 0 {
				gid = t.GoalList[len(t.GoalList)-1].ID
			}
			return assistant.OK(fmt.Sprintf("Goal added (id %d).", gid), assistant.T("goal added", nil)), nil
		case "check", "uncheck":
			gid := int64(a.Num("id"))
			if err := s.Kanban.Goal(loc, id, kanban.GoalOp{Op: "check", ID: gid, Done: action == "check"}); err != nil {
				return assistant.ToolResult{}, err
			}
			t, err := s.Kanban.Get(loc, id)
			if err != nil {
				return assistant.ToolResult{}, err
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
			return assistant.OKPlain(fmt.Sprintf("Goal %s: %s. %d goal(s) left.", word, text, left), mark+" "+text), nil
		case "edit":
			if err := s.Kanban.GoalEdit(loc, id, int64(a.Num("id")), a.Str("title"), a.Str("description")); err != nil {
				return assistant.ToolResult{}, err
			}
			return assistant.OK("Goal edited.", assistant.T("goal edited", nil)), nil
		case "delete":
			if err := s.Kanban.Goal(loc, id, kanban.GoalOp{Op: "delete", ID: int64(a.Num("id"))}); err != nil {
				return assistant.ToolResult{}, err
			}
			return assistant.OK("Goal deleted.", assistant.T("goal deleted", nil)), nil
		default:
			return assistant.ToolResult{}, assistant.Usagef("unknown action: %s", action)
		}
	case "kanban_feedback":
		action := a.Str("action")
		if action == "add" {
			if a.Str("complexity") == "" {
				return assistant.ToolResult{}, assistant.Usagef("complexity is required: low, medium or high")
			}
			fid, err := s.Kanban.Feedback(loc, id, kanban.FeedbackOp{Op: "add", Kind: a.Str("kind"), Text: a.Str("text"), Complexity: a.Str("complexity"), ChatID: chatID}, kanban.ByModel)
			if err != nil {
				return assistant.ToolResult{}, err
			}
			s.pod.NotifyFeedback(loc, id, a.Str("text"))
			return assistant.OK(fmt.Sprintf("Feedback added (id %d).", fid), assistant.T("feedback added", nil)), nil
		}
		if action != "done" && action != "reopen" {
			return assistant.ToolResult{}, assistant.Usagef("unknown action: %s", action)
		}
		fid := int64(a.Num("id"))
		if _, err := s.Kanban.Feedback(loc, id, kanban.FeedbackOp{Op: "check", ID: fid, Done: action == "done"}, kanban.ByModel); err != nil {
			return assistant.ToolResult{}, err
		}
		t, err := s.Kanban.Get(loc, id)
		if err != nil {
			return assistant.ToolResult{}, err
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
		return assistant.OKPlain(fmt.Sprintf("Feedback %s: %s. %d open feedback left.", word, string(short[:min(80, len(short))]), left), mark+" "+string(short[:min(60, len(short))])), nil
	case "kanban_move":
		status := kanban.NormStatus(a.Str("status"))
		if status == kanban.ToTest {
			summary := strings.TrimSpace(a.Str("test_summary"))
			if summary == "" {
				return assistant.ToolResult{}, failf(`test_summary is required to move to "To test"`)
			}
			if err := s.Kanban.Update(loc, id, kanban.Patch{TestSummary: &summary}, kanban.ByModel); err != nil {
				return assistant.ToolResult{}, err
			}
		}
		if err := s.Kanban.Move(loc, id, status, kanban.ByModel, a.Str("comment")); err != nil {
			return assistant.ToolResult{}, err
		}
		t, err := s.Kanban.Get(loc, id)
		if err != nil {
			return assistant.ToolResult{}, err
		}
		return assistant.OK(fmt.Sprintf(`Ticket #%d moved to "%s".`, id, kanban.StatusNames[t.Status]), assistant.T(kanban.StatusNames[t.Status], nil).With("→ ", "")), nil
	}
	return assistant.ToolResult{}, assistant.Usagef("unknown tool: %s", name)
}

func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

// assistantMilestone runs kanban_milestone: every conversation reads the milestones and creates
// some (in Briefing mode it is linked to the ones it creates); it changes only the milestones
// it is linked to.
func (s *A) assistantMilestone(r *assistantRun, loc kanban.Location, a assistant.Args, mode, chatID, chatTitle string) (assistant.ToolResult, error) {
	var args kbp.MilestoneArgs
	raw, _ := json.Marshal(a)
	if err := json.Unmarshal(raw, &args); err != nil {
		return assistant.ToolResult{}, assistant.Usagef("invalid arguments: %v", err)
	}
	switch args.Action {
	case "list", "get", "create":
	default:
		if !s.Kanban.MilestoneLinked(loc, args.ID, chatID) {
			return assistant.ToolResult{}, failf("this conversation is not linked to milestone %d: only list, get and create are available on it", args.ID)
		}
	}
	out, created, err := s.kb().MilestoneTool(r.root, loc, args, kanban.ByModel, chatID)
	if err != nil {
		return assistant.ToolResult{}, err
	}
	if created != 0 && mode == assistant.Briefing {
		if err := s.Kanban.LinkMilestoneChat(loc, created, chatID, "briefing", chatTitle); err == nil {
			out += " This conversation is linked to it: it can change it."
		}
	}
	return assistant.OK(out, assistant.T("milestone", nil)), nil
}
