package kanban

import (
	"context"
	"errors"
	"path"
	"strconv"
	"strings"
	"time"
	"unicode"

	"golang.org/x/text/unicode/norm"

	"github.com/DrSmithFr/web-ide/pod/internal/execx"
	"github.com/DrSmithFr/web-ide/pod/internal/i18n"
)

// Git runs the git operations of the tickets in the main folder of a project (locally or
// on the SSH host): worktrees, branches, diff, merge and rebase.
type Git struct {
	Run  execx.Runner
	Root string // main folder of the project
}

func (g Git) git(ctx context.Context, dir string, args ...string) (string, error) {
	out, err := g.Run.Output(ctx, append([]string{"git", "-c", "core.quotepath=off", "-c", "color.ui=false"}, args...), dir)
	if err != nil {
		return string(out), errors.New(strings.TrimSpace(strings.TrimPrefix(err.Error(), "fatal: ")))
	}
	return string(out), nil
}

// Commit is the full hash and the subject of a commit of the repository (worktrees share
// it), for linking it to a ticket.
func (g Git) Commit(ctx context.Context, ref string) (hash, subject string, err error) {
	out, err := g.git(ctx, g.Root, "log", "-1", "--format=%H%x1f%s", ref, "--")
	if err != nil {
		return "", "", i18n.Errorf("commit not found: %s", ref)
	}
	hash, subject, _ = strings.Cut(strings.TrimSpace(out), "\x1f")
	return hash, subject, nil
}

// Slug turns a title into a short branch-friendly name.
func Slug(title string) string {
	var b strings.Builder
	dash := false
	for _, r := range norm.NFD.String(strings.ToLower(title)) {
		switch {
		case unicode.Is(unicode.Mn, r):
			continue
		case r < 128 && (unicode.IsLetter(r) || unicode.IsDigit(r)):
			b.WriteRune(r)
			dash = false
		default:
			if !dash && b.Len() > 0 {
				b.WriteByte('-')
				dash = true
			}
		}
		if b.Len() >= 40 {
			break
		}
	}
	return strings.Trim(b.String(), "-")
}

// Names returns the branch and the worktree folder of a ticket.
func (g Git) Names(id int64, title string) (branch, dir string) {
	name := strconv.FormatInt(id, 10)
	if s := Slug(title); s != "" {
		name += "-" + s
	}
	return "ticket/" + name, path.Join(g.Root, ".ide", "worktrees", name)
}

func (g Git) IsRepo(ctx context.Context) bool {
	_, err := g.git(ctx, g.Root, "rev-parse", "--git-dir")
	return err == nil
}

// HasCommit is false for a repository whose branch has no commit yet.
func (g Git) HasCommit(ctx context.Context) bool { return g.verify(ctx, g.Root, "HEAD") }

func (g Git) verify(ctx context.Context, dir, ref string) bool {
	_, err := g.git(ctx, dir, "rev-parse", "--verify", "--quiet", ref+"^{commit}")
	return err == nil
}

// DefaultBase is origin/main when it exists, else main, else the current branch.
func (g Git) DefaultBase(ctx context.Context) string {
	for _, ref := range []string{"origin/main", "origin/master", "main", "master"} {
		if g.verify(ctx, g.Root, ref) {
			return ref
		}
	}
	out, err := g.git(ctx, g.Root, "branch", "--show-current")
	if err == nil && strings.TrimSpace(out) != "" {
		return strings.TrimSpace(out)
	}
	return "HEAD"
}

// Fetch updates the remote branches (ignored without a remote).
func (g Git) Fetch(ctx context.Context) error {
	out, err := g.git(ctx, g.Root, "remote")
	if err != nil || strings.TrimSpace(out) == "" {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	_, err = g.git(ctx, g.Root, "fetch", "--quiet", "--prune")
	return err
}

// AddWorktree creates the worktree of a ticket, with a new branch from base unless the
// branch already exists (a reopened ticket).
func (g Git) AddWorktree(ctx context.Context, dir, branch, base string) error {
	if g.verify(ctx, g.Root, "refs/heads/"+branch) {
		_, err := g.git(ctx, g.Root, "worktree", "add", dir, branch)
		return err
	}
	if !g.verify(ctx, g.Root, base) {
		return i18n.Errorf("base not found: %s", base)
	}
	_, err := g.git(ctx, g.Root, "worktree", "add", "--no-track", "-b", branch, dir, base)
	return err
}

func (g Git) RemoveWorktree(ctx context.Context, dir string) error {
	_, err := g.git(ctx, g.Root, "worktree", "remove", "--force", dir)
	if err != nil && strings.Contains(err.Error(), "is not a working tree") {
		_, _ = g.git(ctx, g.Root, "worktree", "prune")
		return nil
	}
	return err
}

func (g Git) DeleteBranch(ctx context.Context, branch string) error {
	_, err := g.git(ctx, g.Root, "branch", "-D", branch)
	return err
}

// Diff is the change of a ticket against its base.
type Diff struct {
	Base   string     `json:"base"`
	From   string     `json:"from"` // merge base
	Head   string     `json:"head"`
	Files  []DiffFile `json:"files"`
	Source string     `json:"source"` // worktree | branch
	Ahead  int        `json:"ahead"`  // commits of the branch not in the base
	Behind int        `json:"behind"` // commits of the base not in the branch
	Dirty  bool       `json:"dirty"`  // uncommitted changes in the worktree
}

// Changes lists the files changed by a ticket: in its worktree (commits and uncommitted
// changes, untracked files included) or, without worktree, on its branch.
func (g Git) Changes(ctx context.Context, worktree, branch, base string) (*Diff, error) {
	d := &Diff{Base: base, Files: []DiffFile{}}
	dir, head := g.Root, branch
	if worktree != "" {
		dir, head, d.Source = worktree, "HEAD", "worktree"
	} else {
		d.Source = "branch"
	}
	if !g.verify(ctx, dir, head) {
		return nil, i18n.Errorf("branch not found: %s", branch)
	}
	if !g.verify(ctx, dir, base) {
		return nil, i18n.Errorf("base not found: %s", base)
	}
	mb, err := g.git(ctx, dir, "merge-base", base, head)
	if err != nil {
		return nil, err
	}
	d.From = strings.TrimSpace(mb)
	h, _ := g.git(ctx, dir, "rev-parse", head)
	d.Head = strings.TrimSpace(h)
	if out, err := g.git(ctx, dir, "rev-list", "--left-right", "--count", base+"..."+head); err == nil {
		f := strings.Fields(out)
		if len(f) == 2 {
			d.Behind, _ = strconv.Atoi(f[0])
			d.Ahead, _ = strconv.Atoi(f[1])
		}
	}
	args := []string{"diff", "-M", "--name-status", "-z", d.From}
	stat := []string{"diff", "-M", "--numstat", "-z", d.From}
	if worktree == "" {
		args = append(args, head)
		stat = append(stat, head)
	}
	out, err := g.git(ctx, dir, args...)
	if err != nil {
		return nil, err
	}
	byPath := map[string]*DiffFile{}
	f := strings.Split(out, "\x00")
	for i := 0; i+1 < len(f); i++ {
		st := f[i]
		if st == "" {
			continue
		}
		p := f[i+1]
		i++
		if st[0] == 'R' || st[0] == 'C' {
			if i+1 < len(f) {
				p = f[i+1]
				i++
			}
		}
		d.Files = append(d.Files, DiffFile{Path: p, Status: st[:1]})
	}
	for i := range d.Files {
		byPath[d.Files[i].Path] = &d.Files[i]
	}
	if out, err := g.git(ctx, dir, stat...); err == nil {
		recs := strings.Split(out, "\x00")
		for i := 0; i < len(recs); i++ {
			parts := strings.SplitN(recs[i], "\t", 3)
			if len(parts) < 3 {
				continue
			}
			p := parts[2]
			if p == "" && i+2 < len(recs) { // rename: "a\tb\t" then old, new
				p = recs[i+2]
				i += 2
			}
			if df := byPath[p]; df != nil {
				df.Added, _ = strconv.Atoi(parts[0])
				df.Removed, _ = strconv.Atoi(parts[1])
			}
		}
	}
	if worktree != "" {
		if out, err := g.git(ctx, dir, "status", "--porcelain", "-z"); err == nil && strings.TrimSpace(out) != "" {
			d.Dirty = true
		}
		if out, err := g.git(ctx, dir, "ls-files", "--others", "--exclude-standard", "-z"); err == nil {
			for _, p := range strings.Split(out, "\x00") {
				if p != "" && byPath[p] == nil {
					n := 0
					if data, err := g.Run.Output(ctx, []string{"wc", "-l", p}, dir); err == nil {
						n, _ = strconv.Atoi(strings.Fields(string(data) + " 0")[0])
					}
					d.Files = append(d.Files, DiffFile{Path: p, Status: "?", Added: n})
				}
			}
		}
	}
	return d, nil
}

// Fixed prefixes: the user's configuration (mnemonicPrefix, noprefix) must not change them.
const prefixA, prefixB = "--src-prefix=a/", "--dst-prefix=b/"

// FilePatch is the unified diff of one file of a ticket (from the merge base).
func (g Git) FilePatch(ctx context.Context, worktree, branch, from, p string) (string, error) {
	if worktree == "" {
		return g.git(ctx, g.Root, "diff", "-M", prefixA, prefixB, from, branch, "--", p)
	}
	out, err := g.git(ctx, worktree, "diff", "-M", prefixA, prefixB, from, "--", p)
	if err == nil && out == "" {
		// Untracked file: everything is added (exit code 1 when there is a difference).
		data, _ := g.Run.Output(ctx, []string{"git", "-c", "color.ui=false", "diff", "--no-index", prefixA, prefixB, "--", "/dev/null", p}, worktree)
		return string(data), nil
	}
	return out, err
}

// Patch is the whole diff of a ticket (frozen in the ticket when it is closed).
func (g Git) Patch(ctx context.Context, worktree, branch, from string, max int) string {
	var out string
	if worktree == "" {
		out, _ = g.git(ctx, g.Root, "diff", "-M", prefixA, prefixB, from, branch)
	} else {
		_, _ = g.git(ctx, worktree, "add", "-A", "-N")
		out, _ = g.git(ctx, worktree, "diff", "-M", prefixA, prefixB, from)
	}
	if len(out) > max {
		out = out[:max] + "\n… (diff cut)\n"
	}
	return out
}

// LocalBranch is the local branch a ticket merges into: main for origin/main.
func (g Git) LocalBranch(ctx context.Context, base string) string {
	if out, err := g.git(ctx, g.Root, "remote"); err == nil {
		for _, r := range strings.Fields(out) {
			if strings.HasPrefix(base, r+"/") {
				return strings.TrimPrefix(base, r+"/")
			}
		}
	}
	return base
}

// State tells what is in progress in a folder: rebase, merge, and the files in conflict.
type State struct {
	Rebase bool `json:"rebase"`
	Merge  bool `json:"merge"`
	// Squash: a merge --squash stopped by a conflict (no MERGE_HEAD, a SQUASH_MSG and
	// changes in the index).
	Squash    bool     `json:"squash"`
	Conflicts []string `json:"conflicts"`
}

// Busy tells whether a rebase or a merge waits in the folder.
func (s State) Busy() bool { return s.Rebase || s.Merge || s.Squash }

func (g Git) State(ctx context.Context, dir string) State {
	st := State{Conflicts: []string{}}
	test := `test -d "$(git rev-parse --git-path rebase-merge)" -o -d "$(git rev-parse --git-path rebase-apply)"`
	if _, err := g.Run.Output(ctx, []string{"sh", "-c", test}, dir); err == nil {
		st.Rebase = true
	}
	if _, err := g.git(ctx, dir, "rev-parse", "-q", "--verify", "MERGE_HEAD"); err == nil {
		st.Merge = true
	}
	if out, err := g.git(ctx, dir, "diff", "--name-only", "--diff-filter=U", "-z"); err == nil {
		for _, p := range strings.Split(out, "\x00") {
			if p != "" {
				st.Conflicts = append(st.Conflicts, p)
			}
		}
	}
	if !st.Merge && !st.Rebase {
		if _, err := g.Run.Output(ctx, []string{"sh", "-c", `test -f "$(git rev-parse --git-path SQUASH_MSG)"`}, dir); err == nil {
			_, staged := g.git(ctx, dir, "diff", "--cached", "--quiet")
			st.Squash = len(st.Conflicts) > 0 || staged != nil
		}
	}
	return st
}

// Merged tells whether a branch is contained in another one.
func (g Git) Merged(ctx context.Context, branch, into string) bool {
	_, err := g.git(ctx, g.Root, "merge-base", "--is-ancestor", branch, into)
	return err == nil
}

// Merge merges the branch of a ticket into the local base branch, in the main folder.
// A conflict leaves the merge in progress (no automatic abort).
func (g Git) Merge(ctx context.Context, branch, into, message string, squash bool) (State, error) {
	cur, _ := g.git(ctx, g.Root, "branch", "--show-current")
	if cur = strings.TrimSpace(cur); cur != into {
		return State{}, i18n.Errorf("the main folder is on the branch “%s”: switch to “%s” to merge", cur, into)
	}
	if st := g.State(ctx, g.Root); st.Busy() {
		return st, i18n.New("a merge or a rebase is already in progress in the main folder")
	}
	// Uncommitted changes of the main folder are put aside and applied again after the
	// merge (by git, also after a conflict once the merge is committed or aborted).
	var err error
	if squash {
		if _, err = g.git(ctx, g.Root, "merge", "--squash", "--autostash", branch); err == nil {
			_, err = g.git(ctx, g.Root, "commit", "-m", message)
		}
	} else {
		_, err = g.git(ctx, g.Root, "merge", "--no-ff", "--autostash", "-m", message, branch)
	}
	st := g.State(ctx, g.Root)
	if err != nil && len(st.Conflicts) == 0 {
		return st, err
	}
	return st, nil
}

// Rebase replays the branch of a ticket on its base, in its worktree.
func (g Git) Rebase(ctx context.Context, worktree, base string) (State, error) {
	if st := g.State(ctx, worktree); st.Busy() {
		return st, i18n.New("a rebase is already in progress in the worktree")
	}
	_, err := g.git(ctx, worktree, "-c", "core.editor=true", "-c", "core.commentChar=auto", "rebase", "--autostash", base)
	st := g.State(ctx, worktree)
	if err != nil && len(st.Conflicts) == 0 && !st.Rebase {
		return st, err
	}
	return st, nil
}

// Continue goes on with the rebase or the merge in progress in a folder (conflicts fixed
// and added); Abort cancels it.
func (g Git) Continue(ctx context.Context, dir string) (State, error) {
	st := g.State(ctx, dir)
	if len(st.Conflicts) > 0 {
		return st, i18n.Errorf("%d file(s) still in conflict: fix them, then git add", len(st.Conflicts))
	}
	var err error
	switch {
	case st.Rebase:
		// Ticket commits start with "#<n>": "#" must not be the comment character.
		_, err = g.git(ctx, dir, "-c", "core.editor=true", "-c", "core.commentChar=auto", "rebase", "--continue")
	case st.Merge, st.Squash:
		_, err = g.git(ctx, dir, "-c", "core.commentChar=auto", "commit", "--no-edit")
	default:
		return st, i18n.New("no rebase or merge in progress")
	}
	st = g.State(ctx, dir)
	if err != nil && len(st.Conflicts) == 0 {
		return st, err
	}
	return st, nil
}

func (g Git) Abort(ctx context.Context, dir string) error {
	st := g.State(ctx, dir)
	var err error
	switch {
	case st.Rebase:
		_, err = g.git(ctx, dir, "rebase", "--abort")
	case st.Merge:
		_, err = g.git(ctx, dir, "merge", "--abort")
	case st.Squash:
		// reset --merge would move the changes put aside by the merge to the stash list:
		// they are applied again here (kept in the stash list on a conflict).
		stash, _ := g.git(ctx, dir, "rev-parse", "-q", "--verify", "MERGE_AUTOSTASH")
		if stash = strings.TrimSpace(stash); stash != "" {
			_, _ = g.git(ctx, dir, "update-ref", "-d", "MERGE_AUTOSTASH")
		}
		if _, err = g.git(ctx, dir, "reset", "--merge"); err == nil {
			_, _ = g.Run.Output(ctx, []string{"sh", "-c", `rm -f "$(git rev-parse --git-path SQUASH_MSG)"`}, dir)
		}
		if stash != "" {
			if _, aerr := g.git(ctx, dir, "stash", "apply", stash); aerr != nil {
				_, _ = g.git(ctx, dir, "stash", "store", "-m", "web-ide: changes put aside by the merge", stash)
				if err == nil {
					err = i18n.New("your uncommitted changes could not be applied again: they are kept in the git stash")
				}
			}
		}
	}
	return err
}
