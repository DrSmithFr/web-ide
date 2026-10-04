// Package git runs git in a project (locally or on the SSH host) for the Git panel.
package git

import (
	"context"
	"errors"
	"path"
	"strconv"
	"strings"

	"github.com/DrSmithFr/web-ide/pod/internal/execx"
	"github.com/DrSmithFr/web-ide/pod/internal/i18n"
)

type Repo struct {
	run  execx.Runner
	root string // project root
}

func New(r execx.Runner, root string) *Repo { return &Repo{run: r, root: root} }

func (g *Repo) git(ctx context.Context, args ...string) (string, error) {
	out, err := g.run.Output(ctx, append([]string{"git", "-c", "core.quotepath=off", "-c", "color.ui=false"}, args...), g.root)
	if err != nil {
		return string(out), errors.New(strings.TrimSpace(strings.TrimPrefix(err.Error(), "fatal: ")))
	}
	return string(out), nil
}

// Top returns the top level of the repository, or "" when the project is not in one.
func (g *Repo) Top(ctx context.Context) string {
	out, err := g.git(ctx, "rev-parse", "--show-toplevel")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

type File struct {
	Path      string `json:"path"`
	OrigPath  string `json:"origPath,omitempty"`
	Index     string `json:"index"` // status in the index: M A D R C T, or "."
	Work      string `json:"work"`  // status in the working tree
	Untracked bool   `json:"untracked,omitempty"`
	Conflict  bool   `json:"conflict,omitempty"`
}

type Status struct {
	Repo     bool   `json:"repo"`
	Top      string `json:"top,omitempty"`
	Branch   string `json:"branch,omitempty"`
	Upstream string `json:"upstream,omitempty"`
	Ahead    int    `json:"ahead"`
	Behind   int    `json:"behind"`
	Files    []File `json:"files"`
}

// Status parses `git status --porcelain=v2 -z`.
func (g *Repo) Status(ctx context.Context) (*Status, error) {
	st := &Status{Files: []File{}}
	top := g.Top(ctx)
	if top == "" {
		return st, nil
	}
	st.Repo, st.Top = true, top
	out, err := g.git(ctx, "status", "--porcelain=v2", "--branch", "-z", "--untracked-files=all")
	if err != nil {
		return nil, err
	}
	abs := func(p string) string { return path.Join(top, p) }
	entries := strings.Split(out, "\x00")
	for i := 0; i < len(entries); i++ {
		e := entries[i]
		if e == "" {
			continue
		}
		switch e[0] {
		case '#':
			f := strings.Fields(e)
			if len(f) < 3 {
				continue
			}
			switch f[1] {
			case "branch.head":
				st.Branch = f[2]
			case "branch.upstream":
				st.Upstream = f[2]
			case "branch.ab":
				st.Ahead, _ = strconv.Atoi(strings.TrimPrefix(f[2], "+"))
				if len(f) > 3 {
					st.Behind, _ = strconv.Atoi(strings.TrimPrefix(f[3], "-"))
				}
			}
		case '1', '2':
			n := 8
			if e[0] == '2' {
				n = 9
			}
			f := strings.SplitN(e, " ", n+1)
			if len(f) <= n {
				continue
			}
			file := File{Path: abs(f[n]), Index: f[1][:1], Work: f[1][1:2]}
			if e[0] == '2' && i+1 < len(entries) {
				i++
				file.OrigPath = abs(entries[i])
			}
			st.Files = append(st.Files, file)
		case 'u':
			f := strings.SplitN(e, " ", 11)
			if len(f) == 11 {
				st.Files = append(st.Files, File{Path: abs(f[10]), Index: f[1][:1], Work: f[1][1:2], Conflict: true})
			}
		case '?':
			st.Files = append(st.Files, File{Path: abs(e[2:]), Index: ".", Work: "?", Untracked: true})
		}
	}
	return st, nil
}

func (g *Repo) rel(ctx context.Context, paths []string) ([]string, string, error) {
	top := g.Top(ctx)
	if top == "" {
		return nil, "", i18n.New("not a git repository")
	}
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		if !strings.HasPrefix(p, top+"/") {
			return nil, "", i18n.Errorf("outside the repository: %s", p)
		}
		out = append(out, strings.TrimPrefix(p, top+"/"))
	}
	return out, top, nil
}

// Ignored returns the paths that .gitignore leaves out (tracked files never are).
// Outside a repository, nothing is ignored.
func (g *Repo) Ignored(ctx context.Context, paths []string) ([]string, error) {
	out := []string{}
	rel, top, err := g.rel(ctx, paths)
	if err != nil {
		return out, nil
	}
	for len(rel) > 0 {
		n := min(len(rel), 300)
		// Exit status 1 (without message) when no path is ignored.
		res, err := g.inTop(ctx, top, append([]string{"check-ignore", "--"}, rel[:n]...)...)
		if err != nil && !strings.HasSuffix(err.Error(), "status 1") { // local and SSH wording
			return nil, err
		}
		for _, p := range strings.Split(res, "\n") {
			if p != "" {
				out = append(out, top+"/"+p)
			}
		}
		rel = rel[n:]
	}
	return out, nil
}

// Show returns a file at a revision: "HEAD", or "" for the index. Missing file: "", false.
func (g *Repo) Show(ctx context.Context, p, rev string) (string, bool, error) {
	rel, _, err := g.rel(ctx, []string{p})
	if err != nil {
		return "", false, err
	}
	out, err := g.git(ctx, "show", rev+":"+rel[0])
	if err != nil {
		if strings.Contains(err.Error(), "does not exist") || strings.Contains(err.Error(), "exists on disk, but not in") || strings.Contains(err.Error(), "invalid object name") || strings.Contains(err.Error(), "bad revision") {
			return "", false, nil
		}
		return "", false, err
	}
	return out, true, nil
}

// inTop runs git from the top level with paths relative to it.
func (g *Repo) inTop(ctx context.Context, top string, args ...string) (string, error) {
	sub := &Repo{run: g.run, root: top}
	return sub.git(ctx, args...)
}

func (g *Repo) Stage(ctx context.Context, paths []string) error {
	rel, top, err := g.rel(ctx, paths)
	if err != nil {
		return err
	}
	_, err = g.inTop(ctx, top, append([]string{"add", "-A", "--"}, rel...)...)
	return err
}

func (g *Repo) Unstage(ctx context.Context, paths []string) error {
	rel, top, err := g.rel(ctx, paths)
	if err != nil {
		return err
	}
	if _, err = g.inTop(ctx, top, append([]string{"restore", "--staged", "--"}, rel...)...); err != nil {
		// No commit yet: nothing to restore from, remove from the index instead.
		_, err = g.inTop(ctx, top, append([]string{"rm", "--cached", "-r", "-q", "--"}, rel...)...)
	}
	return err
}

// Discard drops the working tree changes of tracked files (back to the index).
func (g *Repo) Discard(ctx context.Context, paths []string) error {
	rel, top, err := g.rel(ctx, paths)
	if err != nil {
		return err
	}
	_, err = g.inTop(ctx, top, append([]string{"restore", "--worktree", "--"}, rel...)...)
	return err
}

func (g *Repo) Commit(ctx context.Context, message string, amend bool) (string, error) {
	if strings.TrimSpace(message) == "" && !amend {
		return "", i18n.New("empty commit message")
	}
	args := []string{"commit", "-m", message}
	if amend {
		args = []string{"commit", "--amend"}
		if strings.TrimSpace(message) != "" {
			args = append(args, "-m", message)
		} else {
			args = append(args, "--no-edit")
		}
	}
	out, err := g.git(ctx, args...)
	return strings.TrimSpace(out), err
}

type Commit struct {
	Hash    string `json:"hash"`
	Short   string `json:"short"`
	Author  string `json:"author"`
	When    int64  `json:"when"` // commit time, Unix seconds (the page formats it)
	Subject string `json:"subject"`
	Refs    string `json:"refs,omitempty"`
}

func (g *Repo) Log(ctx context.Context, n int) ([]Commit, error) {
	if n <= 0 || n > 500 {
		n = 50
	}
	out, err := g.git(ctx, "log", "-n", strconv.Itoa(n), "--pretty=format:%H%x1f%h%x1f%an%x1f%at%x1f%s%x1f%D%x1e")
	if err != nil {
		if strings.Contains(err.Error(), "does not have any commits") {
			return []Commit{}, nil
		}
		return nil, err
	}
	list := []Commit{}
	for _, rec := range strings.Split(out, "\x1e") {
		f := strings.Split(strings.Trim(rec, "\n"), "\x1f")
		if len(f) == 6 {
			when, _ := strconv.ParseInt(f[3], 10, 64)
			list = append(list, Commit{Hash: f[0], Short: f[1], Author: f[2], When: when, Subject: f[4], Refs: f[5]})
		}
	}
	return list, nil
}

type Branch struct {
	Name     string `json:"name"`
	Current  bool   `json:"current"`
	Upstream string `json:"upstream,omitempty"`
	Remote   bool   `json:"remote,omitempty"`
}

func (g *Repo) Branches(ctx context.Context) ([]Branch, error) {
	out, err := g.git(ctx, "branch", "-a", "--format=%(HEAD)%00%(refname)%00%(refname:short)%00%(upstream:short)")
	if err != nil {
		return nil, err
	}
	list := []Branch{}
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		f := strings.Split(line, "\x00")
		if len(f) != 4 || strings.HasSuffix(f[2], "/HEAD") {
			continue
		}
		list = append(list, Branch{Name: f[2], Current: f[0] == "*", Upstream: f[3], Remote: strings.HasPrefix(f[1], "refs/remotes/")})
	}
	return list, nil
}

func (g *Repo) Switch(ctx context.Context, name string, create bool) error {
	args := []string{"switch", name}
	if create {
		args = []string{"switch", "-c", name}
	}
	_, err := g.git(ctx, args...)
	return err
}

// Init creates a repository whose first branch is main.
func (g *Repo) Init(ctx context.Context) error {
	if _, err := g.git(ctx, "init", "-q"); err != nil {
		return err
	}
	_, err := g.git(ctx, "symbolic-ref", "HEAD", "refs/heads/main")
	return err
}

// Setup prepares the repository of a new project: init when the folder is in none, an
// empty first commit when it is empty (so that main exists for the ticket worktrees),
// and the remote origin when given and not yet set.
func (g *Repo) Setup(ctx context.Context, remote string, empty bool) error {
	if g.Top(ctx) == "" {
		if err := g.Init(ctx); err != nil {
			return err
		}
		if empty {
			// Best effort: a machine without a git identity keeps an unborn main.
			_, _ = g.git(ctx, "commit", "-q", "--allow-empty", "-m", "Initial commit")
		}
	}
	remote = strings.TrimSpace(remote)
	if remote == "" {
		return nil
	}
	if _, err := g.git(ctx, "remote", "get-url", "origin"); err == nil {
		return nil
	}
	_, err := g.git(ctx, "remote", "add", "origin", remote)
	return err
}

type Worktree struct {
	Path   string `json:"path"`
	Branch string `json:"branch,omitempty"` // empty: detached HEAD
	Main   bool   `json:"main,omitempty"`   // the main folder of the repository
}

// Worktrees parses `git worktree list --porcelain`; the main folder comes first.
func (g *Repo) Worktrees(ctx context.Context) ([]Worktree, error) {
	out, err := g.git(ctx, "worktree", "list", "--porcelain")
	if err != nil {
		return nil, err
	}
	list := []Worktree{}
	for _, block := range strings.Split(strings.TrimSpace(out), "\n\n") {
		var w Worktree
		prunable := false
		for _, line := range strings.Split(block, "\n") {
			k, v, _ := strings.Cut(line, " ")
			switch k {
			case "worktree":
				w.Path = v
			case "branch":
				w.Branch = strings.TrimPrefix(v, "refs/heads/")
			case "prunable":
				prunable = true
			}
		}
		if w.Path != "" && !prunable {
			w.Main = len(list) == 0
			list = append(list, w)
		}
	}
	return list, nil
}

// AddWorktree checks a branch out in a new worktree: a local branch, a branch of a remote
// ("origin/x": local branch x tracking it), or a new branch from HEAD when create is set.
func (g *Repo) AddWorktree(ctx context.Context, dir, branch string, create bool) error {
	var err error
	switch {
	case create:
		_, err = g.git(ctx, "worktree", "add", "-b", branch, dir)
	case g.ref(ctx, "refs/heads/"+branch):
		_, err = g.git(ctx, "worktree", "add", dir, branch)
	case g.ref(ctx, "refs/remotes/"+branch):
		_, local, _ := strings.Cut(branch, "/")
		_, err = g.git(ctx, "worktree", "add", "--track", "-b", local, dir, branch)
	default:
		return i18n.Errorf("branch not found: %s", branch)
	}
	return err
}

// HasLocal tells whether a local branch exists.
func (g *Repo) HasLocal(ctx context.Context, name string) bool { return g.ref(ctx, "refs/heads/"+name) }

func (g *Repo) ref(ctx context.Context, ref string) bool {
	_, err := g.git(ctx, "rev-parse", "--verify", "--quiet", ref)
	return err == nil
}

// Dirty tells whether a worktree has uncommitted changes (untracked files included).
func (g *Repo) Dirty(ctx context.Context, dir string) bool {
	out, err := g.inTop(ctx, dir, "status", "--porcelain")
	return err != nil || strings.TrimSpace(out) != ""
}

// RemoveWorktree deletes a worktree (its uncommitted changes with it); the branch stays.
func (g *Repo) RemoveWorktree(ctx context.Context, dir string) error {
	_, err := g.git(ctx, "worktree", "remove", "--force", dir)
	return err
}
