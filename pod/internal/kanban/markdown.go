package kanban

import (
	"fmt"
	"strings"
	"time"
)

// English names read by the models (the page has the same tables in kanban/state.ts).
var (
	PriorityNames = map[string]string{"low": "Low", "normal": "Normal", "high": "High", "critical": "Critical"}
	FeedbackNames = map[string]string{"info": "Info", "bug": "Bug", "feature": "New feature"}
	RoleNames     = map[string]string{"briefing": "Briefing", "plan": "Plan", "dev": "Development", "correction": "Correction", "resolve": "Conflicts"}
	authorNames   = map[string]string{ByUser: "user", ByModel: "assistant", ByClaude: "Claude"}
)

func isoDate(ms int64) string { return time.UnixMilli(ms).UTC().Format("2006-01-02 15:04") }

func indent(s string) string { return strings.ReplaceAll(strings.TrimSpace(s), "\n", "\n  ") }

// Markdown is a ticket as a model reads it (assistant and MCP endpoint).
func Markdown(t *Ticket) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Ticket #%d · %s\n", t.ID, t.Title)
	fmt.Fprintf(&b, "Status: %s · priority: %s", StatusNames[t.Status], PriorityNames[t.Priority])
	if t.Size != "" {
		fmt.Fprintf(&b, " · size: %s", SizeNames[t.Size])
	}
	if t.Complexity != "" {
		fmt.Fprintf(&b, " · complexity: %s", ComplexityNames[t.Complexity])
	}
	if t.Branch != "" {
		fmt.Fprintf(&b, " · branch: %s", t.Branch)
	}
	if t.Base != "" {
		fmt.Fprintf(&b, " · base: %s", t.Base)
	}
	if t.Worktree != "" {
		fmt.Fprintf(&b, " · worktree: %s", t.Worktree)
	}
	if lines := lineageLines(t); len(lines) > 0 {
		b.WriteString("\n\n## Lineage\n" + strings.Join(lines, "\n"))
	}
	if t.Milestone != 0 {
		fmt.Fprintf(&b, "\n\n## Milestone\nMilestone %d · %s (kanban_milestone action get: its spec and user stories).", t.Milestone, t.MilestoneTitle)
	}
	desc := strings.TrimSpace(t.Description)
	if desc == "" {
		desc = "(empty)"
	}
	fmt.Fprintf(&b, "\n\n## Description\n%s\n", desc)
	if len(t.Files) > 0 {
		b.WriteString("\n## Linked files\n")
		for _, f := range t.Files {
			fmt.Fprintf(&b, "- %s\n", f)
		}
	}
	if len(t.Attachments) > 0 {
		b.WriteString("\n## Attachments\n")
		for _, a := range t.Attachments {
			mime := a.Mime
			if mime == "" {
				mime = "file"
			}
			fmt.Fprintf(&b, "- %s (%s)\n", a.Name, mime)
		}
	}
	notes := false
	for _, n := range t.Notes {
		if n.Kind == "event" {
			continue
		}
		if !notes {
			b.WriteString("\n## Notes\n")
			notes = true
		}
		fmt.Fprintf(&b, "- (%s, %s) %s\n", authorNames[n.Author], isoDate(n.Created), strings.TrimSpace(n.Text))
	}
	plan := strings.TrimSpace(t.Plan)
	if plan == "" {
		plan = "(no plan yet)"
	}
	fmt.Fprintf(&b, "\n## Plan\n%s\n", plan)
	if len(t.GoalList) > 0 {
		b.WriteString("\n## Goals\n")
		for _, g := range t.GoalList {
			mark := " "
			if g.Done {
				mark = "x"
			}
			fmt.Fprintf(&b, "- [%s] (id %d) %s\n", mark, g.ID, g.Text)
			if d := strings.TrimSpace(g.Description); d != "" {
				fmt.Fprintf(&b, "  %s\n", indent(d))
			}
		}
	}
	if s := strings.TrimSpace(t.TestSummary); s != "" {
		fmt.Fprintf(&b, "\n## How to test\n%s\n", s)
	}
	if len(t.FeedbackList) > 0 {
		b.WriteString("\n## Test feedback\n")
		for _, f := range t.FeedbackList {
			mark := " "
			if f.Done {
				mark = "x"
			}
			kind := FeedbackNames[f.Kind]
			if f.Complexity != "" {
				kind += ", complexity " + ComplexityNames[f.Complexity]
			}
			fmt.Fprintf(&b, "- [%s] (id %d, %s, %s) %s\n", mark, f.ID, kind, isoDate(f.Created), indent(f.Text))
		}
	}
	if len(t.ChatList) > 0 {
		b.WriteString("\n## Linked conversations\n")
		for _, c := range t.ChatList {
			title := c.Title
			if title == "" {
				title = c.ChatID
			}
			fmt.Fprintf(&b, "- %s: %s (chat %s)\n", RoleNames[c.Role], title, c.ChatID)
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

func lineageLines(t *Ticket) []string {
	var out []string
	if t.Parent != 0 {
		out = append(out, fmt.Sprintf("- Step of #%d (the root): developed in its worktree, on its branch, after the previous steps; merged with it.", t.Parent))
	}
	if len(t.Children) > 0 {
		out = append(out, "- Steps, in order (developed in this worktree after this ticket, the root; it is merged once they are finished):")
		for _, c := range t.Children {
			out = append(out, fmt.Sprintf("  - #%d [%s] %s", c.ID, StatusNames[c.Status], c.Title))
		}
	}
	if len(t.DependsOn) > 0 {
		ids := make([]string, len(t.DependsOn))
		for i, d := range t.DependsOn {
			ids[i] = fmt.Sprintf("#%d", d)
		}
		out = append(out, fmt.Sprintf("- Depends on %s: starts once they are merged or done.", strings.Join(ids, ", ")))
	}
	if len(t.Blockers) > 0 {
		out = append(out, fmt.Sprintf("- Cannot start yet: %s.", BlockersText(t.Blockers)))
	}
	return out
}

// BlockersText is a list of blockers as the models read it: "#3 (previous step not validated)".
func BlockersText(bl []Blocker) string {
	parts := make([]string, len(bl))
	for i, b := range bl {
		parts[i] = fmt.Sprintf("#%d (%s)", b.ID, BlockerNames[b.Kind])
	}
	return strings.Join(parts, ", ")
}

// MilestoneMarkdown is a milestone as a model reads it.
func MilestoneMarkdown(ms *Milestone) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Milestone %d · %s\n", ms.ID, ms.Title)
	fmt.Fprintf(&b, "State: %s · tickets finished %d/%d · user stories validated %d/%d", MilestoneStateNames[ms.State], ms.TicketsDone, ms.Tickets, ms.StoriesDone, ms.Stories)
	if ms.FeedbackOpen > 0 {
		fmt.Fprintf(&b, " · open feedback %d", ms.FeedbackOpen)
	}
	if ms.Spec != "" {
		fmt.Fprintf(&b, "\nSpec: %s", ms.Spec)
	}
	desc := strings.TrimSpace(ms.Description)
	if desc == "" {
		desc = "(empty)"
	}
	fmt.Fprintf(&b, "\n\n## Description\n%s\n", desc)
	if len(ms.StoryList) > 0 {
		b.WriteString("\n## User stories (validated by the user; the base of the e2e coverage)\n")
		for _, s := range ms.StoryList {
			mark := " "
			if s.Done {
				mark = "x"
			}
			fmt.Fprintf(&b, "- [%s] (id %d) %s\n", mark, s.ID, indent(s.Text))
		}
	}
	if len(ms.TicketList) > 0 {
		b.WriteString("\n## Tickets\n")
		for _, t := range ms.TicketList {
			line := fmt.Sprintf("- #%d [%s] %s", t.ID, StatusNames[t.Status], t.Title)
			if t.Parent != 0 {
				line += fmt.Sprintf(" (step of #%d)", t.Parent)
			}
			b.WriteString(line + "\n")
		}
	}
	if len(ms.FeedbackList) > 0 {
		b.WriteString("\n## Feedback\n")
		for _, f := range ms.FeedbackList {
			mark := " "
			if f.Done {
				mark = "x"
			}
			kind := FeedbackNames[f.Kind]
			if f.Complexity != "" {
				kind += ", complexity " + ComplexityNames[f.Complexity]
			}
			if f.Ticket != 0 {
				kind += fmt.Sprintf(", made into #%d", f.Ticket)
			}
			fmt.Fprintf(&b, "- [%s] (id %d, %s, %s) %s\n", mark, f.ID, kind, isoDate(f.Created), indent(f.Text))
		}
	}
	notes := false
	for _, n := range ms.Notes {
		if n.Kind == "event" {
			continue
		}
		if !notes {
			b.WriteString("\n## Notes\n")
			notes = true
		}
		fmt.Fprintf(&b, "- (%s, %s) %s\n", authorNames[n.Author], isoDate(n.Created), strings.TrimSpace(n.Text))
	}
	if len(ms.ChatList) > 0 {
		b.WriteString("\n## Linked conversations\n")
		for _, c := range ms.ChatList {
			title := c.Title
			if title == "" {
				title = c.ChatID
			}
			fmt.Fprintf(&b, "- %s: %s (chat %s)\n", RoleNames[c.Role], title, c.ChatID)
		}
	}
	return strings.TrimRight(b.String(), "\n")
}
