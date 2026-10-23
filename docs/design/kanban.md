# Kanban

Each project has a kanban, a tool of the IDE that is also where the user and the AIs work together: tickets are written, specified, planned, developed and tested through linked conversations of the assistant.

The kanban is a built-in plugin ([plugins.md](plugins.md#what-kanban-added-to-the-api)), enabled by default and turned off per project in *Settings → Plugins*: its RPC methods are in `plugins/kanban` (the base and the git operations of a ticket in `plugins/kanban/kanban`), its page in `plugins/kanban/web`. It needs the `terminal` (Exec) and `worktrees` (Git) capabilities; off, the `kanban_*` tools are refused and the ticket parts of the assistant and the project bar go.

## Storage

- A SQLite base that is not versioned: `<project>/.ide/kanban.db` for a local project, `~/.web-ide/kanban/<project>.db` for an SSH one (like the conversations). `.ide/.gitignore` ignores the whole folder (`*`), the base and the worktrees included.
- Attachments are stored in the base (20 MB max per file).
- The schema is versioned with `PRAGMA user_version`; an older base is migrated when it is opened (Ready → To do, Fix → To test, test feedback moved from goals and notes to its own table).
- The worktree of a ticket, opened as its own project, uses the kanban and the conversations of its parent project.

## Ticket

Number (#1, #2… per project), title, priority (low, normal, high, critical), linked files (paths), attachments, history, and what each stage adds:

- **New**: description (Markdown, 1500 characters max: the model tends to be verbose), notes (1000 characters max each, with the conversation that wrote them), briefing conversations.
- **To do**: implementation plan (Markdown), goals (a title and a description of how to check it, checkable), plan conversations, estimated size (S, M, L, XL: required when a model writes the plan, editable by the user).

**Complexity** (low, medium, high: how hard the work is, apart from its size) routes the sessions: a model writing the plan estimates it (required with `kanban_set_plan`), as it does for each test feedback it adds (`kanban_feedback` action `add`); the user changes it on the ticket until its development starts, like the priority and the size (then the pod refuses a change, and a plan written later keeps it), and on each feedback at any time (a feedback without one takes the one of its ticket). Each level is an effort of the assistant and a model of Claude Code:

| Complexity | Assistant | Claude Code |
|---|---|---|
| High | effort Max | Opus |
| Medium, or not estimated | effort Dynamic | Sonnet |
| Low | effort Low | Haiku |

*Briefing*, *Dev session*, *Fix session* and *Fix feedbacks* (the hardest of the open feedback) run the level of the complexity on a click, named on the button (*· Max*, *· Low*); their arrow lists every level, the recommended one starred. *Review* offers Claude Opus (high) or Sonnet. The effort of a session stays with its conversation (`ticket.effort`), whatever the options of the assistant; changing the effort in the conversation changes it for that session.

The priority and the size are fixed once the development started (*In progress*, *To test*, *Done*, *Abandoned*): the view shows them as badges, the pod refuses a change from the user or a model, and a plan written then keeps the size.
- **In progress**: development conversations, git and changes, how to test (written by the model when it finishes).
- **To test**: test feedback (info, bug or new feature; 1000 characters max; checked once handled, by the model or the user; with the conversation handling it), pull request.

The status bar shows the status, the priority and the size of the ticket view in focus; a click on one opens its menu to change it (a status asks its reason; the priority and the size are fixed once the development started).

The ticket view lists, in one column, the sections of the status of the ticket first, then, under a rule, those of the earlier statuses, folded (each status change folds and opens them again):

- **New**: description, briefing conversations, notes.
- **To do**: description, notes (folded), briefing conversations, plan conversations, goals, implementation plan.
- **In progress**: development conversations, goals, git and changes; then description, briefing, notes, plan conversations and plan, folded.
- **To test**: how to test, feedback, git and changes, pull request (shown once it exists or can be made); then the earlier sections, folded.
- **Done**, **Abandoned**: the sections of *To test* that have content, all folded but the description.

Aside: lineage, linked files, attachments, history.

Time of work: each linked conversation shows the time of its answers, its sub-agents included (the tooltip splits the conversation, its sub-agents and the thinking); a card of the board shows the total of its conversations at the top right, and lists the titles of its goals, checked or not. Only the assistant of the IDE is counted: Claude Code runs in a terminal.

The status *To test* is named `to_test` in the code, the base and the API (it was `review` until the base version 7, which migrated it; *Review* is the reading of a change by a model). The RPC and the MCP tools still accept `review` as an alias on input (status filters, `kanban_move`); every output says `to_test`. The page translates a `review` left in the history of older tickets.

## Lineages and dependencies

Work too big for one ticket is split into a **lineage**: a ticket (the root) and its **steps**, in order. A step has one root (the API field `parent`) and a step has no steps (one level).

- The steps have no branch of their own: they are developed in the worktree and on the branch of the root, one after the other. A step starts once the previous one is validated: the root by **Validate the step** (it stays *To test*, its worktree kept), a step by being closed (*Done*, without merge; its worktree and branch stay). Its change is counted from the commit it started at, and frozen when it is closed.
- The root is merged, gets its pull request and is closed once every step is *Done* or *Abandoned*; a step has no merge, rebase or pull request of its own. Abandoning the root with open steps abandons them too; an abandoned step warns that its commits stay on the branch.
- The root and the order of a step change only before its development starts; a ticket with steps cannot be deleted.
- A ticket may also **depend on** tickets of other lineages (no loop, not inside its own lineage): it waits until their lineage is merged into its base or its root is *Done*. An abandoned dependency keeps blocking it.
- Only the start of the development is blocked (briefing and plan stay open). *Start development*, `kanban.start` and the MCP `kanban_start` refuse with the blockers (`root not started`, `previous step not validated`, `dependency not merged`, `dependency abandoned`); the user can start anyway (*Start anyway…*, written in the history), a model cannot.
- A dependency merged in the local base branch only (`main`, never pushed by the IDE) and not in the base of the ticket (`origin/main`): the ticket starts from the local branch, and the history says so.
- In the worktree of a lineage, the default ticket of Claude Code is the current step: the step in progress, else the last one to test, else the root.
- Cards show `↳ #n` (step of) and a lock with the blocking tickets; the ticket view has a *Lineage* section (root, steps with their order, dependencies with their state, blockers).

## Milestones

A **milestone** groups the tickets of one feature: a functional grouping above lineages, spanning several lineages and lone tickets. It is the scope the agent loop will chain later.

- Title, description, spec (a path of the repo or a URL), linked briefing conversations, user stories (checkable, validated by the user).
- A ticket belongs to 0 or 1 milestone; a step takes the milestone of its root.
- Its tickets may be chained without the user (agent loop), but the milestone is always validated by the user: *Validate the milestone* is refused to models, and waits until every ticket is *Done* or *Abandoned*, every story checked and no feedback open.
- **Milestone feedback**, like the feedback of a ticket (info, bug, feature; complexity), added by the user or a model; handled by hand, or turned into a ticket of the milestone and handled when that ticket is *Done*.
- The milestone page validates its stories, its tickets *To test* (their *How to test* shown there) and the milestone; each ticket recalls its milestone and stories in its *How to test* section.
- User stories are not linked to tickets: they are the base of *Review coverage* on the milestone page (Claude Code, Opus or Sonnet, `/mcp__web-ide__coverage <n>`, read only): each story covered, partly or not by the e2e tests; a story partly or not covered becomes a milestone feedback, so the milestone waits for it; the verdict is a note of the milestone.
- The Board has a third view, *Milestones* (#106): a card per milestone with its progress (tickets finished, stories validated, tickets to test, open feedback; its bar, like the bar of an objective, fills with the finished tickets then the ones to test in their color, over all its tickets), the validated and abandoned ones folded, *New milestone*; a click opens its page. A milestone filter (all, none, one) narrows Board and Roadmap; with all, the Roadmap shows the lineages of each milestone (that of their root) in a band labelled with its title, those without milestone last.
- Storage and API (#104): tables `milestones`, `stories`, `milestone_feedback`, `milestone_chats`, `milestone_notes`, and `tickets.milestone_id` (base version 8); RPC `kanban.milestone.*` (`kanban.changed` carries `milestone`); MCP and assistant tool `kanban_milestone` (no validation nor story check: the user's), `milestone` on `kanban_create` / `kanban_update`. The assistant changes only the milestones its conversation is linked to (a briefing is linked to those it creates).
- Interface (#105): the milestone page, a tab of the editor (`?milestone=<n>` in a link, like `?ticket=`): title, state (*Ongoing*, *Validated*, *Abandoned*), *Briefing* (a Briefing conversation linked to it), *Validate the milestone* (disabled with its reason), description and spec, user stories (add, edit, order, check), tickets by status (those *To test* with their *How to test* and *Validate the ticket* / *Validate the step*), feedback (*Make a ticket*, handled by hand), notes, history. The ticket view chooses the milestone of a root or a lone ticket (*New milestone…* creates one), a step shows its root's (*No milestone (#n)* opens the root, where it is chosen); its *How to test* recalls the milestone and its stories, as does `kanban_get`; a card shows its milestone.

## Objectives

An **objective** is a named deadline (*MVP*, *Promo launch*, *v2*…): a milestone says *what* (one feature), an objective says *when*. It takes some tickets of several milestones and lone tickets: the MVP before a launch may take three tickets of a milestone but not the fourth (#109).

- Title, description (what reaching it means), an optional date (`YYYY-MM-DD`), a place in an ordered list (the roadmap of objectives), a state: *Ongoing*, *Reached*, *Abandoned*.
- A ticket belongs to 0 or 1 objective, **apart from its milestone** (two axes, like epic and fix version); a step takes the objective of its root, and follows it.
- A milestone may have a **default objective**: choosing it puts its open tickets in it (those without objective or with the former default; one put elsewhere by hand stays), and a ticket added to the milestone later takes it when it has none.
- Only the user reaches (once every ticket is *Done* or *Abandoned*), abandons, reopens or reorders an objective; models create and update them, put tickets in them and write notes.
- `kanban_next` (the Orchestrator's *what do we work on?*) takes the tickets of the first open objective first, then those of the next ones, then the others.
- Storage and API: tables `objectives`, `objective_notes`, `tickets.objective_id`, `milestones.objective_id` (base version 9); RPC `kanban.objective.*` (`list`, `get`, `create`, `update`, `delete`, `move`, `reach`, `state`, `note`; `kanban.changed` carries `objective`), `objective` in the patch of `kanban.update` and `kanban.milestone.update`; MCP and assistant tool `kanban_objective` (list, get, create, update, note), `objective` on `kanban_create`, `kanban_update` and the `update` of `kanban_milestone`; `kanban_get` shows the objective of a ticket.
- Interface: the objective page, a tab of the editor (`?objective=<n>`): title, state, date, progress, *Reach the objective* (disabled with its reason), *Reopen*, *Abandon*, *Delete* (its tickets and milestones stay), description, its tickets grouped by milestone then the lone ones (status, blockers), its milestones (the default ones marked), notes, history. The *Milestones* view shows a section per open objective in order (date, progress, ↑ ↓, *New objective*) with the cards of the milestones it is the default of, then the milestones without one; the closed ones under *Closed*. The ticket view chooses the objective of a root or a lone ticket (*New objective…*), a step shows its root's; the milestone page chooses its default objective; a card shows its objective; an objective filter (all, none, one) narrows Board and Roadmap.

## Statuses and transitions

Tickets move with buttons only (no drag and drop).

| Status | User buttons | The model may |
|---|---|---|
| New | Briefing, Generate the plan; *More actions*: Abandon | — (a plan moves the ticket to *To do* by itself, whoever writes it) |
| To do | Start development (→ In progress: branch, worktree and setup commands, no conversation); *More actions*: Back to New, Redo the plan (assistant or Claude Code), Start anyway (blocked), Abandon | — |
| In progress | Review, Dev session (the main action until a development conversation is linked), Send to testing; *More actions*: Open the worktree, New dev session (once one exists), Develop with Claude Code, Abandon | move to *To test* (with how to test) |
| To test | Add feedback, Review, with open test feedback Fix feedbacks (assistant or Claude Code, all the open feedback), else Validate the step (a ticket with open children) or Validate the ticket (→ Done); Fix session (per open feedback), Create the pull request in their sections; *More actions*: Open the worktree, Back to In progress, Develop with Claude Code, Abandon | mark feedback handled |
| Done / Abandoned | Reopen (Done → To test, Abandoned → New) | — |

Every status also has *Change the status…* in *More actions*: the user sets any status, with an optional reason kept in the history, for work done outside the flow (committed on main, covered by another ticket). It makes no branch, worktree nor session; Done and Abandoned go through Close and Abandon (the worktree is removed). The pod still refuses what breaks a lineage (a root closed before its steps) and Done with open test feedback; the model keeps its own transitions only.

A change of status never starts a conversation: the user starts each session (*Start development* makes the branch and the worktree, then *Dev session* starts the development); only the `start_dev` card of the Orchestrator does both, as its click asks to develop and the Orchestrator follows that session.

The header keeps the main actions; *Briefing*, *Generate the plan*, *Dev session* and the *Fix session* of a feedback are split buttons: a click starts the assistant, the arrow offers Claude Code instead (in a terminal). *Review* (In progress, To test) is a split button too, with Claude Code only for now: a review of the work whose findings become test feedback, plus a note with the verdict. The other actions are in the *More actions* menu.

A feedback leaves the ticket in *To test*; while one is open, the ticket and its step cannot be validated (the pod refuses too). Closing or abandoning removes the worktree; the branch is kept (abandoning offers to delete it). The change is frozen in the ticket when it is merged, or else when it is closed.

## Linked conversations

`chat.ticket = { id, role, feedback? }`; roles:

- `briefing` (Briefing mode, New): clarify the need with the user, questions with `ask_user`, findings written into the ticket;
- `plan` (Plan mode): writes the plan and the goals (the ticket moves to *To do*);
- `dev` (Build mode, in the worktree): follows the plan, checks goals, commits on the ticket branch, moves to *To test* with how to test;
- `correction` (Build mode, in the worktree): handles one test feedback (`feedback`, started from it, which records the conversation) or the open ones, and marks each one handled;
- `resolve`: resolves the conflicts of a rebase (worktree) or a merge (main folder).

The system prompt receives the ticket as it is now and the instructions of the role.

## Steering the day: the Orchestrator mode

A new conversation starts in Orchestrator mode (setting *Mode of a new conversation*). It reads the project and the kanban and changes nothing:
- `kanban_next`: the tickets that can start now, in order (the first open step of a lineage in progress, then priority, smaller size, age; blocked tickets listed apart with what they wait for), then those waiting for the user (to test, open feedback);
- `kanban_history { from, to }`: the tickets whose history has events in a period (yesterday 00:00 to now by default, local time of the pod), closed ones first, with their moves;
- `list_conversations`: the conversations of the project (mode, linked ticket, state, sub-agents);
- `action_card { kind, ticket | chat, label, reason }`: a button in the thread, run only on the user's click: *start_dev* (as *Start development* then *Dev session*, refused with the blockers of a blocked ticket), *generate_plan*, *open_ticket*, *open_conversation*; the card then shows what happened and stays disabled;
- `open_conversation { chat | mode, title, ticket, message, send }`: opens an existing conversation, or creates one (Briefing or Plan, maybe linked to a ticket; Build without ticket) whose first message is sent at once or left in the message box; the windows showing the orchestrator conversation switch to it, and its thread keeps a link back.
Its sub-agents may start sub-agents of their own (depth 2); a sub-agent of the Orchestrator works in Plan mode unless told otherwise.

## From an idea to tickets: the Briefing mode

The assistant has four modes (Shift+Tab cycles through them): Orchestrator (the default of a new conversation, see below), Build, Plan and Briefing. In Briefing mode the model changes nothing: its prompt (editable, `briefing-prompt.md` / `.ide/briefing-prompt.md`) makes it question the user in rounds of `ask_user` until the need is clear, then write it as one or several tickets with `kanban_create` when the user agrees. The first ticket created links the conversation to it (role `briefing`), so `kanban_update` and `kanban_add_note` refine it; the other tickets created list the conversation too. A ticket may have several briefing conversations.

The usual path: a briefing makes the tickets, *Generate the plan* writes the plan of each one, then development sessions build them.

## Assistant tools

- In every conversation: `kanban_list`, `kanban_get`, `kanban_create` (a new ticket in the backlog, `parent` and `depends_on` included), `ask_user` (1 to 10 questions shown one at a time in the thread, see below).
- Only in a conversation linked to a ticket, and only on that ticket: `kanban_update` (title, description, priority, how to test, files, parent, dependencies), `kanban_add_note` (1000 characters max: notes are for decisions, not reports), `kanban_set_plan` (plan and goals with their description), `kanban_goal` (check, uncheck, add, edit, delete), `kanban_feedback` (add a test feedback, mark one handled or open again), `kanban_move` (*To test* only, with how to test).
- A description over 1500 characters is refused with advice to shorten it.

Each question of `ask_user` has a type that picks its widget: `choice` (the default: options, with `pros` / `cons` if given), `idea` (one proposal answered No / Yes, but… / Yes / Exactly, or by a swipe on a touch screen), `compare` (exactly two approaches side by side), `rank` (2 to 8 items to order, `top` to keep only the first N) and `scenario` (a `situation`, then options). Any question also takes a free answer, "I don't know" (the model then offers examples), "Up to you" (it decides and says what) and a note. The pod checks the rules of each type: a call breaking one is refused with an error naming the question, so that the model asks again; a question without type works as before.

One call may hold a small graph of questions: a question with an `id` is asked only when an option naming it in `next` is chosen (for an idea, `nextYes` / `nextNo`); the others are asked in order. The card walks the graph without the model and shows the answers that led to a question as a breadcrumb. A free answer, "Yes, but…" or "I don't know" on a question with branches leaves the anticipated path: the round is sent at once, and the model is told where the user left it and which questions were not asked. The pod refuses unknown ids and cycles.

## Development by a CLI agent (Agents tool)

The kanban settings choose the **development agent**: the assistant (default) or a CLI agent installed for the Agents tool (Claude Code, opencode, pi; `meta.devAgent`).

- With an agent, *Start development* creates the branch and the worktree as usual, then *Dev session* gives the ticket to the agent (`kanban.agent`): **one session per ticket** in the Agents tool, in the worktree, named `#<n> <title>`, whose first prompt is the briefing of the role (the instructions of `agent/texts.go`, the ticket in Markdown) followed by the message of the button. *Fix session*, *Fix feedbacks*, *New dev session* and the resolution of a rebase in the worktree send their briefing to the same session (refused while it works).
- Every session of the Agents tool gets the **MCP server of the IDE** in `session/new` / `session/load` (`http`, `http://127.0.0.1:<port>/mcp`, the token as a bearer, named `web-ide` like the one users add to Claude Code, so it replaces it): the agent reaches the kanban tools with its worktree as `cwd`, which selects its ticket. pi has no MCP over HTTP: its sessions say so in a notice.
- The ticket view shows an *Agent session* section with *Open the session* (also in *More actions*): the worktree in the window, the Agents tool on that tab. `kanban_get` lists it under *Agent session* with its id; `kanban_conversation` reads it (`chat`: that id); `/review-dev` reviews it, its comments going to the report (`kanban_reply` writes to conversations of the assistant only).
- The worktree removed (merge, abandon), its sessions end, also when no window had it open (`acp.Purge`: their processes in the keeper, their files).

## Claude Code

The pod serves an MCP endpoint (`POST /mcp`, the token of the pod as a bearer) so that Claude Code can work on the kanban next to the assistant: a planning or a debug that needs a stronger model, a task to go faster. Setup, once:

```
claude mcp add --transport http --scope user web-ide http://127.0.0.1:4433/mcp --header "Authorization: Bearer $(cat ~/.web-ide/token)"
```

- Every tool takes `cwd`, the folder Claude works in: the deepest local project holding it gives the kanban; in the worktree of a ticket, that ticket is the default one.
- Tools: those of the assistant, on any ticket (`kanban_list`, `kanban_get`, `kanban_create`, `kanban_update`, `kanban_add_note`, `kanban_set_plan`, `kanban_goal`, `kanban_feedback`, `kanban_move`), plus `kanban_conversation` (a linked conversation of the assistant: messages, `ask_user` answers and the questions still waiting), `kanban_reply` (writes in a linked conversation and starts it, queued while it runs; the questions waiting are left aside), `kanban_answer` (answers the `ask_user` questions waiting, one list of strings per question, then the conversation goes on) and `kanban_start` (*Start development*: branch, worktree, setup commands). The ticket tools are the Kanban plugin's (`plugins/kanban/mcp.go`), the three conversation tools the AI's (`plugins/ai/mcp.go`, they also need the `tickets` capability), both through `Host.MCPTool`: refused on a project where their plugin is off. `list_commands` and `run_saved_command` run the saved commands of the project in its consoles, in the worktree of `cwd` (one console per command: a command running answers so, `restart` restarts it; the call waits `timeout` seconds, 20 by default, then returns the output). A message or answers of Claude carry `author: "claude"`: the thread shows a *Claude Code* badge on the message and *answered by Claude Code* on the questions; the conversation runs in the worktree of the ticket for a development or a correction.
- **Tools of the IDE** (`server/mcp_ide.go`), for Claude Code and the agents of the Agents tool, the window side run by a window of the project of `cwd` (else of its main project; an error when none is open): the consoles (`list_consoles`, `read_console`, `console_input`), Docker (`docker_ps`, `docker_logs`, with `profiles`), app previews (`share_preview`: a notification with *Open*, nothing runs before the click; `list_previews` with their URLs), `open_file`, `open_diff` (the uncommitted changes in the diff view), `focus`, `show_message` (a notification) and `request_doodle` (the doodle editor opens in the window, *Send to the agent* returns the PNG as MCP image content with its text description; closing it answers so; 15 min at most). Each reuses the code of the tool of the assistant and keeps its rule: these run freely there too.
- **`ask_user`** (`server/mcp_ask.go`): the questions of the assistant's types (and a `context` shown above them) wait in the Questions tool of the windows of the project; the call returns the answers. It waits 4 minutes at most, then answers that the questions are still open with their id: the agent calls again with `wait` set to it.
- Inventory of the assistant tools (#68): the file, `bash`, LSP and skill tools stay in the assistant (the CLI agents have their own); the kanban and saved command tools were already here; the consoles, Docker, previews and window tools above moved here; `ask_user` goes to the Questions tool (#65); the board, sub-agents and Orchestrator tools stay in the assistant. The assistant has no Compose action nor tunnel tool: none is added.
- Author `claude`: the rules of the model, plus *To do* → *In progress* through `kanban_start`; shown as Claude in the notes and the history.
- File links: `/open?path=<absolute path>&line=<n>` (cookie or bearer) opens the file in the windows of its project (`ide.open` event), or the project page on it (`?open=`) when none has it; the instructions of the endpoint ask Claude to write the files it mentions as such links.
- In the IDE: the arrow of *Briefing*, *Generate the plan*, *Dev session* and *Fix session*, and the Claude Code entries of *More actions*, run `claude` with the prompt fitting the action in a terminal of the project (in the worktree for `dev` and `fix`; `--model opus` for `brief` and `plan`; `WEBIDE_CLAUDE` in the environment of the pod names another command), and the kanban settings show the `claude mcp add` command to copy.
- Links to the IDE (`kanban_list`, `kanban_get`, `kanban_create`, `kanban_start` answers, file links) use the public address of the IDE: `publicUrl` in `config.json` (or `-public-url`) when set; else, for a paired pod with a relay, its page on the relay origin (`https://<relay>/pod/<device key>`, opened in the frame of the bootstrap page); else `http://<addr>`. When the public address is not the local one, the instructions of the MCP server give the local address too (`http://127.0.0.1:<port>`), and Claude Code writes both links. `/project/<id>?ticket=<n>` opens the tab of a ticket.
- The Claude Code mod `claude-mod/` (`claude --plugin-dir claude-mod`, or copied into a plugin folder): above the prompt, links to the project Claude works in and to the ticket of its worktree, and the files it changed, each a button opening the file in the IDE (the mobile app draws no band above the prompt: it gets the same links in a pane, opened when it joins the session and by `/webide`); `/webide <file[:line]>` opens any file. It reads the token in `~/.web-ide/token` (`WEBIDE_DATA`, `WEBIDE_URL` to change). Tests: `claude plugin test claude-mod`.
- A session started at the root of the project moves into the worktree of a ticket with `EnterWorktree` (the worktrees of the IDE are git worktrees): `kanban_start` and the `dev` / `fix` prompts ask for it.
- `kanban_conversation` takes `last` (the last messages only) and `tools` (the tool calls, their errors, the failed answers and the notes): what a review of a development needs. In this repository, the Claude Code skill `/review-dev` (`.claude/skills/review-dev`) reviews the tickets in progress (conversation, worktree, goals), reports, and comments with `kanban_reply` without asking, keeping for the user what is theirs to decide (`--dry`: propose only).
- Prompts, commands of Claude Code: `/mcp__web-ide__brief <n>` (take over a briefing of the assistant: check the need, complete the ticket), `plan`, `dev`, `fix` (`/mcp__web-ide__fix <n> [feedback]`: one feedback, else all the open ones), `review` (read only: findings as test feedback, the verdict as a note; Opus), `coverage <milestone>` (read only: the e2e coverage of each user story of a milestone, the gaps as milestone feedback, the verdict as a milestone note).

## Git

- *Start development*: `git fetch` (when there is a remote), then the branch `ticket/<n>-<slug>` is created from the base (default `origin/main`, else `main`, configurable per project and per ticket) in a worktree `<project>/.ide/worktrees/<n>-<slug>`. Without a repository, or in one without a commit yet, the IDE offers to develop in the project folder instead.
- The setup commands run in a new worktree: the project commands flagged `setup` (Console ▶, *Edit commands…*; `npm install`, `cp ../../../.env .`…), in their order, each in its folder, stopping at the first failure; their log and state are kept in the ticket. The setup command of the kanban settings of earlier versions became such a command (*Worktree setup*) the first time the project opened or a worktree was set up (by the Kanban plugin); the kanban settings list the setup commands and open the modal.
- The worktree is a separate project, not listed on the home page, shown in the same window: from its ticket (*Start development*, *Open the worktree*, a development conversation) or from the worktree selector of the menu bar, which shows *Ticket #<n>* in place of the branch. Its files and consoles carry the chip `#<n>`. A development conversation keeps working in the worktree of its ticket whatever worktree the window shows.
- The model manages the branch and its commits (messages start with `#<n>`). The user triggers the merge (`merge --no-ff` by default, or squash) into the local base branch of the main folder (never pushed; uncommitted changes of the main folder are put aside with `--autostash` and applied again after the merge, also after a conflict once it is committed or aborted) and the rebase on the base (`--autostash` as well for the changes of the worktree).
- Pull request (*To test* stage): with a remote `origin` and the `gh` command, *Create the pull request* pushes the branch (`git push --set-upstream origin`) then runs `gh pr create` (title `#<n> <title>`, base the ticket base without `origin/`, body: description, goals, how to test); its address is kept in the ticket. The only push of the IDE, always asked by the user.
- Conflicts: no automatic abort; the ticket lists the conflicted files with *Continue*, *Abort* and *Resolution session*.
- From *In progress* on: changed files and their diff against the chosen base (from the merge base), including uncommitted changes and untracked files of the worktree.

## Interface

Board in an editor tab (columns New, To do, In progress and To test; Done and Abandoned folded; cards show the open feedback), its **Roadmap** view and its **Milestones** view (switch in the toolbar, remembered by the browser; a milestone filter for Board and Roadmap): one row per lineage not merged yet, without row labels, its blocks in order on a sequence axis without dates, each one as wide as the size of its ticket (a dotted edge when not estimated); a row waiting for another lineage starts where that one ends, with an arrow; the lineages linked by dependencies are grouped, as a staircase by start, a gap between two groups. Ready tickets stand out (accent border, ▶), blocked ones are dimmed with a lock, New ones hatched, the finished steps of an open lineage greyed; a click or Enter opens the ticket, the arrow keys move between blocks; the filter applies to both views. ticket detail in an editor tab, compact list in a side panel (its *Board*, *Roadmap* and *Milestones* buttons open the board on that view), kanban settings (default base; the setup commands of a worktree, edited in the commands modal). Every window follows the changes (`kanban.changed` event).
