package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"

	"github.com/DrSmithFr/web-ide/pod/internal/kanban"
	"github.com/DrSmithFr/web-ide/pod/internal/llm"
	"github.com/DrSmithFr/web-ide/pod/internal/projects"
	"github.com/DrSmithFr/web-ide/pod/internal/sshx"
)

// Tools of the MCP endpoint: those of the assistant (web/src/llm/kanbanTools.ts) with the
// ticket given explicitly, plus kanban_start and kanban_conversation. Texts read by the
// model are English.

type mcpTool struct {
	name, description string
	props             map[string]any
	required          []string
	run               func(ctx context.Context, s *Server, args json.RawMessage) (string, error)
}

func (t mcpTool) schema() map[string]any {
	props := map[string]any{"cwd": str("Your working directory (absolute): it selects the project")}
	for k, v := range t.props {
		props[k] = v
	}
	return map[string]any{"type": "object", "properties": props, "required": append([]string{"cwd"}, t.required...)}
}

func str(description string) map[string]any {
	return map[string]any{"type": "string", "description": description}
}

func strList(description string) map[string]any {
	return map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": description}
}

func enum(description string, values ...string) map[string]any {
	return map[string]any{"type": "string", "enum": values, "description": description}
}

var ticketID = map[string]any{"type": "integer", "description": "Ticket number (default: the ticket of the worktree you are in)"}

// mcpScope is the kanban a call works on, found from the working directory of the client.
type mcpScope struct {
	here   *projects.Project // project of the working directory (a worktree has its own)
	root   *projects.Project
	loc    kanban.Location
	ticket int64 // ticket of the worktree the client works in, 0 elsewhere
}

func (s *Server) mcpScope(cwd string) (mcpScope, error) {
	if !filepath.IsAbs(cwd) {
		return mcpScope{}, fmt.Errorf("cwd must be an absolute path, got %q", cwd)
	}
	p, ok := s.Projects.At(cwd)
	if !ok {
		return mcpScope{}, fmt.Errorf("no project of the IDE holds %s: open the folder as a project in the IDE first", cwd)
	}
	root, err := s.kanbanProject(p.ID)
	if err != nil {
		return mcpScope{}, err
	}
	return mcpScope{here: p, root: root, loc: kanbanLoc(root), ticket: p.Ticket}, nil
}

// projectURL and ticketURL are the pages of the IDE, at the address the user opens it.
func (s *Server) projectURL(id string) string {
	return s.publicURL() + "/project/" + url.PathEscape(id)
}

func (s *Server) ticketURL(sc mcpScope, id int64) string {
	return fmt.Sprintf("%s?ticket=%d", s.projectURL(sc.root.ID), id)
}

// id is the ticket of a call: the one given, else the one of the worktree.
func (sc mcpScope) id(given int64) (int64, error) {
	if given > 0 {
		return given, nil
	}
	if sc.ticket > 0 {
		return sc.ticket, nil
	}
	return 0, fmt.Errorf("id is missing (you are not in the worktree of a ticket)")
}

// git opens the runtime of the project for the git side of the tickets.
func (s *Server) mcpGit(sc mcpScope) (gctx, error) {
	rt, err := s.openRuntime(sc.root.ID, sshx.Creds{})
	if err != nil {
		return gctx{}, err
	}
	return gctx{loc: sc.loc, root: sc.root, rt: rt, git: kanban.Git{Run: rt.Runner, Root: sc.root.Path}}, nil
}

// ticketTool binds the arguments of a tool on one ticket and announces the change.
func ticketTool[T any](f func(ctx context.Context, s *Server, sc mcpScope, id int64, a T) (string, error)) func(context.Context, *Server, json.RawMessage) (string, error) {
	return func(ctx context.Context, s *Server, raw json.RawMessage) (string, error) {
		head, err := bind[struct {
			Cwd string `json:"cwd"`
			ID  int64  `json:"id"`
		}](raw)
		if err != nil {
			return "", err
		}
		a, err := bind[T](raw)
		if err != nil {
			return "", err
		}
		sc, err := s.mcpScope(head.Cwd)
		if err != nil {
			return "", err
		}
		id, err := sc.id(head.ID)
		if err != nil {
			return "", err
		}
		out, err := f(ctx, s, sc, id, a)
		if err != nil {
			return "", err
		}
		s.emitKanban(sc.root.ID, id, nil)
		return out, nil
	}
}

func tooLong(text string, limit int, advice string) error {
	if n := len([]rune(strings.TrimSpace(text))); n > limit {
		return fmt.Errorf("too long (%d characters, %d max). %s", n, limit, advice)
	}
	return nil
}

const descriptionAdvice = "Keep the context, the need and the acceptance criteria, in short sentences; decisions go in notes (kanban_add_note), the approach in the plan."

func nonEmpty(v string) *string {
	if v == "" {
		return nil
	}
	return &v
}

var mcpTools = []mcpTool{
	{
		name:        "kanban_list",
		description: "Lists the tickets of the kanban of the project (number, status, priority, title, goals, open feedback).",
		props:       map[string]any{"status": enum("Status filter (optional)", kanban.Statuses...), "query": str("Filter on the title (optional)")},
		run: func(ctx context.Context, s *Server, raw json.RawMessage) (string, error) {
			a, err := bind[struct{ Cwd, Status, Query string }](raw)
			if err != nil {
				return "", err
			}
			sc, err := s.mcpScope(a.Cwd)
			if err != nil {
				return "", err
			}
			list, err := s.Kanban.List(sc.loc)
			if err != nil {
				return "", err
			}
			var b strings.Builder
			fmt.Fprintf(&b, "Kanban of %s (%s)\nProject in the IDE: %s\n", sc.root.Name(), sc.root.Path, s.projectURL(sc.here.ID))
			n := 0
			for _, t := range list {
				if (a.Status != "" && t.Status != a.Status) || (a.Query != "" && !strings.Contains(strings.ToLower(t.Title), strings.ToLower(a.Query))) {
					continue
				}
				n++
				fmt.Fprintf(&b, "#%d [%s] (%s) %s", t.ID, kanban.StatusNames[t.Status], kanban.PriorityNames[t.Priority], t.Title)
				if t.Goals > 0 {
					fmt.Fprintf(&b, " · goals %d/%d", t.GoalsDone, t.Goals)
				}
				if t.FeedbackOpen > 0 {
					fmt.Fprintf(&b, " · open feedback %d", t.FeedbackOpen)
				}
				b.WriteString("\n")
			}
			if n == 0 {
				b.WriteString("No ticket.")
			}
			return strings.TrimSpace(b.String()), nil
		},
	},
	{
		name:        "kanban_get",
		description: "Reads a whole ticket: description, notes, plan, goals and test feedback (with their ids), linked files, conversations, branch and worktree.",
		props:       map[string]any{"id": ticketID},
		run: ticketTool(func(ctx context.Context, s *Server, sc mcpScope, id int64, _ struct{}) (string, error) {
			t, err := s.Kanban.Get(sc.loc, id)
			if err != nil {
				return "", err
			}
			return fmt.Sprintf("%s\n\nTicket in the IDE: %s\nProject in the IDE: %s", kanban.Markdown(t), s.ticketURL(sc, id), s.projectURL(sc.here.ID)), nil
		}),
	},
	{
		name:        "kanban_conversation",
		description: "Reads a conversation of the local assistant linked to the ticket (see Linked conversations in kanban_get): the messages of the user and of the assistant, with the questions asked and their answers. Use it to take over a briefing.",
		props:       map[string]any{"id": ticketID, "chat": str("Conversation id (chat … in kanban_get)")},
		required:    []string{"chat"},
		run: ticketTool(func(ctx context.Context, s *Server, sc mcpScope, id int64, a struct{ Chat string }) (string, error) {
			t, err := s.Kanban.Get(sc.loc, id)
			if err != nil {
				return "", err
			}
			linked := false
			for _, c := range t.ChatList {
				linked = linked || c.ChatID == a.Chat
			}
			if !linked {
				return "", fmt.Errorf("conversation %s is not linked to ticket #%d", a.Chat, id)
			}
			raw, err := s.LLM.GetChat(llm.ChatLocation{Project: sc.loc.Project, IdeDir: sc.loc.IdeDir}, a.Chat)
			if err != nil {
				return "", err
			}
			return conversationText(raw), nil
		}),
	},
	{
		name:        "kanban_create",
		description: "Creates a ticket in the backlog (status New). Use it when the user asks for it or agrees to note a task for later.",
		props: map[string]any{
			"title":       str("Short title"),
			"description": str(fmt.Sprintf("Description in Markdown, %d characters max: context, need, acceptance criteria", kanban.MaxDescription)),
			"priority":    enum("Priority", kanban.Priorities...),
			"files":       strList("Paths of the files concerned (relative to the root of the project)"),
		},
		required: []string{"title"},
		run: func(ctx context.Context, s *Server, raw json.RawMessage) (string, error) {
			a, err := bind[struct {
				Cwd, Title, Description, Priority string
				Files                             []string
			}](raw)
			if err != nil {
				return "", err
			}
			if err := tooLong(a.Description, kanban.MaxDescription, descriptionAdvice); err != nil {
				return "", err
			}
			sc, err := s.mcpScope(a.Cwd)
			if err != nil {
				return "", err
			}
			id, err := s.Kanban.Create(sc.loc, kanban.Patch{Title: &a.Title, Description: nonEmpty(a.Description), Priority: nonEmpty(a.Priority), AddFiles: a.Files}, kanban.ByClaude)
			if err != nil {
				return "", err
			}
			s.emitKanban(sc.root.ID, id, nil)
			return fmt.Sprintf("Ticket #%d created in the backlog (status New): %s", id, s.ticketURL(sc, id)), nil
		},
	},
	{
		name:        "kanban_update",
		description: "Changes a ticket (only the given fields).",
		props: map[string]any{
			"id":           ticketID,
			"title":        str("New title"),
			"description":  str(fmt.Sprintf("New full description (Markdown, %d characters max)", kanban.MaxDescription)),
			"priority":     enum("Priority", kanban.Priorities...),
			"test_summary": str("How to test the ticket (Markdown): steps, commands, expected results"),
			"add_files":    strList("Files to link"),
			"remove_files": strList("Files to unlink"),
		},
		run: ticketTool(func(ctx context.Context, s *Server, sc mcpScope, id int64, a struct {
			Title, Description, Priority string
			TestSummary                  string   `json:"test_summary"`
			AddFiles                     []string `json:"add_files"`
			RemoveFiles                  []string `json:"remove_files"`
		}) (string, error) {
			if err := tooLong(a.Description, kanban.MaxDescription, descriptionAdvice); err != nil {
				return "", err
			}
			p := kanban.Patch{Title: nonEmpty(a.Title), Description: nonEmpty(a.Description), Priority: nonEmpty(a.Priority), TestSummary: nonEmpty(a.TestSummary), AddFiles: a.AddFiles, RemoveFiles: a.RemoveFiles}
			if err := s.Kanban.Update(sc.loc, id, p, kanban.ByClaude); err != nil {
				return "", err
			}
			return fmt.Sprintf("Ticket #%d updated.", id), nil
		}),
	},
	{
		name:        "kanban_add_note",
		description: fmt.Sprintf("Adds a short note to a ticket (%d characters max): a decision, a fact found, an answer of the user worth keeping. Not for progress logs, restatements of the ticket or corrections of earlier notes.", kanban.MaxNote),
		props:       map[string]any{"id": ticketID, "text": str(fmt.Sprintf("Note in Markdown, %d characters max", kanban.MaxNote))},
		required:    []string{"text"},
		run: ticketTool(func(ctx context.Context, s *Server, sc mcpScope, id int64, a struct{ Text string }) (string, error) {
			if err := tooLong(a.Text, kanban.MaxNote, "Keep only what is worth remembering, in a few lines; details belong in the description (kanban_update) or the plan."); err != nil {
				return "", err
			}
			if err := s.Kanban.AddNote(sc.loc, id, strings.TrimSpace(a.Text), kanban.ByClaude, ""); err != nil {
				return "", err
			}
			return "Note added.", nil
		}),
	},
	{
		name:        "kanban_set_plan",
		description: `Writes the implementation plan of a ticket and its goals (verifiable objectives, checked during development). Replaces the plan and the goals of a previous plan (the goals of the user stay). A New ticket moves to "To do".`,
		props: map[string]any{
			"id":   ticketID,
			"plan": str("Plan in Markdown: approach, files, steps, risks, tests"),
			"goals": map[string]any{
				"type":        "array",
				"description": "Goals, each one verifiable",
				"items": map[string]any{"type": "object", "required": []string{"title"}, "properties": map[string]any{
					"title": str("Short title, one sentence"), "description": str("How to check it (optional, a few lines)"),
				}},
			},
		},
		required: []string{"plan", "goals"},
		run: ticketTool(func(ctx context.Context, s *Server, sc mcpScope, id int64, a struct {
			Plan  string
			Goals []kanban.GoalInput
		}) (string, error) {
			if strings.TrimSpace(a.Plan) == "" {
				return "", fmt.Errorf("empty plan")
			}
			if err := s.Kanban.SetPlan(sc.loc, id, a.Plan, a.Goals, kanban.ByClaude); err != nil {
				return "", err
			}
			t, err := s.Kanban.Get(sc.loc, id)
			if err != nil {
				return "", err
			}
			var b strings.Builder
			fmt.Fprintf(&b, "Plan saved; the ticket is %q. Goals:", kanban.StatusNames[t.Status])
			for _, g := range t.GoalList {
				fmt.Fprintf(&b, "\n- (id %d) %s", g.ID, g.Text)
			}
			return b.String(), nil
		}),
	},
	{
		name:        "kanban_goal",
		description: "Checks, unchecks or adds a goal of a ticket. Check each goal as soon as it is reached and verified.",
		props: map[string]any{
			"id":          ticketID,
			"action":      enum("What to do", "check", "uncheck", "add"),
			"goal":        map[string]any{"type": "integer", "description": "Goal id (check / uncheck), see kanban_get"},
			"title":       str("Title of the goal (add)"),
			"description": str("How to check it (add, optional)"),
		},
		required: []string{"action"},
		run: ticketTool(func(ctx context.Context, s *Server, sc mcpScope, id int64, a struct {
			Action, Title, Description string
			Goal                       int64
		}) (string, error) {
			op := kanban.GoalOp{Op: "check", ID: a.Goal, Done: a.Action == "check"}
			switch a.Action {
			case "add":
				op = kanban.GoalOp{Op: "add", Text: a.Title, Description: a.Description, Source: "plan"}
			case "check", "uncheck":
			default:
				return "", fmt.Errorf("unknown action: %s", a.Action)
			}
			if err := s.Kanban.Goal(sc.loc, id, op); err != nil {
				return "", err
			}
			t, err := s.Kanban.Get(sc.loc, id)
			if err != nil {
				return "", err
			}
			left := 0
			for _, g := range t.GoalList {
				if !g.Done {
					left++
				}
			}
			return fmt.Sprintf("Goal %sed. %d goal(s) left.", strings.TrimSuffix(a.Action, "e"), left), nil
		}),
	},
	{
		name:        "kanban_feedback",
		description: "Marks a test feedback of a ticket as handled (done) once fixed and verified, or as open again (reopen).",
		props: map[string]any{
			"id":       ticketID,
			"action":   enum("What to do", "done", "reopen"),
			"feedback": map[string]any{"type": "integer", "description": "Feedback id, see kanban_get"},
		},
		required: []string{"action", "feedback"},
		run: ticketTool(func(ctx context.Context, s *Server, sc mcpScope, id int64, a struct {
			Action   string
			Feedback int64
		}) (string, error) {
			if a.Action != "done" && a.Action != "reopen" {
				return "", fmt.Errorf("unknown action: %s", a.Action)
			}
			if _, err := s.Kanban.Feedback(sc.loc, id, kanban.FeedbackOp{Op: "check", ID: a.Feedback, Done: a.Action == "done"}, kanban.ByClaude); err != nil {
				return "", err
			}
			return fmt.Sprintf("Feedback %d marked %s.", a.Feedback, map[string]string{"done": "done", "reopen": "open"}[a.Action]), nil
		}),
	},
	{
		name:        "kanban_move",
		description: `Moves a ticket from In progress to To test (status review), with test_summary: how to test it. Other changes of status belong to the user.`,
		props: map[string]any{
			"id":           ticketID,
			"status":       enum("New status", kanban.Review),
			"test_summary": str("What to test and how (steps, commands, expected results), in Markdown"),
			"comment":      str("Comment for the history (optional)"),
		},
		required: []string{"status", "test_summary"},
		run: ticketTool(func(ctx context.Context, s *Server, sc mcpScope, id int64, a struct {
			Status, Comment string
			TestSummary     string `json:"test_summary"`
		}) (string, error) {
			if strings.TrimSpace(a.TestSummary) == "" {
				return "", fmt.Errorf(`test_summary is required to move to "To test"`)
			}
			t, err := s.Kanban.Get(sc.loc, id)
			if err != nil {
				return "", err
			}
			if !kanban.CanMove(t.Status, a.Status, kanban.ByClaude) {
				return "", fmt.Errorf("you cannot move the ticket from %q to %q", kanban.StatusNames[t.Status], kanban.StatusNames[a.Status])
			}
			if err := s.Kanban.Update(sc.loc, id, kanban.Patch{TestSummary: &a.TestSummary}, kanban.ByClaude); err != nil {
				return "", err
			}
			if err := s.Kanban.Move(sc.loc, id, a.Status, kanban.ByClaude, a.Comment); err != nil {
				return "", err
			}
			return fmt.Sprintf("Ticket #%d moved to %q.", id, kanban.StatusNames[a.Status]), nil
		}),
	},
	{
		name:        "kanban_link_commit",
		description: "Links a commit to a ticket (after a git commit on its branch).",
		props:       map[string]any{"id": ticketID, "hash": str("Commit hash (short or full)")},
		required:    []string{"hash"},
		run: ticketTool(func(ctx context.Context, s *Server, sc mcpScope, id int64, a struct{ Hash string }) (string, error) {
			k, err := s.mcpGit(sc)
			if err != nil {
				return "", err
			}
			hash, subject, err := k.git.Commit(ctx, strings.TrimSpace(a.Hash))
			if err != nil {
				return "", err
			}
			if err := s.Kanban.LinkCommit(sc.loc, id, hash, subject); err != nil {
				return "", err
			}
			return fmt.Sprintf("Commit %s linked to ticket #%d.", hash[:min(10, len(hash))], id), nil
		}),
	},
	{
		name: "kanban_start",
		description: `Starts the development of a ticket ("To do", or "In progress" / "To test" without a worktree): creates the branch ticket/<n>-<slug> from the base in a worktree of the project, runs the setup command of the kanban there, and moves the ticket to "In progress". Answers the worktree: work there, with absolute paths, and commit on that branch.`,
		props:       map[string]any{"id": ticketID, "base": str("Base branch (optional: the base of the ticket or of the kanban, else origin/main or main)")},
		run: ticketTool(func(ctx context.Context, s *Server, sc mcpScope, id int64, a struct{ Base string }) (string, error) {
			k, err := s.mcpGit(sc)
			if err != nil {
				return "", err
			}
			t, child, err := s.startTicket(ctx, k, id, a.Base, kanban.ByClaude)
			if err != nil {
				return "", err
			}
			setup := ""
			if t.Setup == "running" {
				setup = "\nThe setup command of the kanban is running in the worktree; kanban_get shows when it is done (Setup)."
			}
			return fmt.Sprintf("Ticket #%d is %q.\nBranch: %s (base %s)\nWorktree: %s\nWorktree in the IDE: %s\nWork in the worktree (absolute paths, `cd %s && …` for commands); commit messages start with \"#%d \".%s",
				t.ID, kanban.StatusNames[t.Status], t.Branch, t.Base, t.Worktree, s.projectURL(child), t.Worktree, t.ID, setup), nil
		}),
	},
}

func findMCPTool(name string) *mcpTool {
	for i := range mcpTools {
		if mcpTools[i].name == name {
			return &mcpTools[i]
		}
	}
	return nil
}

// conversationText is a conversation of the assistant as Claude reads it: what the user
// and the assistant wrote, and the questions of ask_user with their answers.
func conversationText(raw json.RawMessage) string {
	var chat struct {
		Title    string `json:"title"`
		Messages []struct {
			Role      string          `json:"role"`
			Content   json.RawMessage `json:"content"`
			Display   string          `json:"display"`
			Kind      string          `json:"kind"`
			ToolCalls []struct {
				ID       string `json:"id"`
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
			ToolCallID string `json:"tool_call_id"`
		} `json:"messages"`
	}
	_ = json.Unmarshal(raw, &chat)
	text := func(c json.RawMessage) string {
		var s string
		if json.Unmarshal(c, &s) == nil {
			return s
		}
		var parts []struct{ Type, Text string }
		_ = json.Unmarshal(c, &parts)
		var out []string
		for _, p := range parts {
			if p.Type == "text" {
				out = append(out, p.Text)
			}
		}
		return strings.Join(out, "\n")
	}
	var b strings.Builder
	fmt.Fprintf(&b, "# Conversation: %s\n", chat.Title)
	asks := map[string]bool{}
	for _, m := range chat.Messages {
		switch m.Role {
		case "user":
			t := m.Display
			if t == "" {
				t = text(m.Content)
			}
			label := "User"
			if m.Kind == "summary" {
				label = "Summary of the earlier messages"
			}
			fmt.Fprintf(&b, "\n## %s\n%s\n", label, strings.TrimSpace(t))
		case "assistant":
			if t := strings.TrimSpace(text(m.Content)); t != "" {
				fmt.Fprintf(&b, "\n## Assistant\n%s\n", t)
			}
			for _, c := range m.ToolCalls {
				if c.Function.Name == "ask_user" {
					asks[c.ID] = true
					fmt.Fprintf(&b, "\n## Assistant asks (ask_user)\n%s\n", c.Function.Arguments)
				}
			}
		case "tool":
			if asks[m.ToolCallID] {
				fmt.Fprintf(&b, "\n## Answers of the user\n%s\n", strings.TrimSpace(text(m.Content)))
			}
		}
	}
	return strings.TrimSpace(b.String())
}
