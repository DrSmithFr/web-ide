package server

import (
	"fmt"
	"strconv"
	"strings"
)

// Prompts of the MCP endpoint: Claude Code shows them as commands
// (/mcp__web-ide__plan 12). Each one hands a ticket to Claude for one role, like the
// roles of the conversations of the assistant (ROLE_INSTRUCTIONS in web/src/llm/prompt.ts).

type mcpRole struct {
	name, description, text string
}

var mcpRoles = []mcpRole{
	{"brief", "Take over the briefing of a ticket qualified by the local assistant", `Take over the **briefing** of ticket #{{id}}: the local assistant has qualified the need with the user; check it before planning.
- Read the ticket (kanban_get) and its briefing conversations (kanban_conversation), then the code concerned.
- Look for what is missing or fragile: unclear need, unstated acceptance criteria, edge cases, risks, conflicts with the existing code, a ticket that should be split.
- Ask the user about what only they can decide; record the answers.
- Improve the ticket: kanban_update (description, 1500 characters max; linked files), kanban_add_note (decisions, a few lines each).
- Do not change any file. Sum up what you changed and offer to write the plan (/mcp__web-ide__plan {{id}}).`},
	{"plan", "Write the implementation plan of a ticket", `Write the **implementation plan** of ticket #{{id}}.
- Read the ticket (kanban_get) and, if useful, its briefing conversations (kanban_conversation); explore the code concerned. Ask the user when essential information is missing.
- Save the plan with kanban_set_plan: Markdown (approach, files to change, steps, risks, tests) and goals, each one a verifiable objective with a short title and, if useful, how to check it. The ticket then moves to "To do" by itself.
- Sum up the plan in a few lines. Do not change any file.`},
	{"dev", "Develop a ticket in its worktree", `**Develop** ticket #{{id}}.
- Read the ticket (kanban_get). If it has no worktree yet, start it with kanban_start; then work only in its worktree, with absolute paths (cd <worktree> && … for commands).
- Follow the plan. Check each goal with kanban_goal as soon as it is reached and verified (tests, build).
- Commit regularly on the ticket branch; each commit message starts with "#{{id}} ". Link each commit with kanban_link_commit.
- Do not merge or push the branch: the user does it from the IDE.
- When all the goals are checked, the tests pass and everything is committed, move the ticket to "To test" with kanban_move (status review) and a test_summary: what the user must test and how.`},
	{"fix", "Handle the open test feedback of a ticket", `Handle the open **test feedback** of ticket #{{id}}.
- Read the ticket (kanban_get); work in its worktree, with absolute paths.
- A bug: fix it. A new feature: build it if it fits the ticket, otherwise ask the user. An info: take it into account.
- Commit on the ticket branch (messages starting with "#{{id}} ") and link the commits with kanban_link_commit. Do not merge or push.
- Once a feedback is handled, verified and committed, mark it done with kanban_feedback (action done). If how to test the ticket changed, update it with kanban_update (test_summary).`},
}

func mcpPromptList() []map[string]any {
	out := make([]map[string]any, 0, len(mcpRoles))
	for _, r := range mcpRoles {
		out = append(out, map[string]any{
			"name":        r.name,
			"description": r.description,
			"arguments":   []map[string]any{{"name": "ticket", "description": "Ticket number", "required": true}},
		})
	}
	return out
}

func mcpPrompt(name string, args map[string]string) (map[string]any, error) {
	for _, r := range mcpRoles {
		if r.name != name {
			continue
		}
		id, err := strconv.ParseInt(strings.TrimPrefix(strings.TrimSpace(args["ticket"]), "#"), 10, 64)
		if err != nil || id <= 0 {
			return nil, fmt.Errorf("ticket must be a ticket number, got %q", args["ticket"])
		}
		text := strings.ReplaceAll(r.text, "{{id}}", strconv.FormatInt(id, 10)) +
			"\n\nPass your working directory as cwd to the kanban tools."
		return map[string]any{
			"description": r.description,
			"messages":    []any{map[string]any{"role": "user", "content": map[string]any{"type": "text", "text": text}}},
		}, nil
	}
	return nil, fmt.Errorf("unknown prompt: %s", name)
}
