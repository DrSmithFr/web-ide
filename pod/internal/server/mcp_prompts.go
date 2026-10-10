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

// The fix prompt may name one feedback (/mcp__web-ide__fix 12 3), else it handles them all.
var feedbackArg = map[string]any{"name": "feedback", "description": "Id of one test feedback to handle (optional: all the open ones)"}

var mcpRoles = []mcpRole{
	{"brief", "Take over the briefing of a ticket qualified by the local assistant", `Take over the **briefing** of ticket #{{id}}: the local assistant has qualified the need with the user; check it before planning.
- Read the ticket (kanban_get) and its briefing conversations (kanban_conversation), then the code concerned.
- Look for what is missing or fragile: unclear need, unstated acceptance criteria, edge cases, risks, conflicts with the existing code, a ticket that should be split.
- Ask the user about what only they can decide; record the answers.
- Improve the ticket: kanban_update (description, 1500 characters max; linked files), kanban_add_note (decisions, a few lines each).
- Work too big for one ticket: split it into a lineage (kanban_create with parent: steps developed one after the other in the same worktree, merged together) and use depends_on for work that must be merged first.
- Do not change any file. Sum up what you changed and offer to write the plan (/mcp__web-ide__plan {{id}}).`},
	{"plan", "Write the implementation plan of a ticket", `Write the **implementation plan** of ticket #{{id}}.
- Read the ticket (kanban_get) and, if useful, its briefing conversations (kanban_conversation); explore the code concerned. Ask the user when essential information is missing.
- Save the plan with kanban_set_plan: Markdown (approach, files to change, steps, risks, tests), the estimated size of the ticket (the volume of work), its complexity (how hard it is: low, medium, high; it picks the model and the effort of the development), and goals, each one a verifiable objective with a short title and, if useful, how to check it. The ticket then moves to "To do" by itself.
- Sum up the plan in a few lines. Do not change any file.`},
	{"dev", "Develop a ticket in its worktree", `**Develop** ticket #{{id}}.
- Read the ticket (kanban_get). If it has no worktree yet, start it with kanban_start; if it cannot start yet (a previous step or a dependency), tell the user and stop.
- A child ticket (Lineage in kanban_get) works in the worktree and on the branch of its parent: develop only this step, on top of the previous ones. Then switch this session into the worktree with EnterWorktree (path: the worktree) unless you already work there; if you cannot, work in it with absolute paths (cd <worktree> && … for commands).
- Follow the plan. Check each goal with kanban_goal as soon as it is reached and verified (tests, build).
- Commit regularly on the ticket branch; each commit message starts with "#{{id}} ".
- Do not merge or push the branch: the user does it from the IDE.
- When all the goals are checked, the tests pass and everything is committed, move the ticket to "To test" with kanban_move (status review) and a test_summary: what the user must test and how.`},
	{"fix", "Handle the open test feedback of a ticket (or one of them)", `Handle {{feedback}} of ticket #{{id}}.
- Read the ticket (kanban_get); switch this session into its worktree with EnterWorktree (path: the worktree) unless you already work there, else work in it with absolute paths.
- A bug: fix it. A new feature: build it if it fits the ticket, otherwise ask the user. An info: take it into account.
- Commit on the ticket branch (messages starting with "#{{id}} "). Do not merge or push.
- Once a feedback is handled, verified and committed, mark it done with kanban_feedback (action done). If how to test the ticket changed, update it with kanban_update (test_summary).`},
	{"review", "Review the work on a ticket: findings become test feedback", `**Review** the work on ticket #{{id}}.
- Read the ticket (kanban_get): description, plan, goals, notes, how to test, open feedback. Work in its worktree (EnterWorktree, path: the worktree, unless you already work there; else absolute paths); a ticket without a worktree is reviewed in the project folder.
- Read the change of its branch against its base (git log and git diff base...HEAD, plus the uncommitted changes): correctness, goals really reached, plan followed, tests, edge cases, security, the conventions of the project.
- Run the tests and the build of the project if they are quick. Do not change any file, do not commit, do not move the ticket.
- Each finding the developer must act on becomes a test feedback with kanban_feedback (action add): kind bug (wrong behavior), feature (missing from the plan or the goals) or info (worth knowing), and the complexity of the fix (low, medium, high); one concrete finding each, with the file and line, under 1000 characters. Skip what an open feedback already says; no feedback for style nits.
- Then one note with kanban_add_note: the verdict in a few lines (ready to test, or what blocks).
- Sum up for the user: the feedback added, the verdict.`},
}

func mcpPromptList() []map[string]any {
	out := make([]map[string]any, 0, len(mcpRoles))
	for _, r := range mcpRoles {
		args := []map[string]any{{"name": "ticket", "description": "Ticket number", "required": true}}
		if r.name == "fix" {
			args = append(args, feedbackArg)
		}
		out = append(out, map[string]any{
			"name":        r.name,
			"description": r.description,
			"arguments":   args,
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
		feedback := "the open **test feedback**"
		if f := strings.TrimSpace(args["feedback"]); f != "" {
			fid, err := strconv.ParseInt(f, 10, 64)
			if err != nil || fid <= 0 {
				return nil, fmt.Errorf("feedback must be a feedback id, got %q", f)
			}
			feedback = "the **test feedback** with id " + f + " (only this one)"
		}
		text := strings.NewReplacer("{{id}}", strconv.FormatInt(id, 10), "{{feedback}}", feedback).Replace(r.text) +
			"\n\nPass your working directory as cwd to the kanban tools."
		return map[string]any{
			"description": r.description,
			"messages":    []any{map[string]any{"role": "user", "content": map[string]any{"type": "text", "text": text}}},
		}, nil
	}
	return nil, fmt.Errorf("unknown prompt: %s", name)
}
