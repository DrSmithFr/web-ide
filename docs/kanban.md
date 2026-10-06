# Kanban

Each project has a kanban, a tool of the IDE that is also where the user and the AI agents work together: tickets are written, specified, planned, developed and tested through linked conversations of the assistant.

## Storage

- A SQLite base that is not versioned: `<project>/.ide/kanban.db` for a local project, `~/.web-ide/kanban/<project>.db` for an SSH one (like the conversations). `.ide/.gitignore` excludes `kanban.db*` and `worktrees/`.
- Attachments are stored in the base (20 MB max per file).
- The schema is versioned with `PRAGMA user_version`; an older base is migrated when it is opened (Ready → To do, Fix → To test, test feedback moved from goals and notes to its own table).
- The worktree of a ticket, opened as its own project, uses the kanban and the conversations of its parent project.

## Ticket

Number (#1, #2… per project), title, priority (low, normal, high, critical), linked files (paths), attachments, linked commits, history, and what each stage adds:

- **New**: description (Markdown, 1500 characters max: the model tends to be verbose), notes (1000 characters max each, with the conversation that wrote them), briefing conversations.
- **To do**: implementation plan (Markdown), goals (a title and a description of how to check it, checkable), plan conversations, estimated size (S, M, L, XL: required when a model writes the plan, editable by the user).
- **In progress**: development conversations, git and changes, how to test (written by the model when it finishes).
- **To test**: test feedback (info, bug or new feature; 1000 characters max; checked once handled, by the model or the user; with the conversation handling it), pull request.

The ticket view shows every stage, always open, the current one marked.

## Lineages and dependencies

Work too big for one ticket is split into a **lineage**: a ticket (the root) and its children, the next steps, in order. A ticket has at most one parent and a child has no children (one level).

- The children have no branch of their own: they are developed in the worktree and on the branch of the root, one after the other. A child starts once the previous step is validated: the root by **Validate the step** (it stays *To test*, its worktree kept), a child by being closed (*Done*, without merge; its worktree and branch stay). Its change is counted from the commit it started at, and frozen when it is closed.
- The root is merged, gets its pull request and is closed once every child is *Done* or *Abandoned*; a child has no merge, rebase or pull request of its own. Abandoning the root with open children abandons them too; an abandoned child warns that its commits stay on the branch.
- The parent and the order of a child change only before its development starts; a ticket with children cannot be deleted.
- A ticket may also **depend on** tickets of other lineages (no loop, not inside its own lineage): it waits until their lineage is merged into its base or its root is *Done*. An abandoned dependency keeps blocking it.
- Only the start of the development is blocked (briefing and plan stay open). *Start development*, `kanban.start` and the MCP `kanban_start` refuse with the blockers (`parent not started`, `previous step not validated`, `dependency not merged`, `dependency abandoned`); the user can start anyway (*Start anyway…*, written in the history), a model cannot.
- A dependency merged in the local base branch only (`main`, never pushed by the IDE) and not in the base of the ticket (`origin/main`): the ticket starts from the local branch, and the history says so.
- In the worktree of a lineage, the default ticket of Claude Code is the current step: the child in progress, else the last one to test, else the root.
- Cards show `↳ #n` (step of) and a lock with the blocking tickets; the ticket view has a *Lineage* section (parent, steps with their order, dependencies with their state, blockers).

## Statuses and transitions

Tickets move with buttons only (no drag and drop).

| Status | User buttons | The model may |
|---|---|---|
| New | Briefing, Generate the plan, Abandon | — (a plan moves the ticket to *To do* by itself, whoever writes it) |
| To do | Back to New, Redo the plan, Start development (→ In progress), Abandon | — |
| In progress | New session, Send to testing, Abandon | move to *To test* (with how to test) |
| To test | Add feedback, Fix session (per open feedback), Create the pull request, Back to In progress, Validate the step (a ticket with children), Close (→ Done), Abandon | mark feedback handled |
| Done / Abandoned | Reopen (Done → To test, Abandoned → New) | — |

A feedback leaves the ticket in *To test*. Closing or abandoning removes the worktree; the branch is kept (abandoning offers to delete it). The change is frozen in the ticket when it is merged, or else when it is closed.

## Linked conversations

`chat.ticket = { id, role, feedback? }`; roles:

- `briefing` (Briefing mode, New): clarify the need with the user, questions with `ask_user`, findings written into the ticket;
- `plan` (Plan mode): writes the plan and the goals (the ticket moves to *To do*);
- `dev` (Build mode, in the worktree): follows the plan, checks goals, commits on the ticket branch, moves to *To test* with how to test;
- `correction` (Build mode, in the worktree): handles one test feedback (`feedback`, started from it, which records the conversation) or the open ones, and marks each one handled;
- `resolve`: resolves the conflicts of a rebase (worktree) or a merge (main folder).

The system prompt receives the ticket as it is now and the instructions of the role.

## From an idea to tickets: the Briefing mode

The assistant has three modes (Shift+Tab cycles through them): Build, Plan and Briefing. In Briefing mode the model changes nothing: its prompt (editable, `briefing-prompt.md` / `.ide/briefing-prompt.md`) makes it question the user in rounds of `ask_user` until the need is clear, then write it as one or several tickets with `kanban_create` when the user agrees. The first ticket created links the conversation to it (role `briefing`), so `kanban_update` and `kanban_add_note` refine it; the other tickets created list the conversation too. A ticket may have several briefing conversations.

The usual path: a briefing makes the tickets, *Generate the plan* writes the plan of each one, then development sessions build them.

## Assistant tools

- In every conversation: `kanban_list`, `kanban_get`, `kanban_create` (a new ticket in the backlog, `parent` and `depends_on` included), `ask_user` (1 to 10 questions shown one at a time in the thread, see below).
- Only in a conversation linked to a ticket, and only on that ticket: `kanban_update` (title, description, priority, how to test, files, parent, dependencies), `kanban_add_note` (1000 characters max: notes are for decisions, not reports), `kanban_set_plan` (plan and goals with their description), `kanban_goal` (check, uncheck, add), `kanban_feedback` (mark a feedback handled or open again), `kanban_move` (*To test* only, with how to test), `kanban_link_commit`.
- A description over 1500 characters is refused with advice to shorten it.

Each question of `ask_user` has a type that picks its widget: `choice` (the default: options, with `pros` / `cons` if given), `idea` (one proposal answered No / Yes, but… / Yes / Exactly, or by a swipe on a touch screen), `compare` (exactly two approaches side by side), `rank` (2 to 8 items to order, `top` to keep only the first N) and `scenario` (a `situation`, then options). Any question also takes a free answer, "I don't know" (the model then offers examples), "Up to you" (it decides and says what) and a note. The pod checks the rules of each type: a call breaking one is refused with an error naming the question, so that the model asks again; a question without type works as before.

## Claude Code

The pod serves an MCP endpoint (`POST /mcp`, the token of the pod as a bearer) so that Claude Code can work on the kanban next to the local assistant: a planning or a debug that needs a stronger model, a task to go faster. Setup, once:

```
claude mcp add --transport http --scope user web-ide http://127.0.0.1:4433/mcp --header "Authorization: Bearer $(cat ~/.web-ide/token)"
```

- Every tool takes `cwd`, the folder Claude works in: the deepest local project holding it gives the kanban; in the worktree of a ticket, that ticket is the default one.
- Tools: those of the assistant, on any ticket (`kanban_list`, `kanban_get`, `kanban_create`, `kanban_update`, `kanban_add_note`, `kanban_set_plan`, `kanban_goal`, `kanban_feedback`, `kanban_move`, `kanban_link_commit`), plus `kanban_conversation` (a linked conversation of the assistant: messages and `ask_user` answers) and `kanban_start` (*Start development*: branch, worktree, setup command).
- Author `claude`: the rules of the model, plus *To do* → *In progress* through `kanban_start`; shown as Claude in the notes and the history.
- File links: `/open?path=<absolute path>&line=<n>` (cookie or bearer) opens the file in the windows of its project (`ide.open` event), or the project page on it (`?open=`) when none has it; the instructions of the endpoint ask Claude to write the files it mentions as such links.
- In the IDE: the *Claude Code* menu of a ticket runs `claude` with the prompt fitting its status in a terminal of the project (in the worktree for `dev` and `fix`; `--model opus` for `brief` and `plan`; `WEBIDE_CLAUDE` in the environment of the pod names another command), and the kanban settings show the `claude mcp add` command to copy.
- Links to the IDE (`kanban_list`, `kanban_get`, `kanban_create`, `kanban_start` answers, file links) use the public address of the IDE: `publicUrl` in `config.json` (or `-public-url`), e.g. the Tailscale name; else `http://<addr>`. `/project/<id>?ticket=<n>` opens the tab of a ticket.
- The Claude Code mod `claude-mod/` (`claude --plugin-dir claude-mod`, or copied into a plugin folder): above the prompt, links to the project Claude works in and to the ticket of its worktree, and the files it changed, each a button opening the file in the IDE (the mobile app draws no band above the prompt: it gets the same links in a pane, opened when it joins the session and by `/webide`); `/webide <file[:line]>` opens any file. It reads the token in `~/.web-ide/token` (`WEBIDE_DATA`, `WEBIDE_URL` to change). Tests: `claude plugin test claude-mod`.
- A session started at the root of the project moves into the worktree of a ticket with `EnterWorktree` (the worktrees of the IDE are git worktrees): `kanban_start` and the `dev` / `fix` prompts ask for it.
- Prompts, commands of Claude Code: `/mcp__web-ide__brief <n>` (take over a briefing of the local assistant: check the need, complete the ticket), `plan`, `dev`, `fix`.

## Git

- *Start development*: `git fetch` (when there is a remote), then the branch `ticket/<n>-<slug>` is created from the base (default `origin/main`, else `main`, configurable per project and per ticket) in a worktree `<project>/.ide/worktrees/<n>-<slug>`. Without a repository, or in one without a commit yet, the IDE offers to develop in the project folder instead.
- A configurable setup command runs in a new worktree (`npm install && cp ../../../.env .`…).
- The worktree opens as a separate project in its own window: not listed on the home page, opened from its ticket or from the worktree selector of the menu bar, which shows the ticket and its status in place of the branch.
- The model manages the branch and its commits (messages start with `#<n>`). The user triggers the merge (`merge --no-ff` by default, or squash) into the local base branch of the main folder (never pushed; uncommitted changes of the main folder are put aside with `--autostash` and applied again after the merge, also after a conflict once it is committed or aborted) and the rebase on the base (`--autostash` as well for the changes of the worktree).
- Pull request (*To test* stage): with a remote `origin` and the `gh` command, *Create the pull request* pushes the branch (`git push --set-upstream origin`) then runs `gh pr create` (title `#<n> <title>`, base the ticket base without `origin/`, body: description, goals, how to test); its address is kept in the ticket. The only push of the IDE, always asked by the user.
- Conflicts: no automatic abort; the ticket lists the conflicted files with *Continue*, *Abort* and *Resolution session*.
- From *In progress* on: changed files and their diff against the chosen base (from the merge base), including uncommitted changes and untracked files of the worktree.

## Interface

Board in an editor tab (columns New, To do, In progress and To test; Done and Abandoned folded; cards show the open feedback), and its **Roadmap** view (switch in the toolbar, remembered by the browser): one row per lineage not merged yet, its blocks in order on a sequence axis without dates, each one as wide as the size of its ticket (a dotted edge when not estimated); a row waiting for another lineage starts where that one ends, with an arrow. Ready tickets stand out (accent border, ▶), blocked ones are dimmed with a lock, New ones hatched, the finished steps of an open lineage greyed; a click or Enter opens the ticket, the arrow keys move between blocks; the filter applies to both views. ticket detail in an editor tab, compact list in a side panel, kanban settings (default base, worktree setup command). Every window follows the changes (`kanban.changed` event).
