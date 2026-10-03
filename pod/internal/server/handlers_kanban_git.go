package server

import (
	"context"
	"encoding/json"
	"errors"
	"path"
	"strconv"
	"strings"
	"time"

	"webide/pod/internal/execx"
	"webide/pod/internal/kanban"
	"webide/pod/internal/projects"
	"webide/pod/internal/runtime"
)

// Git side of the tickets: worktree and branch of a ticket, its changes, and the end of
// its worktree when it is closed or abandoned (docs/kanban.md).
func (s *Server) registerKanbanGit() {
	type gctx struct {
		loc  kanban.Location
		root *projects.Project
		rt   *runtime.Runtime
		git  kanban.Git
	}
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
	exists := func(k gctx, p string) bool {
		if p == "" {
			return false
		}
		_, err := k.rt.FS.Stat(p)
		return err == nil
	}
	type idArg struct {
		ID   int64  `json:"id"`
		Base string `json:"base"`
	}

	s.handle("kanban.start", h(func(ctx context.Context, c *Client, k gctx, p json.RawMessage) (any, error) {
		a, err := bind[idArg](p)
		if err != nil {
			return nil, err
		}
		t, err := s.Kanban.Get(k.loc, a.ID)
		if err != nil {
			return nil, err
		}
		if t.Status == kanban.Done || t.Status == kanban.Abandoned || t.Status == kanban.New {
			return nil, errors.New("le ticket doit être « À développer », « En cours » ou en correction")
		}
		if !exists(k, t.Worktree) {
			if !k.git.IsRepo(ctx) {
				return nil, errors.New("le projet n'est pas un dépôt git : impossible de créer la branche du ticket")
			}
			if err := s.ignoreWorktrees(k.root, k.rt); err != nil {
				return nil, err
			}
			fetchErr := k.git.Fetch(ctx)
			meta, _ := s.Kanban.Meta(k.loc)
			base := firstOf(a.Base, t.Base, meta["base"])
			if base == "" {
				base = k.git.DefaultBase(ctx)
			}
			branch, dir := k.git.Names(t.ID, t.Title)
			if t.Branch != "" {
				branch = t.Branch
			}
			if err := k.git.AddWorktree(ctx, dir, branch, base); err != nil {
				return nil, err
			}
			setup := strings.TrimSpace(meta["setup"])
			state := ""
			if setup != "" {
				state = "running"
			}
			if err := s.Kanban.SetGit(k.loc, t.ID, kanban.GitState{Branch: &branch, Base: &base, Worktree: &dir, Setup: &state}); err != nil {
				return nil, err
			}
			text := "Worktree créé : branche " + branch + " depuis " + base
			if fetchErr != nil {
				text += " (git fetch a échoué : " + fetchErr.Error() + ")"
			}
			_ = s.Kanban.Event(k.loc, t.ID, kanban.ByUser, text)
			if setup != "" {
				go s.runSetup(k.loc, k.root.ID, t.ID, dir, setup, k.rt.Runner)
			}
		}
		if t.Status == kanban.Ready {
			if err := s.Kanban.Move(k.loc, t.ID, kanban.InProgress, kanban.ByUser, ""); err != nil {
				return nil, err
			}
		}
		t, err = s.Kanban.Get(k.loc, a.ID)
		if err != nil {
			return nil, err
		}
		child, err := s.Projects.PutChild(k.root, t.ID, "#"+itoa(t.ID)+" "+t.Title, t.Worktree)
		if err != nil {
			return nil, err
		}
		s.emitKanban(k.root.ID, t.ID, nil)
		return map[string]any{"ticket": t, "project": child.ID}, nil
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
			return nil, errors.New("ce ticket n'a pas de worktree")
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
		switch {
		case exists(k, t.Worktree):
			return k.git.Changes(ctx, t.Worktree, t.Branch, base)
		case t.Branch != "" && k.git.IsRepo(ctx):
			if d, err := k.git.Changes(ctx, "", t.Branch, base); err == nil || t.Snapshot == nil {
				return d, err
			}
		}
		if t.Snapshot != nil {
			return &kanban.Diff{Base: t.Snapshot.Base, From: t.Snapshot.Base, Head: t.Snapshot.Head, Files: t.Snapshot.Files, Source: "snapshot"}, nil
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

	// kanban.finish closes (done) or abandons a ticket: the change is frozen in the ticket,
	// the worktree and its project are removed; the branch is kept unless asked.
	s.handle("kanban.finish", h(func(ctx context.Context, c *Client, k gctx, p json.RawMessage) (any, error) {
		a, err := bind[struct {
			ID           int64  `json:"id"`
			Status       string `json:"status"`
			Comment      string `json:"comment"`
			DeleteBranch bool   `json:"deleteBranch"`
		}](p)
		if err != nil {
			return nil, err
		}
		if a.Status != kanban.Done && a.Status != kanban.Abandoned {
			return nil, errors.New("état final inconnu")
		}
		t, err := s.Kanban.Get(k.loc, a.ID)
		if err != nil {
			return nil, err
		}
		if !kanban.CanMove(t.Status, a.Status, kanban.ByUser) {
			return nil, errors.New("ce ticket ne peut pas passer à cet état")
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
			if d, err := k.git.Changes(ctx, wt, t.Branch, base); err == nil {
				snap := &kanban.Snapshot{Base: d.From, Head: d.Head, Files: d.Files, Patch: k.git.Patch(ctx, wt, t.Branch, d.From, 2<<20)}
				_ = s.Kanban.SetGit(k.loc, t.ID, kanban.GitState{Snapshot: snap})
			}
		}
		if err := s.removeWorktree(ctx, k.loc, k.root, k.git, t, wt); err != nil {
			return nil, err
		}
		if a.DeleteBranch && t.Branch != "" {
			if err := k.git.DeleteBranch(ctx, t.Branch); err != nil {
				_ = s.Kanban.Event(k.loc, t.ID, kanban.ByUser, "Branche non supprimée : "+err.Error())
			} else {
				empty := ""
				_ = s.Kanban.SetGit(k.loc, t.ID, kanban.GitState{Branch: &empty})
				_ = s.Kanban.Event(k.loc, t.ID, kanban.ByUser, "Branche "+t.Branch+" supprimée")
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
		_ = s.Kanban.Event(loc, t.ID, kanban.ByUser, "Worktree supprimé")
	}
	if t.Worktree != "" {
		empty := ""
		return s.Kanban.SetGit(loc, t.ID, kanban.GitState{Worktree: &empty})
	}
	return nil
}

// ignoreWorktrees keeps .ide/worktrees out of the repository (also on an SSH host).
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
	state, text := "ok", "Initialisation du worktree terminée"
	if err != nil {
		state, text = "error", "Initialisation du worktree en échec : "+err.Error()
		log += "\n" + err.Error()
	}
	_ = s.Kanban.SetGit(loc, id, kanban.GitState{Setup: &state, SetupLog: &log})
	_ = s.Kanban.Event(loc, id, kanban.ByUser, text)
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
