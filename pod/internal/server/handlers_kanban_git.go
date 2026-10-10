package server

import (
	"context"
	"encoding/json"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/DrSmithFr/web-ide/pod/internal/execx"
	"github.com/DrSmithFr/web-ide/pod/internal/i18n"
	"github.com/DrSmithFr/web-ide/pod/internal/kanban"
	"github.com/DrSmithFr/web-ide/pod/internal/projects"
	"github.com/DrSmithFr/web-ide/pod/internal/runtime"
)

// Git side of the tickets: worktree and branch of a ticket, its changes, and the end of
// its worktree when it is closed or abandoned (docs/kanban.md).
// gctx is what the git side of a ticket works on: the kanban, the main project, its
// runtime and git.
type gctx struct {
	loc  kanban.Location
	root *projects.Project
	rt   *runtime.Runtime
	git  kanban.Git
}

func (k gctx) exists(p string) bool {
	if p == "" {
		return false
	}
	_, err := k.rt.FS.Stat(p)
	return err == nil
}

func (s *Server) registerKanbanGit() {
	h := func(f func(ctx context.Context, c *Client, k gctx, p json.RawMessage) (any, error)) handler {
		return func(ctx context.Context, c *Client, p json.RawMessage) (any, error) {
			root, err := s.kanbanProject(c.project)
			if err != nil {
				return nil, err
			}
			rt, err := c.runtime()
			if err != nil {
				return nil, err
			}
			return f(ctx, c, gctx{loc: kanbanLoc(root), root: root, rt: rt, git: kanban.Git{Run: rt.Runner, Root: root.Path}}, p)
		}
	}
	exists := func(k gctx, p string) bool { return k.exists(p) }
	type idArg struct {
		ID    int64  `json:"id"`
		Base  string `json:"base"`
		Force bool   `json:"force"`
	}

	s.handle("kanban.start", h(func(ctx context.Context, c *Client, k gctx, p json.RawMessage) (any, error) {
		a, err := bind[idArg](p)
		if err != nil {
			return nil, err
		}
		t, child, err := s.startTicket(ctx, k, a.ID, a.Base, kanban.ByUser, a.Force)
		if err != nil {
			return nil, err
		}
		return map[string]any{"ticket": t, "project": child}, nil
	}))

	// kanban.open returns the project of the worktree of a ticket (registered again if needed).
	s.handle("kanban.open", h(func(ctx context.Context, c *Client, k gctx, p json.RawMessage) (any, error) {
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

	s.handle("kanban.diff", h(func(ctx context.Context, c *Client, k gctx, p json.RawMessage) (any, error) {
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
		case t.Parent != 0 && t.Snapshot != nil && (t.Status == kanban.Done || t.Status == kanban.Abandoned):
			return &kanban.Diff{Base: base, From: t.Snapshot.Base, Head: t.Snapshot.Head, Files: t.Snapshot.Files, Source: "snapshot"}, nil
		case exists(k, t.Worktree):
			d, err := k.git.Changes(ctx, t.Worktree, t.Branch, base)
			// Merged: nothing left against the base, the change is the one frozen by the merge
			// (with what the worktree may still hold, uncommitted).
			if err != nil || t.Snapshot == nil || d.Ahead > 0 || !k.git.Merged(ctx, t.Branch, k.git.LocalBranch(ctx, base)) {
				return d, err
			}
			return &kanban.Diff{Base: base, From: t.Snapshot.Base, Head: t.Snapshot.Head, Files: t.Snapshot.Files, Source: "snapshot", Dirty: d.Dirty}, nil
		case t.Snapshot != nil:
			return &kanban.Diff{Base: base, From: t.Snapshot.Base, Head: t.Snapshot.Head, Files: t.Snapshot.Files, Source: "snapshot"}, nil
		case t.Branch != "" && k.git.IsRepo(ctx):
			return k.git.Changes(ctx, "", t.Branch, base)
		}
		return nil, nil
	}))

	s.handle("kanban.diff.file", h(func(ctx context.Context, c *Client, k gctx, p json.RawMessage) (any, error) {
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
		Worktree *kanban.State `json:"worktree,omitempty"`
		Main     kanban.State  `json:"main"`
		Into     string        `json:"into"`
		Merged   bool          `json:"merged"`
		CanPR    bool          `json:"canPR"`
	}
	info := func(ctx context.Context, k gctx, t *kanban.Ticket) gitInfo {
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
	ticketOf := func(k gctx, p json.RawMessage) (*kanban.Ticket, error) {
		a, err := bind[idArg](p)
		if err != nil {
			return nil, err
		}
		return s.Kanban.Get(k.loc, a.ID)
	}
	s.handle("kanban.gitstate", h(func(ctx context.Context, c *Client, k gctx, p json.RawMessage) (any, error) {
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
	done := func(c *Client, k gctx, t *kanban.Ticket, key string, p kanban.Params) {
		_ = s.Kanban.Event(k.loc, t.ID, kanban.ByUser, key, p)
		s.emitKanban(k.root.ID, t.ID, nil)
		s.emitter(k.root.ID)("git.changed", nil, "")
		s.emitter(projects.ChildID(k.root.ID, t.ID))("git.changed", nil, "")
	}
	s.handle("kanban.merge", h(func(ctx context.Context, c *Client, k gctx, p json.RawMessage) (any, error) {
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
		var snap *kanban.Snapshot
		if d, err := k.git.Changes(ctx, "", t.Branch, gi.Into); err == nil && len(d.Files) > 0 {
			snap = &kanban.Snapshot{Base: d.From, Head: d.Head, Files: d.Files, Patch: k.git.Patch(ctx, "", t.Branch, d.From, 2<<20)}
		}
		st, err := k.git.Merge(ctx, t.Branch, gi.Into, msg, a.Squash)
		if err != nil {
			return nil, err
		}
		if snap != nil && len(st.Conflicts) == 0 {
			_ = s.Kanban.SetGit(k.loc, t.ID, kanban.GitState{Snapshot: snap})
		}
		how := "merge --no-ff"
		if a.Squash {
			how = "squash"
		}
		if len(st.Conflicts) > 0 {
			done(c, k, t, "Merge into {branch} ({how}) stopped: {n} file(s) in conflict", kanban.Params{"branch": gi.Into, "how": how, "n": len(st.Conflicts)})
		} else {
			done(c, k, t, "Branch merged into {branch} ({how})", kanban.Params{"branch": gi.Into, "how": how})
		}
		return info(ctx, k, t), nil
	}))
	// kanban.pr pushes the branch of the ticket and opens its pull request with gh.
	s.handle("kanban.pr", h(func(ctx context.Context, c *Client, k gctx, p json.RawMessage) (any, error) {
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
		url, err := k.git.OpenPR(ctx, t.Branch, base, "#"+itoa(t.ID)+" "+t.Title, kanban.PRBody(t, c.language()))
		if err != nil {
			return nil, err
		}
		if err := s.Kanban.SetGit(k.loc, t.ID, kanban.GitState{PR: &url}); err != nil {
			return nil, err
		}
		done(c, k, t, "Pull request opened: {url}", kanban.Params{"url": url})
		return s.Kanban.Get(k.loc, t.ID)
	}))
	s.handle("kanban.rebase", h(func(ctx context.Context, c *Client, k gctx, p json.RawMessage) (any, error) {
		t, err := ticketOf(k, p)
		if err != nil {
			return nil, err
		}
		if !exists(k, t.Worktree) {
			return nil, i18n.New("this ticket has no worktree")
		}
		if t.Parent != 0 {
			return nil, i18n.Errorf("a child ticket is rebased with its lineage: see #%d", t.Parent)
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
			done(c, k, t, "Rebase on {base} stopped: {n} file(s) in conflict", kanban.Params{"base": base, "n": len(st.Conflicts)})
		} else {
			done(c, k, t, "Branch rebased on {base}", kanban.Params{"base": base})
		}
		return info(ctx, k, t), nil
	}))
	// Continue or abort the operation in progress: where is "worktree" or "main".
	folder := func(k gctx, t *kanban.Ticket, where string) (string, error) {
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
		s.handle("kanban."+op, h(func(ctx context.Context, c *Client, k gctx, p json.RawMessage) (any, error) {
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
				done(c, k, t, what+": new conflicts ({n} file(s))", kanban.Params{"n": len(st.Conflicts)})
			} else if !st.Busy() {
				done(c, k, t, what+" finished", nil)
			}
			return info(ctx, k, t), nil
		}))
	}

	// kanban.finish closes (done) or abandons a ticket: the change is frozen in the ticket,
	// the worktree and its project are removed; the branch is kept unless asked.
	s.handle("kanban.finish", h(func(ctx context.Context, c *Client, k gctx, p json.RawMessage) (any, error) {
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
		if a.Status != kanban.Done && a.Status != kanban.Abandoned {
			return nil, i18n.New("unknown final status")
		}
		t, err := s.Kanban.Get(k.loc, a.ID)
		if err != nil {
			return nil, err
		}
		if !kanban.CanMove(t.Status, a.Status, kanban.ByUser) {
			return nil, i18n.New("this ticket cannot move to this status")
		}
		// Before the worktree goes: the open test feedback keeps the ticket To test.
		if a.Status == kanban.Done && t.FeedbackOpen > 0 {
			return nil, i18n.Errorf("%d test feedback(s) not handled yet", t.FeedbackOpen)
		}
		if t.Parent != 0 {
			return s.finishChild(ctx, k, t, a.Status, a.Comment)
		}
		if open := kanban.OpenChildren(t); len(open) > 0 {
			if a.Status == kanban.Done || !a.Lineage {
				return nil, lineageOpen(t)
			}
			for _, id := range open {
				if err := s.Kanban.Move(k.loc, id, kanban.Abandoned, kanban.ByUser, a.Comment); err != nil {
					return nil, err
				}
				s.emitKanban(k.root.ID, id, nil)
			}
		}
		wt := ""
		if exists(k, t.Worktree) {
			wt = t.Worktree
		}
		if a.Status == kanban.Done && t.Branch != "" && k.git.IsRepo(ctx) {
			base := firstOf(t.Base)
			if base == "" {
				base = k.git.DefaultBase(ctx)
			}
			// Already merged: keep the change frozen by the merge.
			merged := t.Snapshot != nil && k.git.Merged(ctx, t.Branch, k.git.LocalBranch(ctx, base))
			if d, err := k.git.Changes(ctx, wt, t.Branch, base); err == nil && !merged {
				snap := &kanban.Snapshot{Base: d.From, Head: d.Head, Files: d.Files, Patch: k.git.Patch(ctx, wt, t.Branch, d.From, 2<<20)}
				_ = s.Kanban.SetGit(k.loc, t.ID, kanban.GitState{Snapshot: snap})
			}
		}
		if err := s.removeWorktree(ctx, k.loc, k.root, k.git, t, wt); err != nil {
			return nil, err
		}
		if a.DeleteBranch && t.Branch != "" {
			if err := k.git.DeleteBranch(ctx, t.Branch); err != nil {
				_ = s.Kanban.Event(k.loc, t.ID, kanban.ByUser, "Branch not deleted: {error}", kanban.Params{"error": err.Error()})
			} else {
				empty := ""
				_ = s.Kanban.SetGit(k.loc, t.ID, kanban.GitState{Branch: &empty})
				_ = s.Kanban.Event(k.loc, t.ID, kanban.ByUser, "Branch {branch} deleted", kanban.Params{"branch": t.Branch})
			}
		}
		if err := s.Kanban.Move(k.loc, t.ID, a.Status, kanban.ByUser, a.Comment); err != nil {
			return nil, err
		}
		s.emitKanban(k.root.ID, t.ID, nil)
		return s.Kanban.Get(k.loc, t.ID)
	}))
}

// removeWorktree removes the worktree of a ticket and the project opened on it.
func (s *Server) removeWorktree(ctx context.Context, loc kanban.Location, root *projects.Project, g kanban.Git, t *kanban.Ticket, wt string) error {
	child := projects.ChildID(root.ID, t.ID)
	if _, ok := s.Projects.Get(child); ok {
		s.closeRuntime(child)
		_ = s.Projects.Delete(child)
		s.Sessions.Delete(child)
	}
	if wt != "" {
		if err := g.RemoveWorktree(ctx, wt); err != nil {
			return err
		}
		_ = s.Kanban.Event(loc, t.ID, kanban.ByUser, "Worktree removed", nil)
	}
	if t.Worktree != "" {
		empty := ""
		return s.Kanban.SetGit(loc, t.ID, kanban.GitState{Worktree: &empty})
	}
	return nil
}

// ignoreWorktrees keeps .ide/worktrees out of the repository (also on an SSH host).
// startTicket starts the development of a ticket (Start development, or Claude Code
// through the MCP endpoint): branch and worktree created from the base, setup command,
// ticket In progress, worktree registered as a child project.
func (s *Server) startTicket(ctx context.Context, k gctx, id int64, base0, by string, force bool) (*kanban.Ticket, string, error) {
	t, err := s.Kanban.Get(k.loc, id)
	if err != nil {
		return nil, "", err
	}
	if t.Status == kanban.Done || t.Status == kanban.Abandoned || t.Status == kanban.New {
		return nil, "", i18n.New("the ticket must be “To do”, “In progress” or “To test”")
	}
	// Only the user may go past the order of the lineages, and it stays in the history.
	var forced []kanban.Blocker
	if t.Status == kanban.Todo {
		bl, err := s.ticketBlockers(ctx, k, t.ID)
		if err != nil {
			return nil, "", err
		}
		if len(bl) > 0 && (!force || by != kanban.ByUser) {
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
			return nil, "", &codeError{"not_git", i18n.New("the project is not a git repository: cannot create the branch of the ticket")}
		}
		if !k.git.HasCommit(ctx) {
			return nil, "", &codeError{"not_git", i18n.New("the repository has no commit yet: cannot create the branch of the ticket")}
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
			_ = s.Kanban.Event(k.loc, t.ID, by, "Started from {local}: it holds #{dep}, not in {base} yet", kanban.Params{"local": local, "dep": dep, "base": base})
			base = local
		}
		branch, dir := k.git.Names(t.ID, t.Title)
		if t.Branch != "" {
			branch = t.Branch
		}
		if err := k.git.AddWorktree(ctx, dir, branch, base); err != nil {
			return nil, "", err
		}
		setup := strings.TrimSpace(meta["setup"])
		state := ""
		if setup != "" {
			state = "running"
		}
		if err := s.Kanban.SetGit(k.loc, t.ID, kanban.GitState{Branch: &branch, Base: &base, Worktree: &dir, Setup: &state}); err != nil {
			return nil, "", err
		}
		if fetchErr != nil {
			_ = s.Kanban.Event(k.loc, t.ID, by, "Worktree created: branch {branch} from {base} (git fetch failed: {error})", kanban.Params{"branch": branch, "base": base, "error": fetchErr.Error()})
		} else {
			_ = s.Kanban.Event(k.loc, t.ID, by, "Worktree created: branch {branch} from {base}", kanban.Params{"branch": branch, "base": base})
		}
		if setup != "" {
			go s.runSetup(k.loc, k.root.ID, t.ID, dir, setup, k.rt.Runner)
		}
	}
	if t.Status == kanban.Todo {
		if len(forced) > 0 {
			_ = s.Kanban.Event(k.loc, t.ID, by, "Started despite: {blockers}", kanban.Params{"blockers": kanban.BlockersText(forced)})
		}
		if err := s.Kanban.Move(k.loc, t.ID, kanban.InProgress, by, ""); err != nil {
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

func (s *Server) ignoreWorktrees(root *projects.Project, rt *runtime.Runtime) error {
	dir := path.Join(root.Path, ".ide")
	if root.Type == "local" {
		return kanban.EnsureIgnored(dir)
	}
	gi := path.Join(dir, ".gitignore")
	data, _ := rt.FS.Read(gi)
	text, changed := kanban.WithIgnored(string(data))
	if !changed {
		return nil
	}
	_ = rt.FS.Mkdir(dir)
	return rt.FS.Write(gi, []byte(text))
}

// runSetup runs the setup command of the kanban in a new worktree (npm install…).
func (s *Server) runSetup(loc kanban.Location, rootID string, id int64, dir, cmd string, run execx.Runner) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	out, err := run.Output(ctx, []string{"sh", "-c", "(" + cmd + ") 2>&1"}, dir)
	log := string(out)
	if len(log) > 20000 {
		log = "…\n" + log[len(log)-20000:]
	}
	state, key, p := "ok", "Worktree setup finished", kanban.Params(nil)
	if err != nil {
		state, key, p = "error", "Worktree setup failed: {error}", kanban.Params{"error": err.Error()}
		log += "\n" + err.Error()
	}
	_ = s.Kanban.SetGit(loc, id, kanban.GitState{Setup: &state, SetupLog: &log})
	_ = s.Kanban.Event(loc, id, kanban.ByUser, key, p)
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
