# Kanban

Each project has a kanban, a tool of the IDE that is also where the user and the AI agents work together: tickets are written, specified, planned, developed and tested through linked conversations of the assistant.

## Storage

- A SQLite base that is not versioned: `<project>/.ide/kanban.db` for a local project, `~/.web-ide/kanban/<project>.db` for an SSH one (like the conversations). `.ide/.gitignore` excludes `kanban.db*` and `worktrees/`.
- Attachments are stored in the base (20 MB max per file).
- The worktree of a ticket, opened as its own project, uses the kanban and the conversations of its parent project.

## Ticket

Number (#1, #2… per project), title, type (feature, bug, refactor, task), priority (low, normal, high, critical), description (Markdown), linked files (paths), attachments, notes, plan (Markdown), goals (checkable objectives), test summary, linked conversations (with their role), branch, comparison base, worktree, linked commits, history.

## Statuses and transitions

Tickets move with buttons only (no drag and drop).

| Status | User buttons | The model may |
|---|---|---|
| New | Briefing, Generate the plan, Ready for development (once there is a plan or goals), Abandon | move to *Ready* (after writing the plan and goals) |
| Ready | Start development (→ In progress), Abandon | — |
| In progress | New session, Send to testing, Abandon | move to *To test* (with a test summary) |
| To test | Add feedback (→ Fix), Close (→ Done), Abandon | — |
| Fix | Fix session, Send to testing, Abandon | move to *To test* |
| Done / Abandoned | Reopen (Done → Fix, Abandoned → New) | — |

Test feedback becomes goals (source `feedback`). Closing or abandoning removes the worktree; the branch is kept (abandoning offers to delete it). The change is frozen in the ticket when it is merged, or else when it is closed.

## Linked conversations

`chat.ticket = { id, role }`; roles:

- `briefing` (Plan mode, New): clarify the need with the user, questions with `ask_user`, findings written into the ticket;
- `plan` (Plan mode): writes the plan and the goals, then moves the ticket to *Ready*;
- `dev` (Build mode, in the worktree): follows the plan, checks goals, commits on the ticket branch, moves to *To test*;
- `correction` (Build mode, in the worktree): handles the test feedback;
- `resolve`: resolves the conflicts of a rebase (worktree) or a merge (main folder).

The system prompt receives the ticket as it is now and the instructions of the role.

## Assistant tools

- In every conversation: `kanban_list`, `kanban_get`, `kanban_create` (a new ticket in the backlog), `ask_user` (1 to 10 multiple-choice questions with a free answer, shown one at a time in the thread).
- Only in a conversation linked to a ticket, and only on that ticket: `kanban_update` (title, description, type, priority, files), `kanban_add_note`, `kanban_set_plan` (plan and goals), `kanban_goal` (check, uncheck, add), `kanban_move` (transitions allowed to the model; *To test* requires a test summary), `kanban_link_commit`.

## Git

- *Start development*: `git fetch` (when there is a remote), then the branch `ticket/<n>-<slug>` is created from the base (default `origin/main`, else `main`, configurable per project and per ticket) in a worktree `<project>/.ide/worktrees/<n>-<slug>`.
- A configurable setup command runs in a new worktree (`npm install && cp ../../../.env .`…).
- The worktree opens as a separate project in its own window: not listed on the home page, opened from its ticket, with a ticket banner.
- The model manages the branch and its commits (messages start with `#<n>`). The user triggers the merge (`merge --no-ff` by default, or squash) into the local base branch of the main folder (never pushed; refused while the main folder has uncommitted tracked changes) and the rebase on the base.
- Conflicts: no automatic abort; the ticket lists the conflicted files with *Continue*, *Abort* and *Resolution session*.
- From *In progress* on: changed files and their diff against the chosen base (from the merge base), including uncommitted changes and untracked files of the worktree.

## Interface

Board in an editor tab (one column per status, Done and Abandoned folded), ticket detail in an editor tab, compact list in a side panel, kanban settings (default base, worktree setup command). Every window follows the changes (`kanban.changed` event).
