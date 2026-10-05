# Changelog

All notable changes of Web IDE. The release workflow publishes the section of a version as the notes of its GitHub release.

## [Unreleased]

### Added

- Claude Code on the kanban: an MCP endpoint of the pod (`/mcp`) gives Claude Code the tools of the assistant on the tickets, the conversations of a briefing, the start of the development, and the commands `brief`, `plan`, `dev` and `fix`; its changes are shown as written by Claude.
- *Claude Code* menu of a ticket (brief, plan, develop, fix) running Claude Code in a terminal of the IDE; the setup command in the kanban settings.
- File links (`/open?path=…&line=…`): a click on a file mentioned by Claude Code opens it in the IDE.

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
