package kanban

import (
	"context"

	kb "github.com/DrSmithFr/web-ide/plugins/kanban/kanban"
	"github.com/DrSmithFr/web-ide/pod/acp"
	"github.com/DrSmithFr/web-ide/pod/projects"
	"github.com/DrSmithFr/web-ide/pod/runtime"
)

// What the rest of the pod still asks the kanban: the kanban tools of the assistant and of the
// MCP (their declarations move with AI, #123), the Orchestrator, the worktrees of a window.

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

func (s *K) TicketSession(root *projects.Project, t *kb.Ticket) (string, acp.Info, []acp.Entry, bool) {
	return s.ticketSession(root, t)
}

func (s *K) MilestoneTool(root string, loc kb.Location, a MilestoneArgs, by, chatID string) (string, int64, error) {
	return s.milestoneTool(root, loc, a, by, chatID)
}

func (s *K) ObjectiveTool(root string, loc kb.Location, a ObjectiveArgs, by, chatID string) (string, error) {
	return s.objectiveTool(root, loc, a, by, chatID)
}

func (s *K) StartTicket(ctx context.Context, k GitCtx, id int64, base, by string, force bool) (*kb.Ticket, string, error) {
	return s.startTicket(ctx, k, id, base, by, force)
}

// ActiveStep is the step of a lineage its worktree works on.
func (s *K) ActiveStep(loc kb.Location, id int64) int64 { return s.activeStep(loc, id) }
