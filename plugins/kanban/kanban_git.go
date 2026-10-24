package kanban

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"antennae.space/ide/lib/i18n"
	kb "antennae.space/ide/plugins/kanban/kanban"
	"antennae.space/ide/pod/commands"
	"antennae.space/ide/pod/execx"
	"antennae.space/ide/pod/plugins"
	"antennae.space/ide/pod/projects"
	"antennae.space/ide/pod/runtime"
)

// Git side of the tickets: worktree and branch of a ticket, its changes, and the end of
// its worktree when it is closed or abandoned (docs/design/kanban.md).
// gctx is what the git side of a ticket works on: the kanban, the main project, its
// runtime and git.
type gctx struct {
	loc  kb.Location
	root *projects.Project
	rt   *runtime.Runtime
	git  kb.Git
}

func (k gctx) exists(p string) bool {
	if p == "" {
		return false
	}
	_, err := k.rt.FS.Stat(p)
	return err == nil
}

func (s *K) registerKanbanGit() {
	h := func(f func(ctx context.Context, c *client, k gctx, p json.RawMessage) (any, error)) handler {
		return func(ctx context.Context, c *client, p json.RawMessage) (any, error) {
			root, err := s.kanbanProject(c.project)
			if err != nil {
				return nil, err
			}
			rt, err := c.runtime()
			if err != nil {
				return nil, err
			}
			return s.withTimes(rt, root.ID)(f(ctx, c, gctx{loc: kanbanLoc(root), root: root, rt: rt, git: kb.Git{Run: rt.Runner, Root: root.Path}}, p))
		}
	}
	exists := func(k gctx, p string) bool { return k.exists(p) }
	type idArg struct {
		ID    int64  `json:"id"`
		Base  string `json:"base"`
		Force bool   `json:"force"`
	}

	s.handle("kanban.start", h(func(ctx context.Context, c *client, k gctx, p json.RawMessage) (any, error) {
		a, err := bind[idArg](p)
		if err != nil {
			return nil, err
		}
		t, child, err := s.startTicket(ctx, k, a.ID, a.Base, kb.ByUser, a.Force)
		if err != nil {
			return nil, err
		}
		return map[string]any{"ticket": t, "project": child}, nil
	}))

	// kanban.open returns the project of the worktree of a ticket (registered again if needed).
	s.handle("kanban.open", h(func(ctx context.Context, c *client, k gctx, p json.RawMessage) (any, error) {
		a, err := bind[idArg](p)
		if err != nil {
			return nil, err
		}
		t, err := s.Kanban.Get(k.loc, a.ID)
		if err != nil {
			return nil, err
		}
		if !exists(k, t.Worktree) {
			return nil, i18n.New("this ticket has no worktree")
		}
		if t.Parent != 0 {
			if t, err = s.Kanban.Get(k.loc, t.Parent); err != nil {
				return nil, err
			}
		}
		child, err := s.Projects.PutChild(k.root, t.ID, "#"+itoa(t.ID)+" "+t.Title, t.Worktree)
		if err != nil {
			return nil, err
		}
		return map[string]any{"project": child.ID}, nil
	}))

	s.handle("kanban.diff", h(func(ctx context.Context, c *client, k gctx, p json.RawMessage) (any, error) {
		a, err := bind[idArg](p)
		if err != nil {
			return nil, err
		}
		t, err := s.Kanban.Get(k.loc, a.ID)
		if err != nil {
			return nil, err
		}
		base := firstOf(a.Base, t.Base)
		if base == "" {
			base = k.git.DefaultBase(ctx)
		}
		// A closed ticket shows the change frozen at its closing (its branch may be merged).
		switch {
		case t.Parent != 0 && t.Snapshot != nil && (t.Status == kb.Done || t.Status == kb.Abandoned):
			return &kb.Diff{Base: base, From: t.Snapshot.Base, Head: t.Snapshot.Head, Files: t.Snapshot.Files, Source: "snapshot"}, nil
		case exists(k, t.Worktree):
			d, err := k.git.Changes(ctx, t.Worktree, t.Branch, base)
			// Merged: nothing left against the base, the change is the one frozen by the merge
			// (with what the worktree may still hold, uncommitted).
			if err != nil || t.Snapshot == nil || d.Ahead > 0 || !k.git.Merged(ctx, t.Branch, k.git.LocalBranch(ctx, base)) {
				return d, err
			}
			return &kb.Diff{Base: base, From: t.Snapshot.Base, Head: t.Snapshot.Head, Files: t.Snapshot.Files, Source: "snapshot", Dirty: d.Dirty}, nil
		case t.Snapshot != nil:
			return &kb.Diff{Base: base, From: t.Snapshot.Base, Head: t.Snapshot.Head, Files: t.Snapshot.Files, Source: "snapshot"}, nil
		case t.Branch != "" && k.git.IsRepo(ctx):
			return k.git.Changes(ctx, "", t.Branch, base)
		}
		return nil, nil
	}))

	s.handle("kanban.diff.file", h(func(ctx context.Context, c *client, k gctx, p json.RawMessage) (any, error) {
		a, err := bind[struct {
			ID     int64  `json:"id"`
			Path   string `json:"path"`
			From   string `json:"from"`
			Source string `json:"source"`
		}](p)
		if err != nil {
			return nil, err
		}
		t, err := s.Kanban.Get(k.loc, a.ID)
		if err != nil {
			return nil, err
		}
		if a.Source == "snapshot" && t.Snapshot != nil {
			return snapshotFile(t.Snapshot.Patch, a.Path), nil
		}
		wt := ""
		if exists(k, t.Worktree) {
			wt = t.Worktree
		}
		return k.git.FilePatch(ctx, wt, t.Branch, a.From, a.Path)
	}))

	// State of the branch of a ticket: rebase in its worktree, merge in the main folder.
	type gitInfo struct {
		Worktree *kb.State `json:"worktree,omitempty"`
		Main     kb.State  `json:"main"`
		Into     string    `json:"into"`
		Merged   bool      `json:"merged"`
		CanPR    bool      `json:"canPR"`
	}
	info := func(ctx context.Context, k gctx, t *kb.Ticket) gitInfo {
		base := firstOf(t.Base)
		if base == "" {
			base = k.git.DefaultBase(ctx)
		}
		gi := gitInfo{Main: k.git.State(ctx, k.root.Path), Into: k.git.LocalBranch(ctx, base), CanPR: k.git.CanPR(ctx)}
		if exists(k, t.Worktree) {
			st := k.git.State(ctx, t.Worktree)
			gi.Worktree = &st
		}
		if t.Branch != "" {
			gi.Merged = k.git.Merged(ctx, t.Branch, gi.Into)
		}
		return gi
	}
	ticketOf := func(k gctx, p json.RawMessage) (*kb.Ticket, error) {
		a, err := bind[idArg](p)
		if err != nil {
			return nil, err
		}
		return s.Kanban.Get(k.loc, a.ID)
	}
	s.handle("kanban.gitstate", h(func(ctx context.Context, c *client, k gctx, p json.RawMessage) (any, error) {
		t, err := ticketOf(k, p)
		if err != nil {
			return nil, err
		}
		if t.Branch == "" || !k.git.IsRepo(ctx) {
			return nil, nil
		}
		return info(ctx, k, t), nil
	}))
	// After a git operation: event in the history, windows told (git panels included).
	done := func(c *client, k gctx, t *kb.Ticket, key string, p kb.Params) {
		_ = s.Kanban.Event(k.loc, t.ID, kb.ByUser, key, p)
		s.emitKanban(k.root.ID, t.ID, nil)
		s.pod.Emit(k.root.ID, "git.changed", nil)
		s.pod.Emit(projects.ChildID(k.root.ID, t.ID), "git.changed", nil)
	}
	s.handle("kanban.merge", h(func(ctx context.Context, c *client, k gctx, p json.RawMessage) (any, error) {
		a, err := bind[struct {
			ID     int64 `json:"id"`
			Squash bool  `json:"squash"`
		}](p)
		if err != nil {
			return nil, err
		}
		t, err := s.Kanban.Get(k.loc, a.ID)
		if err != nil {
			return nil, err
		}
		if t.Branch == "" {
			return nil, i18n.New("this ticket has no branch")
		}
		if err := lineageOpen(t); err != nil {
			return nil, err
		}
		gi := info(ctx, k, t)
		msg := "Merge #" + itoa(t.ID) + " " + t.Title + " (" + t.Branch + ")"
		if a.Squash {
			msg = "#" + itoa(t.ID) + " " + t.Title
		}
		// The change is frozen before the merge: afterwards the branch has nothing left
		// against its base.
		var snap *kb.Snapshot
		if d, err := k.git.Changes(ctx, "", t.Branch, gi.Into); err == nil && len(d.Files) > 0 {
			snap = &kb.Snapshot{Base: d.From, Head: d.Head, Files: d.Files, Patch: k.git.Patch(ctx, "", t.Branch, d.From, 2<<20)}
		}
		st, err := k.git.Merge(ctx, t.Branch, gi.Into, msg, a.Squash)
		if err != nil {
			return nil, err
		}
		if snap != nil && len(st.Conflicts) == 0 {
			_ = s.Kanban.SetGit(k.loc, t.ID, kb.GitState{Snapshot: snap})
		}
		how := "merge --no-ff"
		if a.Squash {
			how = "squash"
		}
		if len(st.Conflicts) > 0 {
			done(c, k, t, "Merge into {branch} ({how}) stopped: {n} file(s) in conflict", kb.Params{"branch": gi.Into, "how": how, "n": len(st.Conflicts)})
		} else {
			done(c, k, t, "Branch merged into {branch} ({how})", kb.Params{"branch": gi.Into, "how": how})
		}
		return info(ctx, k, t), nil
	}))
	// kanban.pr pushes the branch of the ticket and opens its pull request with gh.
	s.handle("kanban.pr", h(func(ctx context.Context, c *client, k gctx, p json.RawMessage) (any, error) {
		t, err := ticketOf(k, p)
		if err != nil {
			return nil, err
		}
		if t.Branch == "" {
			return nil, i18n.New("this ticket has no branch")
		}
		if err := lineageOpen(t); err != nil {
			return nil, err
		}
		if !k.git.CanPR(ctx) {
			return nil, i18n.New("a pull request needs a remote “origin” and the gh command")
		}
		base := firstOf(t.Base)
		if base == "" {
			base = k.git.DefaultBase(ctx)
		}
		url, err := k.git.OpenPR(ctx, t.Branch, base, "#"+itoa(t.ID)+" "+t.Title, kb.PRBody(t, c.language()))
		if err != nil {
			return nil, err
		}
		if err := s.Kanban.SetGit(k.loc, t.ID, kb.GitState{PR: &url}); err != nil {
			return nil, err
		}
		done(c, k, t, "Pull request opened: {url}", kb.Params{"url": url})
		return s.Kanban.Get(k.loc, t.ID)
	}))
	s.handle("kanban.rebase", h(func(ctx context.Context, c *client, k gctx, p json.RawMessage) (any, error) {
		t, err := ticketOf(k, p)
		if err != nil {
			return nil, err
		}
		if !exists(k, t.Worktree) {
			return nil, i18n.New("this ticket has no worktree")
		}
		if t.Parent != 0 {
			return nil, i18n.Errorf("a step is rebased with its lineage: see #%d", t.Parent)
		}
		_ = k.git.Fetch(ctx)
		base := firstOf(t.Base)
		if base == "" {
			base = k.git.DefaultBase(ctx)
		}
		st, err := k.git.Rebase(ctx, t.Worktree, base)
		if err != nil {
			return nil, err
		}
		if len(st.Conflicts) > 0 {
			done(c, k, t, "Rebase on {base} stopped: {n} file(s) in conflict", kb.Params{"base": base, "n": len(st.Conflicts)})
		} else {
			done(c, k, t, "Branch rebased on {base}", kb.Params{"base": base})
		}
		return info(ctx, k, t), nil
	}))
	// Continue or abort the operation in progress: where is "worktree" or "main".
	folder := func(k gctx, t *kb.Ticket, where string) (string, error) {
		if where == "main" {
			return k.root.Path, nil
		}
		if !exists(k, t.Worktree) {
			return "", i18n.New("this ticket has no worktree")
		}
		return t.Worktree, nil
	}
	for _, op := range []string{"continue", "abort"} {
		op := op
		s.handle("kanban."+op, h(func(ctx context.Context, c *client, k gctx, p json.RawMessage) (any, error) {
			a, err := bind[struct {
				ID    int64  `json:"id"`
				Where string `json:"where"`
			}](p)
			if err != nil {
				return nil, err
			}
			t, err := s.Kanban.Get(k.loc, a.ID)
			if err != nil {
				return nil, err
			}
			dir, err := folder(k, t, a.Where)
			if err != nil {
				return nil, err
			}
			what := "Rebase"
			if a.Where == "main" {
				what = "Merge"
			}
			if op == "abort" {
				if err := k.git.Abort(ctx, dir); err != nil {
					return nil, err
				}
				done(c, k, t, what+" aborted", nil)
				return info(ctx, k, t), nil
			}
			st, err := k.git.Continue(ctx, dir)
			if err != nil {
				return nil, err
			}
			if len(st.Conflicts) > 0 {
				done(c, k, t, what+": new conflicts ({n} file(s))", kb.Params{"n": len(st.Conflicts)})
			} else if !st.Busy() {
				done(c, k, t, what+" finished", nil)
			}
			return info(ctx, k, t), nil
		}))
	}

	// kanban.finish closes (done) or abandons a ticket: the change is frozen in the ticket,
	// the worktree and its project are removed; the branch is kept unless asked.
	s.handle("kanban.finish", h(func(ctx context.Context, c *client, k gctx, p json.RawMessage) (any, error) {
		a, err := bind[struct {
			ID           int64  `json:"id"`
			Status       string `json:"status"`
			Comment      string `json:"comment"`
			DeleteBranch bool   `json:"deleteBranch"`
			// Lineage: abandoning a ticket abandons its open children too.
			Lineage bool `json:"lineage"`
		}](p)
		if err != nil {
			return nil, err
		}
		if a.Status != kb.Done && a.Status != kb.Abandoned {
			return nil, i18n.New("unknown final status")
		}
		t, err := s.Kanban.Get(k.loc, a.ID)
		if err != nil {
			return nil, err
		}
		if !kb.CanMove(t.Status, a.Status, kb.ByUser) {
			return nil, i18n.New("this ticket cannot move to this status")
		}
		// Before the worktree goes: the open test feedback keeps the ticket To test.
		if a.Status == kb.Done && t.FeedbackOpen > 0 {
			return nil, i18n.Errorf("%d test feedback(s) not handled yet", t.FeedbackOpen)
		}
		if t.Parent != 0 {
			return s.finishChild(ctx, k, t, a.Status, a.Comment)
		}
		if open := kb.OpenChildren(t); len(open) > 0 {
			if a.Status == kb.Done || !a.Lineage {
				return nil, lineageOpen(t)
			}
			for _, id := range open {
				if err := s.Kanban.Move(k.loc, id, kb.Abandoned, kb.ByUser, a.Comment); err != nil {
					return nil, err
				}
				s.emitKanban(k.root.ID, id, nil)
			}
		}
		wt := ""
		if exists(k, t.Worktree) {
			wt = t.Worktree
		}
		if a.Status == kb.Done && t.Branch != "" && k.git.IsRepo(ctx) {
			base := firstOf(t.Base)
			if base == "" {
				base = k.git.DefaultBase(ctx)
			}
			// Already merged: keep the change frozen by the merge.
			merged := t.Snapshot != nil && k.git.Merged(ctx, t.Branch, k.git.LocalBranch(ctx, base))
			if d, err := k.git.Changes(ctx, wt, t.Branch, base); err == nil && !merged {
				snap := &kb.Snapshot{Base: d.From, Head: d.Head, Files: d.Files, Patch: k.git.Patch(ctx, wt, t.Branch, d.From, 2<<20)}
				_ = s.Kanban.SetGit(k.loc, t.ID, kb.GitState{Snapshot: snap})
			}
		}
		if err := s.removeWorktree(ctx, k.rt, k.loc, k.root, k.git, t, wt); err != nil {
			return nil, err
		}
		if a.DeleteBranch && t.Branch != "" {
			if err := k.git.DeleteBranch(ctx, t.Branch); err != nil {
				_ = s.Kanban.Event(k.loc, t.ID, kb.ByUser, "Branch not deleted: {error}", kb.Params{"error": err.Error()})
			} else {
				empty := ""
				_ = s.Kanban.SetGit(k.loc, t.ID, kb.GitState{Branch: &empty})
				_ = s.Kanban.Event(k.loc, t.ID, kb.ByUser, "Branch {branch} deleted", kb.Params{"branch": t.Branch})
			}
		}
		if err := s.Kanban.Move(k.loc, t.ID, a.Status, kb.ByUser, a.Comment); err != nil {
			return nil, err
		}
		s.emitKanban(k.root.ID, t.ID, nil)
		return s.Kanban.Get(k.loc, t.ID)
	}))
}

// removeWorktree removes the worktree of a ticket and the project opened on it.
func (s *K) removeWorktree(ctx context.Context, rt *runtime.Runtime, loc kb.Location, root *projects.Project, g kb.Git, t *kb.Ticket, wt string) error {
	child := projects.ChildID(root.ID, t.ID)
	if _, ok := s.Projects.Get(child); ok {
		s.pod.CloseRuntime(child)
		_ = s.Projects.Delete(child)
		s.Desks.Delete(child)
	}
	// Its agent sessions end with it, even when no window had it open.
	if ag := s.agents(rt); ag != nil {
		ag.Purge(child)
	}
	if wt != "" {
		if err := g.RemoveWorktree(ctx, wt); err != nil {
			return err
		}
		_ = s.Kanban.Event(loc, t.ID, kb.ByUser, "Worktree removed", nil)
	}
	if t.Worktree != "" {
		empty := ""
		return s.Kanban.SetGit(loc, t.ID, kb.GitState{Worktree: &empty})
	}
	return nil
}

// startTicket starts the development of a ticket (Start development, or Claude Code
// through the MCP endpoint): branch and worktree created from the base, setup commands,
// ticket In progress, worktree registered as a child project.
func (s *K) startTicket(ctx context.Context, k gctx, id int64, base0, by string, force bool) (*kb.Ticket, string, error) {
	t, err := s.Kanban.Get(k.loc, id)
	if err != nil {
		return nil, "", err
	}
	if t.Status == kb.Done || t.Status == kb.Abandoned || t.Status == kb.New {
		return nil, "", i18n.New("the ticket must be “To do”, “In progress” or “To test”")
	}
	// The worktree of a development is the Git plugin's capability.
	if k.rt != nil && !s.pod.CapabilityOn(k.rt, "worktrees") {
		return nil, "", i18n.New("starting a development needs the Git plugin, off on this project (Settings > Plugins)")
	}
	// Only the user may go past the order of the lineages, and it stays in the history.
	var forced []kb.Blocker
	if t.Status == kb.Todo {
		bl, err := s.ticketBlockers(ctx, k, t.ID)
		if err != nil {
			return nil, "", err
		}
		if len(bl) > 0 && (!force || by != kb.ByUser) {
			return nil, "", blockedError(bl)
		}
		forced = bl
	}
	// The worktree of a lineage is the one of its root.
	owner := t
	if t.Parent != 0 {
		if !k.exists(t.Worktree) {
			if err := s.startChild(ctx, k, t, by); err != nil {
				return nil, "", err
			}
		}
		if owner, err = s.Kanban.Get(k.loc, t.Parent); err != nil {
			return nil, "", err
		}
	} else if !k.exists(t.Worktree) {
		if !k.git.IsRepo(ctx) {
			return nil, "", &plugins.CodedError{Code: "not_git", Err: i18n.New("the project is not a git repository: cannot create the branch of the ticket")}
		}
		if !k.git.HasCommit(ctx) {
			return nil, "", &plugins.CodedError{Code: "not_git", Err: i18n.New("the repository has no commit yet: cannot create the branch of the ticket")}
		}
		if err := s.ignoreWorktrees(k.root, k.rt); err != nil {
			return nil, "", err
		}
		fetchErr := k.git.Fetch(ctx)
		meta, _ := s.Kanban.Meta(k.loc)
		base := firstOf(base0, t.Base, meta["base"])
		if base == "" {
			base = k.git.DefaultBase(ctx)
		}
		if local, dep := s.localBase(ctx, k, t, base); local != "" {
			_ = s.Kanban.Event(k.loc, t.ID, by, "Started from {local}: it holds #{dep}, not in {base} yet", kb.Params{"local": local, "dep": dep, "base": base})
			base = local
		}
		branch, dir := k.git.Names(t.ID, t.Title)
		if t.Branch != "" {
			branch = t.Branch
		}
		if err := k.git.AddWorktree(ctx, dir, branch, base); err != nil {
			return nil, "", err
		}
		setup, setupErr := s.setupScript(k.rt)
		state := ""
		if setup != "" {
			state = "running"
		} else if setupErr != nil {
			state = "error"
		}
		git := kb.GitState{Branch: &branch, Base: &base, Worktree: &dir, Setup: &state}
		if setupErr != nil {
			msg := setupErr.Error()
			git.SetupLog = &msg
		}
		if err := s.Kanban.SetGit(k.loc, t.ID, git); err != nil {
			return nil, "", err
		}
		if fetchErr != nil {
			_ = s.Kanban.Event(k.loc, t.ID, by, "Worktree created: branch {branch} from {base} (git fetch failed: {error})", kb.Params{"branch": branch, "base": base, "error": fetchErr.Error()})
		} else {
			_ = s.Kanban.Event(k.loc, t.ID, by, "Worktree created: branch {branch} from {base}", kb.Params{"branch": branch, "base": base})
		}
		if setupErr != nil {
			_ = s.Kanban.Event(k.loc, t.ID, by, "Worktree setup failed: {error}", kb.Params{"error": setupErr.Error()})
		}
		if setup != "" {
			go s.runSetup(k.loc, k.root.ID, t.ID, dir, setup, k.rt.Runner)
		}
	}
	if t.Status == kb.Todo {
		if len(forced) > 0 {
			_ = s.Kanban.Event(k.loc, t.ID, by, "Started despite: {blockers}", kb.Params{"blockers": kb.BlockersText(forced)})
		}
		if err := s.Kanban.Move(k.loc, t.ID, kb.InProgress, by, ""); err != nil {
			return nil, "", err
		}
	}
	t, err = s.Kanban.Get(k.loc, id)
	if err != nil {
		return nil, "", err
	}
	if owner.ID == t.ID {
		owner = t
	} else if owner, err = s.Kanban.Get(k.loc, owner.ID); err != nil {
		return nil, "", err
	}
	child, err := s.Projects.PutChild(k.root, owner.ID, "#"+itoa(owner.ID)+" "+owner.Title, owner.Worktree)
	if err != nil {
		return nil, "", err
	}
	s.emitKanban(k.root.ID, t.ID, nil)
	return t, child.ID, nil
}

// ignoreWorktrees keeps .ide, its worktrees included, out of the repository (also on an
// SSH host).
func (s *K) ignoreWorktrees(root *projects.Project, rt *runtime.Runtime) error {
	return rt.KeepIdeOut(root)
}

// setupScript is what sets a new worktree of the project of rt up: its setup commands.
func (s *K) setupScript(rt *runtime.Runtime) (string, error) {
	list, err := s.projectCommands(rt)
	if err != nil {
		return "", err
	}
	return commands.SetupScript(list), nil
}

// runSetup runs the setup commands of the project in a new worktree (npm install…).
func (s *K) runSetup(loc kb.Location, rootID string, id int64, dir, cmd string, run execx.Runner) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	out, err := run.Output(ctx, []string{"sh", "-c", "(" + cmd + ") 2>&1"}, dir)
	log := string(out)
	if len(log) > 20000 {
		log = "…\n" + log[len(log)-20000:]
	}
	state, key, p := "ok", "Worktree setup finished", kb.Params(nil)
	if err != nil {
		state, key, p = "error", "Worktree setup failed: {error}", kb.Params{"error": err.Error()}
		log += "\n" + err.Error()
	}
	_ = s.Kanban.SetGit(loc, id, kb.GitState{Setup: &state, SetupLog: &log})
	_ = s.Kanban.Event(loc, id, kb.ByUser, key, p)
	s.emitKanban(rootID, id, nil)
}

// snapshotFile extracts the diff of one file from a whole patch.
func snapshotFile(patch, p string) string {
	var b strings.Builder
	in := false
	for _, line := range strings.SplitAfter(patch, "\n") {
		if strings.HasPrefix(line, "diff --git ") {
			in = strings.HasSuffix(strings.TrimSpace(line), " b/"+p)
		}
		if in {
			b.WriteString(line)
		}
	}
	return b.String()
}

func firstOf(v ...string) string {
	for _, x := range v {
		if strings.TrimSpace(x) != "" {
			return strings.TrimSpace(x)
		}
	}
	return ""
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }
