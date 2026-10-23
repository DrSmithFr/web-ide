package kanban

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	kb "github.com/DrSmithFr/web-ide/plugins/kanban/kanban"
	"github.com/DrSmithFr/web-ide/pod/assistant"
	"github.com/DrSmithFr/web-ide/pod/plugins"
)

// Tools of the MCP endpoint on the tickets (Host.MCPTool): those of the assistant with the
// ticket given explicitly, plus kanban_start; Claude Code is their author. The ones on the
// conversations linked to a ticket are the AI's. Texts read by the model are English.

// Scope is the kanban a tool of the MCP works on: the one of the main project of the folder
// of the client; in the worktree of a lineage, its ticket is the step it works on.
type Scope struct {
	plugins.MCPScope
	Loc kb.Location
}

func (s *K) scope(ms plugins.MCPScope) Scope {
	sc := Scope{MCPScope: ms, Loc: kb.LocationOf(ms.Root)}
	if sc.Ticket != 0 {
		sc.Ticket = s.activeStep(sc.Loc, sc.Ticket)
	}
	return sc
}

func (sc Scope) ticketURL(id int64) string {
	return fmt.Sprintf("%s?ticket=%d", sc.ProjectURL(sc.Root.ID), id)
}

// id is the ticket of a call: the one given, else the one of the worktree.
func (sc Scope) id(given int64) (int64, error) {
	if given > 0 {
		return given, nil
	}
	if sc.Ticket > 0 {
		return sc.Ticket, nil
	}
	return 0, fmt.Errorf("id is missing (you are not in the worktree of a ticket)")
}

// mcpGit opens the runtime of the main project for the git side of the tickets.
func (s *K) mcpGit(sc Scope) (GitCtx, error) {
	rt, err := s.pod.Runtime(sc.Root.ID)
	if err != nil {
		return GitCtx{}, err
	}
	return NewGitCtx(sc.Loc, sc.Root, rt, kb.Git{Run: rt.Runner, Root: sc.Root.Path}), nil
}

// TicketTool binds the arguments of a tool of the MCP on one ticket and announces the change
// (the conversation tools of the AI use it too).
func TicketTool[T any](s *K, f func(ctx context.Context, sc Scope, id int64, a T) (string, error)) func(context.Context, plugins.MCPScope, json.RawMessage) (string, error) {
	return func(ctx context.Context, ms plugins.MCPScope, raw json.RawMessage) (string, error) {
		head, err := bind[struct {
			ID int64 `json:"id"`
		}](raw)
		if err != nil {
			return "", err
		}
		a, err := bind[T](raw)
		if err != nil {
			return "", err
		}
		sc := s.scope(ms)
		id, err := sc.id(head.ID)
		if err != nil {
			return "", err
		}
		out, err := f(ctx, sc, id, a)
		if err != nil {
			return "", err
		}
		s.Emit(sc.Root.ID, id)
		return out, nil
	}
}

// TicketID is the parameter of the ticket of a tool of the MCP.
var TicketID = map[string]any{"type": "integer", "description": "Ticket number (default: the ticket of the worktree you are in)"}

var (
	parentProp     = map[string]any{"type": "integer", "description": "Root ticket: this one becomes a step of its lineage, developed in the root's worktree after the previous steps (0 takes it out). Only before its development starts"}
	complexityProp = plugins.Enum("How hard the work is, which picks the model and the effort of its sessions: low (a small obvious change: a color, a label, a one-line fix), medium (usual work), high (a delicate design, concurrency, security, a bug hard to find)", kb.Complexities...)
	sizeProp       = plugins.Enum("Estimated effort of the whole ticket: s (a few files, an hour of AI work), m, l, xl (many files across the pod and the page, several days)", kb.Sizes...)
	dependsOnProp  = map[string]any{"type": "array", "items": map[string]any{"type": "integer"}, "description": "Tickets of other lineages this one waits for: it starts once they are merged or done (replaces the list)"}
	milestoneProp  = map[string]any{"type": "integer", "description": "Milestone of the ticket and of its steps (0 takes it out; a step has the one of its root); see kanban_milestone"}
	objectiveProp  = map[string]any{"type": "integer", "description": "Objective of the ticket and of its steps, apart from its milestone (0 takes it out; a step has the one of its root); see kanban_objective"}
)

const descriptionAdvice = "Keep the context, the need and the acceptance criteria, in short sentences; decisions go in notes (kanban_add_note), the approach in the plan."

func (s *K) registerMCP() {
	for _, t := range s.mcpTools() {
		s.host.MCPTool(t)
	}
}

func (s *K) mcpTools() []plugins.MCPTool {
	return []plugins.MCPTool{
		{
			Name:        "kanban_list",
			Description: "Lists the tickets of the kanban of the project (number, status, priority, title, goals, open feedback).",
			Props:       map[string]any{"status": plugins.Enum("Status filter (optional; review is the old name of to_test)", append(append([]string{}, kb.Statuses...), "review")...), "query": plugins.Str("Filter on the title (optional)")},
			Run: func(ctx context.Context, ms plugins.MCPScope, raw json.RawMessage) (string, error) {
				a, err := bind[struct{ Cwd, Status, Query string }](raw)
				if err != nil {
					return "", err
				}
				sc := s.scope(ms)
				a.Status = kb.NormStatus(a.Status)
				list, err := s.Kanban.List(sc.Loc)
				if err != nil {
					return "", err
				}
				if k, err := s.mcpGit(sc); err == nil {
					s.fillBlockers(ctx, k, list)
				}
				var b strings.Builder
				fmt.Fprintf(&b, "Kanban of %s (%s)\nProject in the IDE: %s\n", sc.Root.Name(), sc.Root.Path, sc.ProjectURL(sc.Here.ID))
				n := 0
				for _, t := range list {
					if (a.Status != "" && t.Status != a.Status) || (a.Query != "" && !strings.Contains(strings.ToLower(t.Title), strings.ToLower(a.Query))) {
						continue
					}
					n++
					fmt.Fprintf(&b, "#%d [%s] (%s) %s", t.ID, kb.StatusNames[t.Status], kb.PriorityNames[t.Priority], t.Title)
					if t.Size != "" {
						fmt.Fprintf(&b, " · size %s", kb.SizeNames[t.Size])
					}
					if t.Complexity != "" {
						fmt.Fprintf(&b, " · complexity %s", kb.ComplexityNames[t.Complexity])
					}
					if t.Goals > 0 {
						fmt.Fprintf(&b, " · goals %d/%d", t.GoalsDone, t.Goals)
					}
					if t.FeedbackOpen > 0 {
						fmt.Fprintf(&b, " · open feedback %d", t.FeedbackOpen)
					}
					if t.Parent != 0 {
						fmt.Fprintf(&b, " · step of #%d", t.Parent)
					}
					if len(t.Blockers) > 0 {
						fmt.Fprintf(&b, " · blocked by %s", kb.BlockersText(t.Blockers))
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
			Name:        "kanban_get",
			Description: "Reads a whole ticket: description, notes, plan, goals and test feedback (with their ids), linked files, conversations, branch and worktree.",
			Props:       map[string]any{"id": TicketID},
			Run: TicketTool(s, func(ctx context.Context, sc Scope, id int64, _ struct{}) (string, error) {
				t, err := s.Kanban.Get(sc.Loc, id)
				if err != nil {
					return "", err
				}
				if kb.Startable(t.Status) && (t.Parent != 0 || len(t.DependsOn) > 0) {
					if k, err := s.mcpGit(sc); err == nil {
						t.Blockers, _ = s.ticketBlockers(ctx, k, id)
					}
				}
				md := kb.Markdown(t)
				if _, info, _, ok := s.ticketSession(sc.Root, t); ok {
					md += fmt.Sprintf("\n\n## Agent session\n- %s (%s, %s): read it with kanban_conversation chat=%s", info.Title, info.Name, info.State, info.ID)
				}
				return fmt.Sprintf("%s\n\nTicket in the IDE: %s\nProject in the IDE: %s", md, sc.ticketURL(id), sc.ProjectURL(sc.Here.ID)), nil
			}),
		},
		{
			Name:        "kanban_create",
			Description: "Creates a ticket in the backlog (status New). Use it when the user asks for it or agrees to note a task for later.",
			Props: map[string]any{
				"title":       plugins.Str("Short title"),
				"description": plugins.Str(fmt.Sprintf("Description in Markdown, %d characters max: context, need, acceptance criteria", kb.MaxDescription)),
				"priority":    plugins.Enum("Priority", kb.Priorities...),
				"files":       plugins.StrList("Paths of the files concerned (relative to the root of the project)"),
				"parent":      parentProp,
				"depends_on":  dependsOnProp,
				"milestone":   milestoneProp,
				"objective":   objectiveProp,
			},
			Required: []string{"title"},
			Run: func(ctx context.Context, ms plugins.MCPScope, raw json.RawMessage) (string, error) {
				a, err := bind[struct {
					Cwd, Title, Description, Priority string
					Files                             []string
					Parent, Milestone, Objective      *int64
					DependsOn                         *[]int64 `json:"depends_on"`
				}](raw)
				if err != nil {
					return "", err
				}
				if err := TooLong(a.Description, kb.MaxDescription, descriptionAdvice); err != nil {
					return "", err
				}
				sc := s.scope(ms)
				id, err := s.Kanban.Create(sc.Loc, kb.Patch{Title: &a.Title, Description: NonEmpty(a.Description), Priority: NonEmpty(a.Priority), AddFiles: a.Files, Parent: a.Parent, DependsOn: a.DependsOn, Milestone: a.Milestone, Objective: a.Objective}, kb.ByClaude)
				if err != nil {
					return "", err
				}
				s.Emit(sc.Root.ID, id)
				return fmt.Sprintf("Ticket #%d created in the backlog (status New): %s", id, sc.ticketURL(id)), nil
			},
		},
		{
			Name:        "kanban_update",
			Description: "Changes a ticket (only the given fields).",
			Props: map[string]any{
				"id":           TicketID,
				"title":        plugins.Str("New title"),
				"description":  plugins.Str(fmt.Sprintf("New full description (Markdown, %d characters max)", kb.MaxDescription)),
				"priority":     plugins.Enum("Priority", kb.Priorities...),
				"test_summary": plugins.Str("How to test the ticket (Markdown): steps, commands, expected results"),
				"add_files":    plugins.StrList("Files to link"),
				"remove_files": plugins.StrList("Files to unlink"),
				"size":         sizeProp,
				"complexity":   complexityProp,
				"parent":       parentProp,
				"depends_on":   dependsOnProp,
				"milestone":    milestoneProp,
				"objective":    objectiveProp,
			},
			Run: TicketTool(s, func(ctx context.Context, sc Scope, id int64, a struct {
				Title, Description, Priority string
				TestSummary                  string   `json:"test_summary"`
				AddFiles                     []string `json:"add_files"`
				RemoveFiles                  []string `json:"remove_files"`
				Parent, Milestone, Objective *int64
				DependsOn                    *[]int64 `json:"depends_on"`
				Size, Complexity             string
			}) (string, error) {
				if err := TooLong(a.Description, kb.MaxDescription, descriptionAdvice); err != nil {
					return "", err
				}
				p := kb.Patch{Title: NonEmpty(a.Title), Description: NonEmpty(a.Description), Priority: NonEmpty(a.Priority), TestSummary: NonEmpty(a.TestSummary), AddFiles: a.AddFiles, RemoveFiles: a.RemoveFiles, Parent: a.Parent, DependsOn: a.DependsOn, Size: NonEmpty(a.Size), Complexity: NonEmpty(a.Complexity), Milestone: a.Milestone, Objective: a.Objective}
				if err := s.Kanban.Update(sc.Loc, id, p, kb.ByClaude); err != nil {
					return "", err
				}
				return fmt.Sprintf("Ticket #%d updated.", id), nil
			}),
		},
		{
			Name:        "kanban_milestone",
			Description: assistant.MilestoneDescription,
			Props:       assistant.MilestoneProps,
			Required:    []string{"action"},
			Run: func(ctx context.Context, ms plugins.MCPScope, raw json.RawMessage) (string, error) {
				a, err := bind[struct {
					Cwd string
					milestoneArgs
				}](raw)
				if err != nil {
					return "", err
				}
				sc := s.scope(ms)
				out, _, err := s.milestoneTool(sc.Root.ID, sc.Loc, a.milestoneArgs, kb.ByClaude, "")
				return out, err
			},
		},
		{
			Name:        "kanban_objective",
			Description: assistant.ObjectiveDescription,
			Props:       assistant.ObjectiveProps,
			Required:    []string{"action"},
			Run: func(ctx context.Context, ms plugins.MCPScope, raw json.RawMessage) (string, error) {
				a, err := bind[struct {
					Cwd string
					objectiveArgs
				}](raw)
				if err != nil {
					return "", err
				}
				sc := s.scope(ms)
				return s.objectiveTool(sc.Root.ID, sc.Loc, a.objectiveArgs, kb.ByClaude, "")
			},
		},
		{
			Name:        "kanban_add_note",
			Description: fmt.Sprintf("Adds a short note to a ticket (%d characters max): a decision, a fact found, an answer of the user worth keeping. Not for progress logs, restatements of the ticket or corrections of earlier notes.", kb.MaxNote),
			Props:       map[string]any{"id": TicketID, "text": plugins.Str(fmt.Sprintf("Note in Markdown, %d characters max", kb.MaxNote))},
			Required:    []string{"text"},
			Run: TicketTool(s, func(ctx context.Context, sc Scope, id int64, a struct{ Text string }) (string, error) {
				if err := TooLong(a.Text, kb.MaxNote, "Keep only what is worth remembering, in a few lines; details belong in the description (kanban_update) or the plan."); err != nil {
					return "", err
				}
				if err := s.Kanban.AddNote(sc.Loc, id, strings.TrimSpace(a.Text), kb.ByClaude, ""); err != nil {
					return "", err
				}
				return "Note added.", nil
			}),
		},
		{
			Name:        "kanban_set_plan",
			Description: `Writes the implementation plan of a ticket and its goals (verifiable objectives, checked during development). Replaces the plan and the goals of a previous plan (the goals of the user stay). A New ticket moves to "To do".`,
			Props: map[string]any{
				"id":   TicketID,
				"plan": plugins.Str("Plan in Markdown: approach, files, steps, risks, tests"),
				"goals": map[string]any{
					"type":        "array",
					"description": "Goals, each one verifiable",
					"items": map[string]any{"type": "object", "required": []string{"title"}, "properties": map[string]any{
						"title": plugins.Str("Short title, one sentence"), "description": plugins.Str("How to check it (optional, a few lines)"),
					}},
				},
				"size":       sizeProp,
				"complexity": complexityProp,
			},
			Required: []string{"plan", "goals", "size", "complexity"},
			Run: TicketTool(s, func(ctx context.Context, sc Scope, id int64, a struct {
				Plan             string
				Goals            []kb.GoalInput
				Size, Complexity string
			}) (string, error) {
				if strings.TrimSpace(a.Plan) == "" {
					return "", fmt.Errorf("empty plan")
				}
				if a.Size == "" {
					return "", fmt.Errorf("size is required: s, m, l or xl")
				}
				if a.Complexity == "" {
					return "", fmt.Errorf("complexity is required: low, medium or high")
				}
				if err := s.Kanban.Update(sc.Loc, id, kb.Patch{Size: &a.Size, Complexity: &a.Complexity, PlanSize: true}, kb.ByClaude); err != nil {
					return "", err
				}
				if err := s.Kanban.SetPlan(sc.Loc, id, a.Plan, a.Goals, kb.ByClaude); err != nil {
					return "", err
				}
				t, err := s.Kanban.Get(sc.Loc, id)
				if err != nil {
					return "", err
				}
				var b strings.Builder
				fmt.Fprintf(&b, "Plan saved; the ticket is %q. Goals:", kb.StatusNames[t.Status])
				for _, g := range t.GoalList {
					fmt.Fprintf(&b, "\n- (id %d) %s", g.ID, g.Text)
				}
				return b.String(), nil
			}),
		},
		{
			Name:        "kanban_goal",
			Description: "Checks, unchecks, adds, edits or deletes a goal of a ticket. Check each goal as soon as it is reached and verified.",
			Props: map[string]any{
				"id":          TicketID,
				"action":      plugins.Enum("What to do", "check", "uncheck", "add", "edit", "delete"),
				"goal":        map[string]any{"type": "integer", "description": "Goal id (check, uncheck, edit, delete), see kanban_get"},
				"title":       plugins.Str("Title of the goal (add, edit)"),
				"description": plugins.Str("How to check it (add, edit; optional)"),
			},
			Required: []string{"action"},
			Run: TicketTool(s, func(ctx context.Context, sc Scope, id int64, a struct {
				Action, Title, Description string
				Goal                       int64
			}) (string, error) {
				op := kb.GoalOp{Op: "check", ID: a.Goal, Done: a.Action == "check"}
				switch a.Action {
				case "add":
					op = kb.GoalOp{Op: "add", Text: a.Title, Description: a.Description, Source: "plan"}
				case "check", "uncheck":
				case "edit":
					if err := s.Kanban.GoalEdit(sc.Loc, id, a.Goal, a.Title, a.Description); err != nil {
						return "", err
					}
					return fmt.Sprintf("Goal %d edited.", a.Goal), nil
				case "delete":
					op = kb.GoalOp{Op: "delete", ID: a.Goal}
				default:
					return "", fmt.Errorf("unknown action: %s", a.Action)
				}
				if err := s.Kanban.Goal(sc.Loc, id, op); err != nil {
					return "", err
				}
				t, err := s.Kanban.Get(sc.Loc, id)
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
			Name:        "kanban_feedback",
			Description: "Test feedback of a ticket: add one (a bug, a missing feature or an info found while testing), mark one handled (done) once fixed and verified, or open again (reopen).",
			Props: map[string]any{
				"id":         TicketID,
				"action":     plugins.Enum("What to do", "add", "done", "reopen"),
				"feedback":   map[string]any{"type": "integer", "description": "Feedback id (done, reopen), see kanban_get"},
				"kind":       plugins.Enum("Kind (add)", kb.FeedbackKinds...),
				"complexity": plugins.Enum("How hard the fix is (add): low, medium or high; it picks the model of the fix session", kb.Complexities...),
				"text":       plugins.Str(fmt.Sprintf("What was noticed (add, %d characters max)", kb.MaxNote)),
			},
			Required: []string{"action"},
			Run: TicketTool(s, func(ctx context.Context, sc Scope, id int64, a struct {
				Action, Kind, Text, Complexity string
				Feedback                       int64
			}) (string, error) {
				if a.Action == "add" {
					if a.Complexity == "" {
						return "", fmt.Errorf("complexity is required: low, medium or high")
					}
					fid, err := s.Kanban.Feedback(sc.Loc, id, kb.FeedbackOp{Op: "add", Kind: a.Kind, Text: a.Text, Complexity: a.Complexity}, kb.ByClaude)
					if err != nil {
						return "", err
					}
					s.NotifyFeedback(sc.Loc, id, a.Text)
					return fmt.Sprintf("Feedback %d added to ticket #%d.", fid, id), nil
				}
				if a.Action != "done" && a.Action != "reopen" {
					return "", fmt.Errorf("unknown action: %s", a.Action)
				}
				if _, err := s.Kanban.Feedback(sc.Loc, id, kb.FeedbackOp{Op: "check", ID: a.Feedback, Done: a.Action == "done"}, kb.ByClaude); err != nil {
					return "", err
				}
				return fmt.Sprintf("Feedback %d marked %s.", a.Feedback, map[string]string{"done": "done", "reopen": "open"}[a.Action]), nil
			}),
		},
		{
			Name:        "kanban_move",
			Description: `Moves a ticket from In progress to To test (status to_test), with test_summary: how to test it. Other changes of status belong to the user.`,
			Props: map[string]any{
				"id":           TicketID,
				"status":       plugins.Enum("New status (review is the old name of to_test)", kb.ToTest, "review"),
				"test_summary": plugins.Str("What to test and how (steps, commands, expected results), in Markdown"),
				"comment":      plugins.Str("Comment for the history (optional)"),
			},
			Required: []string{"status", "test_summary"},
			Run: TicketTool(s, func(ctx context.Context, sc Scope, id int64, a struct {
				Status, Comment string
				TestSummary     string `json:"test_summary"`
			}) (string, error) {
				a.Status = kb.NormStatus(a.Status)
				if strings.TrimSpace(a.TestSummary) == "" {
					return "", fmt.Errorf(`test_summary is required to move to "To test"`)
				}
				t, err := s.Kanban.Get(sc.Loc, id)
				if err != nil {
					return "", err
				}
				if !kb.CanMove(t.Status, a.Status, kb.ByClaude) {
					return "", fmt.Errorf("you cannot move the ticket from %q to %q", kb.StatusNames[t.Status], kb.StatusNames[a.Status])
				}
				if err := s.Kanban.Update(sc.Loc, id, kb.Patch{TestSummary: &a.TestSummary}, kb.ByClaude); err != nil {
					return "", err
				}
				if err := s.Kanban.Move(sc.Loc, id, a.Status, kb.ByClaude, a.Comment); err != nil {
					return "", err
				}
				return fmt.Sprintf("Ticket #%d moved to %q.", id, kb.StatusNames[a.Status]), nil
			}),
		},
		{
			Name:        "kanban_start",
			Description: `Starts the development of a ticket ("To do", or "In progress" / "To test" without a worktree): creates the branch ticket/<n>-<slug> from the base in a worktree of the project, runs the setup commands of the project there (its saved commands flagged setup), and moves the ticket to "In progress". Answers the worktree: work there, with absolute paths, and commit on that branch.`,
			Props:       map[string]any{"id": TicketID, "base": plugins.Str("Base branch (optional: the base of the ticket or of the kanban, else origin/main or main)")},
			Run: TicketTool(s, func(ctx context.Context, sc Scope, id int64, a struct{ Base string }) (string, error) {
				k, err := s.mcpGit(sc)
				if err != nil {
					return "", err
				}
				t, child, err := s.startTicket(ctx, k, id, a.Base, kb.ByClaude, false)
				var ce *plugins.CodedError
				if errors.As(err, &ce) && ce.Code == "blocked" {
					if bl, e := s.ticketBlockers(ctx, k, id); e == nil && len(bl) > 0 {
						return "", fmt.Errorf("ticket #%d cannot start yet: %s. Only the user can start it anyway, from the IDE", id, kb.BlockersText(bl))
					}
				}
				if err != nil {
					return "", err
				}
				setup := ""
				if t.Parent != 0 {
					setup = fmt.Sprintf("\nThis ticket is a step of the lineage of #%d: it shares its worktree and branch; commit only this step.", t.Parent)
				}
				if t.Setup == "running" {
					setup += "\nThe setup commands of the project are running in the worktree; kanban_get shows when it is done (Setup)."
				}
				return fmt.Sprintf("Ticket #%d is %q.\nBranch: %s (base %s)\nWorktree: %s\nWorktree in the IDE: %s\nSwitch this session into the worktree with EnterWorktree (path %s), or work in it with absolute paths (`cd %s && …` for commands); commit messages start with \"#%d \".%s",
					t.ID, kb.StatusNames[t.Status], t.Branch, t.Base, t.Worktree, sc.ProjectURL(child), t.Worktree, t.Worktree, t.ID, setup), nil
			}),
		},
	}
}
