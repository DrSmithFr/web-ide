package git

import (
	"context"
	"path"
	"strconv"
	"strings"

	"github.com/DrSmithFr/web-ide/pod/internal/i18n"
)

type Commit struct {
	Hash    string   `json:"hash"`
	Short   string   `json:"short"`
	Parents []string `json:"parents"`
	Author  string   `json:"author"`
	Email   string   `json:"email"`
	When    int64    `json:"when"` // author time, Unix seconds (the page formats it)
	Subject string   `json:"subject"`
	Refs    string   `json:"refs,omitempty"`
	Body    string   `json:"-"`
}

const logFormat = "--pretty=format:%H%x1f%h%x1f%P%x1f%an%x1f%ae%x1f%at%x1f%s%x1f%D%x1f%b%x1e"

func parseLog(out string) []Commit {
	list := []Commit{}
	for _, rec := range strings.Split(out, "\x1e") {
		f := strings.Split(strings.TrimLeft(rec, "\n"), "\x1f")
		if len(f) != 9 {
			continue
		}
		when, _ := strconv.ParseInt(f[5], 10, 64)
		list = append(list, Commit{Hash: f[0], Short: f[1], Parents: strings.Fields(f[2]), Author: f[3], Email: f[4], When: when, Subject: f[6], Refs: f[7], Body: f[8]})
	}
	return list
}

// Log lists the commits of the current branch in graph order (`git log --graph` order),
// n at a time from skip. A query keeps the commits whose message, author or hash
// contains it (case-insensitive), among the 5000 most recent.
func (g *Repo) Log(ctx context.Context, skip, n int, query string) ([]Commit, error) {
	if n <= 0 || n > 500 {
		n = 50
	}
	skip = max(skip, 0)
	query = strings.ToLower(strings.TrimSpace(query))
	args := []string{"log", "--topo-order", logFormat}
	if query == "" {
		args = append(args, "--skip", strconv.Itoa(skip), "-n", strconv.Itoa(n))
	} else {
		args = append(args, "-n", "5000")
	}
	// A branch without commit has no history; git's message is translated, so HEAD is
	// checked first instead of reading it.
	if !g.ref(ctx, "HEAD") {
		return []Commit{}, nil
	}
	out, err := g.git(ctx, args...)
	if err != nil {
		return nil, err
	}
	list := parseLog(out)
	if query == "" {
		return list, nil
	}
	found := []Commit{}
	for _, c := range list {
		if strings.Contains(strings.ToLower(c.Subject+"\n"+c.Body+"\n"+c.Author+"\n"+c.Email), query) || strings.HasPrefix(c.Hash, query) {
			found = append(found, c)
		}
	}
	if skip >= len(found) {
		return []Commit{}, nil
	}
	return found[skip:min(skip+n, len(found))], nil
}

// ChangedFile is a file changed by a commit, against its first parent.
type ChangedFile struct {
	Path     string `json:"path"`
	OrigPath string `json:"origPath,omitempty"`
	Status   string `json:"status"` // A M D R C T
}

type CommitInfo struct {
	Commit
	Message   string        `json:"message"`
	Committer string        `json:"committer"`
	Committed int64         `json:"committed"`
	Files     []ChangedFile `json:"files"`
}

// CommitInfo returns a commit with its full message and the files it changed.
func (g *Repo) CommitInfo(ctx context.Context, rev string) (*CommitInfo, error) {
	top := g.Top(ctx)
	if top == "" {
		return nil, i18n.New("not a git repository")
	}
	if strings.HasPrefix(rev, "-") {
		return nil, i18n.Errorf("unknown revision: %s", rev)
	}
	out, err := g.git(ctx, "show", "-s", logFormat[:len(logFormat)-len("%x1e")]+"%x1f%B%x1f%cn%x1f%ct", rev, "--")
	if err != nil {
		return nil, err
	}
	f := strings.Split(out, "\x1f")
	if len(f) != 12 {
		return nil, i18n.Errorf("unknown revision: %s", rev)
	}
	list := parseLog(strings.Join(f[:9], "\x1f"))
	if len(list) != 1 {
		return nil, i18n.Errorf("unknown revision: %s", rev)
	}
	info := &CommitInfo{Commit: list[0], Message: strings.TrimRight(f[9], "\n"), Committer: f[10], Files: []ChangedFile{}}
	info.Committed, _ = strconv.ParseInt(strings.TrimSpace(f[11]), 10, 64)
	// Against the first parent, as the diff tabs of the history.
	args := []string{"diff-tree", "-r", "-M", "--name-status", "-z", "--no-commit-id", "--root", info.Hash}
	if len(info.Parents) > 0 {
		args = []string{"diff", "-M", "--name-status", "-z", info.Parents[0], info.Hash}
	}
	names, err := g.inTop(ctx, top, args...)
	if err != nil {
		return nil, err
	}
	e := strings.Split(names, "\x00")
	for i := 0; i+1 < len(e); i++ {
		st := e[i]
		if st == "" {
			continue
		}
		file := ChangedFile{Status: st[:1], Path: path.Join(top, e[i+1])}
		i++
		if (file.Status == "R" || file.Status == "C") && i+1 < len(e) {
			file.OrigPath = file.Path
			file.Path = path.Join(top, e[i+1])
			i++
		}
		info.Files = append(info.Files, file)
	}
	return info, nil
}

// Revert creates a commit undoing rev (a merge is reverted against its first parent).
func (g *Repo) Revert(ctx context.Context, rev string) (string, error) {
	info, err := g.CommitInfo(ctx, rev)
	if err != nil {
		return "", err
	}
	args := []string{"revert", "--no-edit"}
	if len(info.Parents) > 1 {
		args = append(args, "-m", "1")
	}
	out, err := g.git(ctx, append(args, info.Hash)...)
	return strings.TrimSpace(out), err
}

// Reset moves the current branch to rev: soft keeps the index and the files, mixed
// keeps the files, hard drops every change.
func (g *Repo) Reset(ctx context.Context, rev, mode string) error {
	if mode != "soft" && mode != "mixed" && mode != "hard" {
		return i18n.Errorf("unknown reset mode: %s", mode)
	}
	info, err := g.CommitInfo(ctx, rev)
	if err != nil {
		return err
	}
	_, err = g.git(ctx, "reset", "-q", "--"+mode, info.Hash)
	return err
}
