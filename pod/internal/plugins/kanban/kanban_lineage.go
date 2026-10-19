package kanban

import (
	"context"
	"github.com/DrSmithFr/web-ide/pod/internal/plugins"
	"strings"

	"github.com/DrSmithFr/web-ide/pod/internal/i18n"
	kb "github.com/DrSmithFr/web-ide/pod/internal/kanban"
	"github.com/DrSmithFr/web-ide/pod/internal/projects"
)

// Lineages and dependencies on the git side (docs/design/kanban.md): a dependency resolves once
// the branch of its lineage is merged, the children of a ticket work in its worktree.

// mergedFunc tells whether the branch of a lineage root is merged into its local base.
func (s *K) mergedFunc(ctx context.Context, k gctx) func(*kb.Summary) bool {
	if k.rt == nil || !k.git.IsRepo(ctx) {
		return nil
	}
	meta, _ := s.Kanban.Meta(k.loc)
	def := ""
	return func(r *kb.Summary) bool {
		if r.Branch == "" {
			return false
		}
		base := ""
		if t, err := s.Kanban.Get(k.loc, r.ID); err == nil {
			base = t.Base
		}
		if base = firstOf(base, meta["base"]); base == "" {
			if def == "" {
				def = k.git.DefaultBase(ctx)
			}
			base = def
		}
		return k.git.Merged(ctx, r.Branch, k.git.LocalBranch(ctx, base))
	}
}

// fillBlockers sets the blockers of the tickets that may still start.
func (s *K) fillBlockers(ctx context.Context, k gctx, list []kb.Summary) {
	var merged func(*kb.Summary) bool
	for i := range list {
		if !kb.Startable(list[i].Status) || list[i].Parent == 0 && len(list[i].DependsOn) == 0 {
			continue
		}
		if merged == nil {
			merged = s.mergedFunc(ctx, k)
		}
		list[i].Blockers = kb.Blockers(&list[i], list, merged)
	}
}

// ticketBlockers lists what keeps a ticket from starting.
func (s *K) ticketBlockers(ctx context.Context, k gctx, id int64) ([]kb.Blocker, error) {
	list, err := s.Kanban.List(k.loc)
	if err != nil {
		return nil, err
	}
	for i := range list {
		if list[i].ID == id {
			return kb.Blockers(&list[i], list, s.mergedFunc(ctx, k)), nil
		}
	}
	return nil, kb.ErrNotFound
}

func blockedError(bl []kb.Blocker) error {
	ids := make([]string, len(bl))
	for i, b := range bl {
		ids[i] = "#" + itoa(b.ID)
	}
	return &plugins.CodedError{Code: "blocked", Err: i18n.Errorf("this ticket cannot start yet: it waits for %s", strings.Join(ids, ", "))}
}

// lineageOpen refuses to merge or close a lineage before its children are finished.
func lineageOpen(t *kb.Ticket) error {
	if t.Parent != 0 {
		return i18n.Errorf("a step is merged with its lineage: see #%d", t.Parent)
	}
	if open := kb.OpenChildren(t); len(open) > 0 {
		ids := make([]string, len(open))
		for i, id := range open {
			ids[i] = "#" + itoa(id)
		}
		return i18n.Errorf("the lineage is not finished: %s", strings.Join(ids, ", "))
	}
	return nil
}

// startChild starts a child in the worktree and on the branch of its parent; its changes
// are counted from the commit it starts at.
func (s *K) startChild(ctx context.Context, k gctx, t *kb.Ticket, by string) error {
	p, err := s.Kanban.Get(k.loc, t.Parent)
	if err != nil {
		return err
	}
	if !k.exists(p.Worktree) {
		return i18n.Errorf("the lineage has no worktree: start #%d first", p.ID)
	}
	head, err := k.git.Head(ctx, p.Worktree)
	if err != nil {
		return err
	}
	if err := s.Kanban.SetGit(k.loc, t.ID, kb.GitState{Branch: &p.Branch, Base: &head, Worktree: &p.Worktree}); err != nil {
		return err
	}
	return s.Kanban.Event(k.loc, t.ID, by, "Started in the worktree of #{parent}, from {commit}", kb.Params{"parent": p.ID, "commit": short(head)})
}

func short(hash string) string {
	if len(hash) > 8 {
		return hash[:8]
	}
	return hash
}

// localBase is the local branch to start from when a dependency is merged there but not
// in the base yet (the merges of the IDE are never pushed); "" otherwise.
func (s *K) localBase(ctx context.Context, k gctx, t *kb.Ticket, base string) (string, int64) {
	local := k.git.LocalBranch(ctx, base)
	if local == base || len(t.DependsOn) == 0 {
		return "", 0
	}
	for _, id := range t.DependsOn {
		d, err := s.Kanban.Get(k.loc, id)
		if err != nil {
			continue
		}
		if d.Parent != 0 {
			if r, err := s.Kanban.Get(k.loc, d.Parent); err == nil {
				d = r
			}
		}
		if d.Branch != "" && k.git.Merged(ctx, d.Branch, local) && !k.git.Merged(ctx, d.Branch, base) {
			return local, id
		}
	}
	return "", 0
}

// activeStep is the ticket a worktree works on now: the child in progress, else the last
// child to test, else the ticket of the worktree itself.
func (s *K) activeStep(loc kb.Location, id int64) int64 {
	t, err := s.Kanban.Get(loc, id)
	if err != nil || len(t.Children) == 0 {
		return id
	}
	step := id
	for _, c := range t.Children {
		switch c.Status {
		case kb.InProgress:
			return c.ID
		case kb.ToTest:
			step = c.ID
		}
	}
	return step
}

// finishChild closes or abandons a child: its change is frozen from the commit it started
// at, and the worktree and the branch stay to the lineage.
func (s *K) finishChild(ctx context.Context, k gctx, t *kb.Ticket, status, comment string) (*kb.Ticket, error) {
	if status == kb.Done && k.exists(t.Worktree) && t.Base != "" {
		if d, err := k.git.Changes(ctx, t.Worktree, t.Branch, t.Base); err == nil {
			snap := &kb.Snapshot{Base: d.From, Head: d.Head, Files: d.Files, Patch: k.git.Patch(ctx, t.Worktree, t.Branch, d.From, 2<<20)}
			_ = s.Kanban.SetGit(k.loc, t.ID, kb.GitState{Snapshot: snap})
		}
	}
	if err := s.Kanban.Move(k.loc, t.ID, status, kb.ByUser, comment); err != nil {
		return nil, err
	}
	s.emitKanban(k.root.ID, t.ID, nil)
	s.emitKanban(k.root.ID, t.Parent, nil)
	return s.Kanban.Get(k.loc, t.ID)
}

// clientGit is the git side of the kanban seen from a window (no runtime: no git).
func (s *K) clientGit(c *client, root *projects.Project) gctx {
	k := gctx{loc: kanbanLoc(root), root: root}
	if rt, err := c.runtime(); err == nil {
		k.rt, k.git = rt, kb.Git{Run: rt.Runner, Root: root.Path}
	}
	return k
}
