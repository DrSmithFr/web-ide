package server

import (
	"context"
	"encoding/json"
	"path"
	"strings"

	"github.com/DrSmithFr/web-ide/pod/internal/i18n"
	"github.com/DrSmithFr/web-ide/pod/internal/kanban"
	"github.com/DrSmithFr/web-ide/pod/internal/projects"
	"github.com/DrSmithFr/web-ide/pod/internal/runtime"
	"github.com/DrSmithFr/web-ide/pod/internal/sshx"
)

// Worktrees of the repository of a project, for the branch selector of the menu bar: the
// main folder, the worktrees of the tickets and the other ones. A branch is opened in its
// own worktree (.ide/worktrees/b-<branch>) rather than checked out in the main folder, so
// that uncommitted changes never get in the way. Each worktree opens as a child project.

type wtItem struct {
	Path    string `json:"path"`
	Branch  string `json:"branch,omitempty"`
	Main    bool   `json:"main,omitempty"`
	Project string `json:"project,omitempty"` // project opened on it, if any
	Ticket  int64  `json:"ticket,omitempty"`
}

// rootOf returns the project owning the repository (the parent of a worktree project) and
// its runtime.
func (s *Server) rootOf(c *Client) (*projects.Project, *runtime.Runtime, error) {
	rt, err := c.runtime()
	if err != nil {
		return nil, nil, err
	}
	p := &rt.P
	if p.Parent == "" {
		return p, rt, nil
	}
	root, ok := s.Projects.Get(p.Parent)
	if !ok {
		return nil, nil, i18n.New("project not found")
	}
	prt, err := s.openRuntime(root.ID, sshx.Creds{})
	if err != nil {
		return nil, nil, err
	}
	return root, prt, nil
}

// family is the project owning the repository of a project (itself or its parent).
func (s *Server) family(id string) string {
	if p, ok := s.Projects.Get(id); ok && p.Parent != "" {
		return p.Parent
	}
	return id
}

func (s *Server) registerWorktrees() {
	// Attaches another worktree of the repository to the window: its files, consoles and
	// git open in the same page (requests naming it in `project`), its events reach the window.
	s.handle("project.attach", func(ctx context.Context, c *Client, p json.RawMessage) (any, error) {
		a, err := bind[struct{ ID string }](p)
		if err != nil {
			return nil, err
		}
		if c.project == "" || s.family(a.ID) != s.family(c.project) {
			return nil, i18n.New("this worktree is not one of the project")
		}
		rt, err := s.openRuntime(a.ID, sshx.Creds{})
		if err != nil {
			return nil, err
		}
		s.mu.Lock()
		fresh := a.ID != c.project && !c.attached[a.ID]
		if fresh {
			c.attached[a.ID] = true
		}
		s.mu.Unlock()
		if fresh {
			rt.Attach()
		}
		v, _ := s.Projects.Get(a.ID)
		return map[string]any{
			"project":  projects.View{Project: v, DisplayName: v.Name()},
			"root":     rt.Root,
			"local":    rt.Local,
			"consoles": rt.Consoles.List(),
		}, nil
	})
	s.handle("worktrees.list", func(ctx context.Context, c *Client, p json.RawMessage) (any, error) {
		root, rt, err := s.rootOf(c)
		if err != nil {
			return nil, err
		}
		items := []wtItem{}
		if rt.Git.Top(ctx) == "" {
			return map[string]any{"root": root.ID, "items": items}, nil
		}
		list, err := rt.Git.Worktrees(ctx)
		if err != nil {
			return nil, err
		}
		for _, w := range list {
			it := wtItem{Path: w.Path, Branch: w.Branch, Main: w.Main}
			if w.Main {
				it.Project = root.ID
			} else {
				it.Project, it.Ticket = s.Projects.ChildAt(root.ID, w.Path)
			}
			items = append(items, it)
		}
		return map[string]any{"root": root.ID, "items": items}, nil
	})
	// Opens an existing worktree (not a ticket one: kanban.open) as a project.
	s.handle("worktrees.open", func(ctx context.Context, c *Client, p json.RawMessage) (any, error) {
		a, err := bind[struct{ Path string }](p)
		if err != nil {
			return nil, err
		}
		root, rt, err := s.rootOf(c)
		if err != nil {
			return nil, err
		}
		list, err := rt.Git.Worktrees(ctx)
		if err != nil {
			return nil, err
		}
		for _, w := range list {
			if w.Path == a.Path && !w.Main {
				title := w.Branch
				if title == "" {
					title = path.Base(w.Path)
				}
				v, err := s.Projects.PutWorktree(root, title, w.Path)
				return map[string]string{"project": v.ID}, err
			}
		}
		return nil, i18n.Errorf("worktree not found: %s", a.Path)
	})
	// Checks a branch out in a new worktree (or a new branch from the main folder).
	s.handle("worktrees.add", func(ctx context.Context, c *Client, p json.RawMessage) (any, error) {
		a, err := bind[struct {
			Branch string
			Create bool
		}](p)
		if err != nil {
			return nil, err
		}
		a.Branch = strings.TrimSpace(a.Branch)
		if a.Branch == "" {
			return nil, i18n.New("branch name missing")
		}
		root, rt, err := s.rootOf(c)
		if err != nil {
			return nil, err
		}
		if err := s.ignoreWorktrees(root, rt); err != nil {
			return nil, err
		}
		local := a.Branch
		if !a.Create && !rt.Git.HasLocal(ctx, local) {
			_, local, _ = strings.Cut(a.Branch, "/") // origin/x → x
		}
		dir := path.Join(rt.Root, ".ide", "worktrees", "b-"+kanban.Slug(local))
		if err := rt.Git.AddWorktree(ctx, dir, a.Branch, a.Create); err != nil {
			return nil, err
		}
		// The path as git gives it (symbolic links resolved), like worktrees.list.
		if list, err := rt.Git.Worktrees(ctx); err == nil {
			for _, w := range list {
				if w.Branch == local && !w.Main {
					dir = w.Path
				}
			}
		}
		v, err := s.Projects.PutWorktree(root, local, dir)
		if err != nil {
			return nil, err
		}
		s.emitter(root.ID)("git.changed", nil, "")
		// The setup command of the kanban (npm install…) runs in the window of the worktree.
		meta, _ := s.Kanban.Meta(kanbanLoc(root))
		return map[string]string{"project": v.ID, "setup": strings.TrimSpace(meta["setup"])}, nil
	})
	// Removes a worktree opened from the menu bar (a ticket one goes with its ticket) and
	// its project; the branch is kept. Uncommitted changes need force.
	s.handle("worktrees.remove", func(ctx context.Context, c *Client, p json.RawMessage) (any, error) {
		a, err := bind[struct {
			Path  string
			Force bool
		}](p)
		if err != nil {
			return nil, err
		}
		root, rt, err := s.rootOf(c)
		if err != nil {
			return nil, err
		}
		list, err := rt.Git.Worktrees(ctx)
		if err != nil {
			return nil, err
		}
		found := false
		for _, w := range list {
			found = found || (w.Path == a.Path && !w.Main)
		}
		if !found {
			return nil, i18n.Errorf("worktree not found: %s", a.Path)
		}
		child, ticket := s.Projects.ChildAt(root.ID, a.Path)
		if ticket != 0 {
			return nil, i18n.New("this worktree belongs to a ticket: it is removed when the ticket is closed or abandoned")
		}
		if !a.Force && rt.Git.Dirty(ctx, a.Path) {
			return nil, &codeError{"dirty", i18n.New("the worktree has uncommitted changes")}
		}
		if child != "" {
			// The asking window leaves the project itself (it goes to the main folder).
			s.mu.Lock()
			if c.project == child {
				c.project = ""
			}
			s.mu.Unlock()
			s.closeRuntime(child)
			_ = s.Projects.Delete(child)
			s.Sessions.Delete(child)
		}
		if err := rt.Git.RemoveWorktree(ctx, a.Path); err != nil {
			return nil, err
		}
		s.emitter(root.ID)("git.changed", nil, "")
		return nil, nil
	})
}
