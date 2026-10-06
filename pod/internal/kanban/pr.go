package kanban

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/DrSmithFr/web-ide/pod/internal/i18n"
)

// Pull request of a ticket: its branch is pushed to origin, then gh opens the pull request
// (docs/kanban.md). Only when the user asks for it: the model never pushes.

// CanPR tells whether a pull request can be opened: a remote origin and the gh command.
func (g Git) CanPR(ctx context.Context) bool {
	if !g.Run.Has("gh") {
		return false
	}
	_, err := g.git(ctx, g.Root, "remote", "get-url", "origin")
	return err == nil
}

// PRBase is the branch a pull request targets: the base without its remote.
func PRBase(base string) string { return strings.TrimPrefix(base, "origin/") }

var urlPattern = regexp.MustCompile(`https?://\S+`)

// OpenPR pushes the branch and creates its pull request; it returns its address (also when
// the pull request already exists).
func (g Git) OpenPR(ctx context.Context, branch, base, title, body string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	if _, err := g.git(ctx, g.Root, "push", "--set-upstream", "origin", branch); err != nil {
		return "", i18n.Errorf("git push failed: %s", err)
	}
	out, err := g.Run.Output(ctx, []string{"gh", "pr", "create", "--head", branch, "--base", PRBase(base), "--title", title, "--body", body}, g.Root)
	if err != nil {
		if u := urlPattern.FindString(err.Error()); u != "" && strings.Contains(err.Error(), "already exists") {
			return u, nil
		}
		return "", i18n.Errorf("gh pr create failed: %s", err)
	}
	if u := urlPattern.FindString(string(out)); u != "" {
		return u, nil
	}
	return "", i18n.Errorf("gh pr create gave no address: %s", strings.TrimSpace(string(out)))
}

// PRBody is the description of the pull request of a ticket, headings in the language of
// the user.
func PRBody(t *Ticket, lang string) string {
	var b strings.Builder
	b.WriteString(strings.TrimSpace(t.Description))
	if len(t.GoalList) > 0 {
		b.WriteString("\n\n## " + i18n.T(lang, "Goals") + "\n")
		for _, g := range t.GoalList {
			x := " "
			if g.Done {
				x = "x"
			}
			fmt.Fprintf(&b, "- [%s] %s\n", x, g.Text)
		}
	}
	if len(t.Children) > 0 {
		b.WriteString("\n## " + i18n.T(lang, "Steps") + "\n")
		for _, c := range t.Children {
			x := " "
			if c.Status == Done {
				x = "x"
			}
			fmt.Fprintf(&b, "- [%s] #%d %s\n", x, c.ID, c.Title)
		}
	}
	if s := strings.TrimSpace(t.TestSummary); s != "" {
		b.WriteString("\n## " + i18n.T(lang, "How to test") + "\n" + s + "\n")
	}
	return strings.TrimSpace(b.String())
}
