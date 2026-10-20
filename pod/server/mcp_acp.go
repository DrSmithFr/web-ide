package server

import (
	"fmt"
	"github.com/DrSmithFr/web-ide/pod/assistant"
	"strings"

	"github.com/DrSmithFr/web-ide/pod/acp"
)

// acpThreadText is the thread of a session of the Agents tool as kanban_conversation gives
// it: the prompts and answers, the questions; with tools, the tool calls and the notices.
func acpThreadText(info acp.Info, entries []acp.Entry, last int, tools bool) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Agent session: %s (%s, %s)\n", info.Title, info.Name, info.State)
	if last > 0 && last < len(entries) {
		fmt.Fprintf(&b, "\n(%d earlier entries left out)\n", len(entries)-last)
		entries = entries[len(entries)-last:]
	}
	for _, e := range entries {
		switch e.Kind {
		case "user":
			fmt.Fprintf(&b, "\n## User\n%s\n", strings.TrimSpace(e.Text))
		case "agent":
			fmt.Fprintf(&b, "\n## %s\n%s\n", info.Name, strings.TrimSpace(e.Text))
		case "permission":
			outcome := e.Outcome
			if outcome == "" {
				outcome = "waiting for the user"
			}
			fmt.Fprintf(&b, "\n## %s asks permission: %s (%s)\n", info.Name, e.Title, outcome)
		case "plan":
			if tools {
				b.WriteString("\n## Plan\n")
				for _, p := range e.Plan {
					fmt.Fprintf(&b, "- [%s] %s\n", p.Status, p.Content)
				}
			}
		case "tool":
			if tools {
				fmt.Fprintf(&b, "- %s %s (%s)\n", e.ToolKind, assistant.Cut(e.Title, 300), e.Status)
			}
		case "notice", "error":
			if tools {
				fmt.Fprintf(&b, "\n(%s)\n", e.Text)
			}
		}
	}
	return b.String()
}
