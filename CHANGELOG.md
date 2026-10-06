# Changelog

All notable changes of Web IDE. The release workflow publishes the section of a version as the notes of its GitHub release.

## [Unreleased]

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
