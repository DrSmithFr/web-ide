package kanban

import (
	"context"
	"net/url"
	"strconv"

	"github.com/DrSmithFr/web-ide/lib/i18n"

	kb "github.com/DrSmithFr/web-ide/plugins/kanban/kanban"
	"github.com/DrSmithFr/web-ide/pod/plugins"
	"github.com/DrSmithFr/web-ide/pod/projects"
	"github.com/DrSmithFr/web-ide/pod/push"
	"github.com/DrSmithFr/web-ide/pod/runtime"
)

// What the AI asks the kanban: the kanban tools of the assistant, the Orchestrator, the
// conversation tools of the MCP (with TicketTool, mcp.go).

// GitCtx is a kanban, its main project, its runtime and its git.
type GitCtx = gctx

// NewGitCtx makes the GitCtx of a kanban.
func NewGitCtx(loc kb.Location, root *projects.Project, rt *runtime.Runtime, git kb.Git) GitCtx {
	return gctx{loc: loc, root: root, rt: rt, git: git}
}

// MilestoneArgs and ObjectiveArgs are the arguments of the milestone and objective tools.
type (
	MilestoneArgs = milestoneArgs
	ObjectiveArgs = objectiveArgs
)

// Emit tells the windows of a main project and of its worktrees that a ticket changed.
func (s *K) Emit(root string, id int64) { s.emitKanban(root, id, nil) }

func (s *K) FillBlockers(ctx context.Context, k GitCtx, list []kb.Summary) {
	s.fillBlockers(ctx, k, list)
}

func (s *K) TicketBlockers(ctx context.Context, k GitCtx, id int64) ([]kb.Blocker, error) {
	return s.ticketBlockers(ctx, k, id)
}

func (s *K) MilestoneTool(root string, loc kb.Location, a MilestoneArgs, by, chatID string) (string, int64, error) {
	return s.milestoneTool(root, loc, a, by, chatID)
}

func (s *K) ObjectiveTool(root string, loc kb.Location, a ObjectiveArgs, by, chatID string) (string, error) {
	return s.objectiveTool(root, loc, a, by, chatID)
}

// NotifyFeedback sends the push notification of a test feedback the assistant or Claude Code
// added to a ticket.
func (s *K) NotifyFeedback(loc kb.Location, id int64, text string) {
	ticket := ""
	if t, err := s.Kanban.Get(loc, id); err == nil {
		ticket = t.Title
	}
	name, _, _ := s.pod.Project(loc.Project)
	if name == "" {
		name = loc.Project
	}
	n := strconv.FormatInt(id, 10)
	s.pod.Notify(plugins.Note{Kind: push.Feedback, Path: "/project/" + url.PathEscape(loc.Project) + "?ticket=" + n, Tag: "ticket-" + n, Urgency: "normal",
		Text: func(lang string) (string, string) {
			return i18n.T(lang, "Feedback on #%d %s", id, ticket), name + " · " + text
		}})
}
