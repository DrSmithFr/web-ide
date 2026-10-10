# Changelog

All notable changes of Web IDE. The release workflow publishes the section of a version as the notes of its GitHub release.

## [Unreleased]

### Added

- The cards of the board list the titles of their goals and show the time of the answers of their conversations; the ticket view shows it next to each conversation (sub-agents included, the thinking in the tooltip).

## [1.6.0] - 2026-10-10

### Added

- *Review* of a ticket (*In progress*, *To test*): Claude Code (Opus) reviews the change of its branch, runs the tests, adds each finding as a test feedback and the verdict as a note (MCP prompt `review`). A split button: the integrated assistant will join it.
- Split buttons: *Briefing*, *Generate the plan*, *Start development* and the *Fix session* of a feedback start the integrated assistant on a click; their arrow offers Claude Code instead, in a terminal. The MCP prompt `fix` takes the id of one feedback.
- *Fix feedbacks* replaces the validation of a ticket while a test feedback is open (one session for all of them, assistant or Claude Code); the pod refuses to close the ticket or validate its step until they are handled.
- The model adds test feedback (`kanban_feedback` action `add`) and edits or deletes goals (`kanban_goal` actions `edit`, `delete`), for the assistant and Claude Code.

### Changed

- The ticket view lists the sections of its status first, then, under a rule, those of the earlier statuses, folded; lineage, linked files, attachments and history aside. A closed ticket keeps the sections with content.
- The header of a ticket keeps its main actions (*To test*: *Add feedback*, *Review*, *Validate*); the others, *Open the worktree* among them, are in *More actions*. The *Claude Code* button is gone: its actions are in the split buttons and *More actions*. *Close the ticket* is now *Validate the ticket*, shown only once its lineage is finished, else *Validate the step*.
- The priority and the size of a ticket are fixed once its development started: badges in the view, refused by the pod (a plan written later keeps the size).
- The pull request section shows once a pull request exists or can be made.

### Removed

- Linked commits (the section, `kanban_link_commit`): *Git and changes* shows the commits of the branch.

### Fixed

- The context of the llama-swap models survives a restart of the pod (kept in `llm-contexts.json`).

## [1.5.0] - 2026-10-09

### Added

- Claude Code answers the local assistant: the MCP tools `kanban_reply` (a message in a conversation linked to a ticket) and `kanban_answer` (the `ask_user` questions waiting); `kanban_conversation` lists the questions waiting. The thread marks what Claude wrote or answered.
- The Orchestrator adopts a conversation that runs on its own (`agent_adopt`), and follows from their start the developments its cards start: such a conversation becomes its sub-agent, announces itself with a note, keeps working with the user and reports when its task is over.
- The Orchestrator resumes a conversation whose last answer failed (`agent_resume`), from its last completed step, and adopts it on the way.
- `kanban_conversation` (MCP) reads the last messages only (`last`) with the tool calls, errors and notes (`tools`); the skill `/review-dev` of this repository reviews the developments in progress with it.
- Reasoning effort of the assistant (options of the message box): *Dynamic* by default, or fixed at *Max*, *Medium* or *Low*. Dynamic thinks the most after a message of the user, medium after tool results, low after a step of simple edits or bookkeeping that succeeded, the most again after a failed or refused call. The *Effort tool* switch offers `set_effort` to the model, which then chooses the level until the user writes again. The block of the reasoning shows the effort of the answer. Sent as `reasoning_effort` in `chat_template_kwargs` (Qwen 3.8 templates; Strata needs `"effort_position": "end"` to keep its conversation cache when the effort changes).
- A server of kind *OpenAI-compatible* that is llama-swap is treated as the local servers behind it: its models can think (the *Thinking* option and the effort show), and it gets the fields of llama.cpp (`chat_template_kwargs`, the progress of the prompt reading).
- Statistics of the assistant, a view next to the board (tabs *Board* / *Statistics* in its column, a button in the header): speeds of reading and writing, cache, time generating and thinking (with the estimated share of the thinking tokens), by reasoning effort, time and failures by tool, answers calling several tools at once, repeated calls, the context now estimated by part, its curve and the compactions. *This conversation* adds its sub-agents (each detailed); *Whole project* filters by model, effort and period. Followed live during an answer.
- Each message of a conversation keeps when it came (`at`); a tool call keeps its wait for the approval (`waitMs`) and the kind of its failure (`failure`: `usage` for a wrong call — a parameter missing or invalid, an unknown tool, an `old_string` not found —, `exit` for a command ending with a code, `error`).
- The local terminals survive the updates of the pod: they run in a second service, the keeper (`web-ide-keeper`, installed by `install.sh` next to the pod, replaced only when its protocol changes). Restarting the pod no longer ends the shells; the page reconnects and shows their output from where it was, without gap or duplicate. Without a keeper (`-keeper off`), the pod runs them as before.
- The conversations of the assistant survive the updates of the pod too: the answer being written goes on through the keeper (an HTTP relay keeps it), a command of the agent running (`bash`, `run_command`) is followed again, an approval waiting is asked again, without asking the model twice. The keeper speaks protocol 3.
- The terminals and the commands of the agent of an SSH project run in the keeper too, on its own connection (the host key accepted by the pod, pinned): they survive the updates of the pod.
- The keeper updates itself in place (`web-ide-keeper keeper -upgrade`, used by `install.sh`): it re-executes its new binary in the same process, keeping its local terminals and their output; it waits for the answers being written and the SSH commands (or cancels them with `-force`), closes the SSH terminals, and stays as it was when the update fails. A keeper older than this one is restarted once.

### Changed

- The conversations of the assistant: the last Orchestrator conversation first with its working children, then the other active conversations as trees, then the history by day; an ended sub-agent leaves its parent for the history of the day it ended.
- An orange dot marks a conversation waiting for an answer, a red one a conversation whose last answer failed; both stay among the active conversations. *Abandon* leaves a failed conversation (a sub-agent is stopped, its parent told). The older Orchestrator conversations with working children keep their tree at the top.
- Worktrees open in the same window: the worktree selector (and *Start development*, *Open the worktree*) switches the explorer, the search, the Git tool and the new consoles without reloading; tabs and consoles of several worktrees stay open side by side, each with a chip (`#<n>` or the branch). The selector shows *Ticket #<n>*; *Open in a new window…* keeps the former way. A development conversation keeps working in the worktree of its ticket.
- The steps of an answer are blocks: the reasoning like the tool calls, each with its duration, counted while it runs. The running block is open and follows its stream (the reasoning, the output of a command as it comes); it folds once done, unless the user toggled it.
- The system prompt asks the model to put the tool calls that do not depend on each other in one answer: fewer round trips, and less thinking between them.

### Fixed

- git run by the assistant takes `auto` as its comment character: a `git rebase --continue` no longer strips the `#<n>` subject of the ticket commits.

## [1.4.0] - 2026-10-07

### Added

- Sub-agents: the assistant delegates a task with `spawn_agent` to a child conversation (fresh context, same rights) running in the background; the child notes its progress, asks its parent and reports, its questions and report waking the parent, which replies (after asking the user if needed), writes to or stops it. What a child needs confirmed goes to the user. Cards in the parent thread, children nested under their parent in the history.
- Cloud providers: a server kind *OpenAI-compatible provider* (OpenAI, OpenRouter…) with its API key kept on the pod, models listed or typed by hand, reasoning and cost read from the stream, rate limits retried and errors explained. Servers can be offered to sub-agents with a note; the assistant picks the server and model of a child (default in the new *Sub-agents* settings tab), whose card shows its tokens and cost.
- Orchestrator mode, the default of a new conversation: it tells what to work on next (`kanban_next`) and what was done (`kanban_history`), offers actions as cards the user clicks (start the development, generate the plan, open a ticket or a conversation), and moves the user into the right conversation (`open_conversation`: an idea goes to a Briefing). It changes no file; its sub-agents may delegate once more.

### Fixed

- A message queued just as a conversation ended stayed in its queue: the conversation now starts again with it.

## [1.3.0] - 2026-10-07

### Added

- Phone layout (720 px wide or less): one bar of three rows with the menus and a single row of icons (the editor first, then the tools), one view at a time full screen, the editor brought back when a file is opened and locked until a double tap (a padlock locks it again), no field focused by itself, the caret line kept above the keyboard, and no zoom of the page.

## [1.2.0] - 2026-10-06

### Added

- Whiteboard of a conversation: its doodles as read-only pages, next to the conversation in a wide assistant or in its place in a narrow one, with zoom, pan, thumbnails and *Reuse*; *Show on the board* from a doodle of the thread.
- The assistant draws on the board: `board_draw_doodle` (layouts, flows, sketches, or annotations on a clone of a page) and `board_draw_image` (an SVG it writes, an image of the project, or a capture of the screen shared by the user); it reads back the description and the image of each page, and its pages go to the tickets like the doodles. The images sent in a conversation are pages of its board too.
- App previews: the assistant offers to try the app with `share_preview`; a click starts its command, waits for its port and opens it on a temporary HTTPS URL of the tailnet (private, the cookie of the IDE required), or public with Tailscale Funnel; it stops with its command. Listed in the new *Previews* tab of the Docker tool.

### Fixed

- A transparent GIF or WebP put under a doodle had a black background: only JPEG images are kept as JPEG.

## [1.1.0] - 2026-10-06

### Added

- Claude Code on the kanban: an MCP endpoint of the pod (`/mcp`) gives Claude Code the tools of the assistant on the tickets, the conversations of a briefing, the start of the development, and the commands `brief`, `plan`, `dev` and `fix`; its changes are shown as written by Claude.
- *Claude Code* menu of a ticket (brief, plan, develop, fix) running Claude Code in a terminal of the IDE; the setup command in the kanban settings.
- Claude Code mod (`claude-mod/`): links to the project and to the ticket of the worktree, and the files changed by Claude, above the prompt, opened in the IDE in one key; `/webide <file[:line]>`.
- `publicUrl` (config, `-public-url`): the address of the IDE in the links given to Claude Code (a Tailscale name…); `/project/<id>?ticket=<n>` opens a ticket.
- File links (`/open?path=…&line=…`): a click on a file mentioned by Claude Code opens it in the IDE.
- Lineages of tickets: the steps of a ticket are developed one after the other in its worktree and on its branch, each one once the previous one is validated, and the ticket is merged once they are finished; a ticket may wait for tickets of other lineages (merged or done). Blocked starts are refused to the models and can be forced by the user; badges on the cards, a *Lineage* section in the ticket, `parent` and `depends_on` in the kanban tools.
- The agent of the assistant runs in the pod: a conversation goes on with its window closed (or the phone asleep) and shows up again when a window opens, several conversations run at once (a number per model server, the others wait in a queue shown in the conversation and the history list), and every window following a conversation can write to it, answer its questions or confirmations, or stop it.
- Question types of `ask_user`: an idea to validate (swipe on a phone), two approaches compared side by side, items to rank (or a top N), a concrete scenario, or choices with their pros and cons; on any question "I don't know", "Up to you" and a note. A call breaking the rules of a type is refused with an error naming the question.
- Graphs of questions in `ask_user`: a follow-up is asked only when the answer leading to it is chosen, with a breadcrumb; leaving the anticipated path (free answer, "Yes, but…", "I don't know") sends the round at once so the model rethinks.
- Roadmap view of the kanban: a row per lineage, blocks as wide as the estimated size of their ticket (S, M, L, XL, written with the plan), what can start, what is blocked, and the dependencies between lineages at a glance.

### Changed

- A stopped or failed answer of the assistant resumes from its last completed step (*Resume*) or starts again from the user message (*Retry*).
- The counters of an answer show the speed of the prompt reading and the share of the prompt found in the cache of the model server.
- Reloading the page or closing it no longer stops a conversation, and a second window following it can write to it instead of being read-only.

## [1.0.0] - 2026-10-05

First release.

### Editor and project

- Block editor for large files (100,000 lines), syntax highlighting with the CSS Custom Highlight API, split panes sharing buffers, multiple carets, folding, indentation guides, find and replace, encodings and line separators, QWERTY and AZERTY shortcuts, themes (including high contrast) with keyboard navigation of the whole interface.
- File explorer with file type icons, git colors and folder marks; Search Everywhere, Recent Files, the switcher and a clipboard history.
- Three-way merge of the changes made to open files by other programs, with a resolution dialog for real conflicts.
- Code navigation through language servers: definition, references, implementations, symbols, completion, rename, formatting, diagnostics.
- Local and SSH projects, project icons, the same session in every browser.
- Terminals and commands, detachable into their own windows.
- Git panel: status, staging, commits, branches, history graph, side-by-side diffs, worktrees.
- Database explorer for SQLite, PostgreSQL and Redis; Docker tool (Compose stack, containers, logs, disk usage) and SSH tunnels.

### AI assistant and kanban

- Agent for llama.cpp and Ollama servers: reads, searches and edits the project, uses the language servers, runs commands, asks questions, follows `CLAUDE.md` / `AGENTS.md` and skills; Build, Plan and Briefing modes; context compaction; Mermaid diagrams; image, PDF and audio attachments; local dictation (Whisper in the browser); doodles to show a layout or a flow.
- Kanban per project: briefing, plan, development in a git worktree per ticket, test feedback, merge, rebase and pull request, each step with its linked conversation.

### Install

- Single binary with the front end embedded; Linux and macOS archives.
- `scripts/install.sh` installs a release as a systemd user service started at boot, optionally served over HTTPS on a Tailscale network.
- English and French interface.
