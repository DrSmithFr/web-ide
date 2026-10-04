# Design specification

This document describes what Web IDE is meant to do and the design decisions behind it. For how the code is organised, see [architecture.md](architecture.md); for the kanban, see [kanban.md](kanban.md).

## 1. Overview

An IDE that runs in the browser, usable without installing anything on the front-end side thanks to a small local agent, the **pod**, which talks to the web page over a WebSocket.

- A browser alone cannot open a TCP socket or an SSH connection.
- The pod is a single binary (Go, no runtime dependency) running on the user's machine.
- The pod gives access to the local disk, to SSH (with the local key set), to databases, terminals and language servers.
- Private keys never leave the machine and are never sent to a remote backend.

## 2. Stack

- TypeScript across the whole front end.
- **SolidJS** for the IDE chrome (menus, tabs, panels, settings, status, pop-ups): fine-grained reactivity with signals, no virtual DOM.
- **Editor core** (buffer, cursor, selection, highlighting): plain TypeScript without a framework, manipulating the DOM directly to avoid diffing on every keystroke.
- All persistent state lives on the pod side in `~/.web-ide`: settings (with snapshot history), project registry, per-project sessions. The browser keeps nothing essential.
- No WebAssembly to start with: plain JS first, profile later. Candidates if a bottleneck appears: the three-way merge and multi-file search.

## 3. Pod (local agent)

- WebSocket connection on a fixed, documented port (`127.0.0.1:4433` by default).
- Access to local and remote (SSH/SFTP) files: read, write, watch.
- Streams files to the front end and pushes new versions when a file changes on disk (for example when an AI agent edits it).
- Holds the session of each project (section 9).
- Hosts database connections (section 11) and language servers (section 12).
- Generic file reading by absolute path, not limited to the project root (needed for dependency sources and stubs, section 12).

Technical points:

- **Mixed content**: the front end is embedded in the pod binary and served from the same origin, so there is no HTTPS page talking to `ws://localhost`.
- **Pairing**: without a check, any website open in the browser could talk to the pod. The pod prints a token once; opening the URL with `?token=…` stores it in an HTTP-only cookie.
- **Persistence**: a systemd user service (`make service`) or a one-off run (`make run`). No tray icon.

## 4. Browser file access (fallback notes)

- File System Access API (`showDirectoryPicker()`, `showOpenFilePicker()`): Chromium only, write permission asked per session.
- Drag and drop: `DataTransferItem.getAsFileSystemHandle()` in Chromium.
- `<input type="file" webkitdirectory>`: everywhere, read-only, not persistent.
- OPFS: sandboxed storage invisible to the user, only useful as a cache.
- None of these APIs gives a raw network socket.

To treat local and SSH projects the same way, the pod exposes one interface (`list`, `read`, `write`, `watch`) for both.

## 5. Layout

```
+--------------------------------------------------+
| Menu bar (project, branch)  pod status, settings |
+----+-----------+-----------------------+---+-----+
| ic | Explorer  |  Tabs                 | T | ic  |
| on |           |  Editor               | o | on  |
| s  |           |                       | o | s   |
|    |           |                       | l |     |
|    +-----------+-----------+-----------+---+     |
|    | Console (tabs)        | Problems      |     |
+----+-----------------------+---------------+-----+
```

- **Menu bar**, from left to right: home button; project icon (click: icon editor), project title and the **worktree selector** (current branch, or the ticket of a worktree window); the File, Edit, Navigate… menus; then on the right conflicts, cursor position, pod status (connection state, download and upload rate in bytes/s over a sliding window of about one second) and settings.
- **Worktree selector**: lists the main folder, the worktrees of the tickets (with their status) and the other worktrees of the repository; choosing one opens its window, or brings back the one already open. *Open a branch…* checks an existing or new branch out in its own worktree (`.ide/worktrees/b-<branch>`) opened in its own window: the main folder is never switched, so its uncommitted changes never get in the way. The setup command of the kanban (`npm install`…) runs in a console of the new worktree window. *Remove a worktree…* deletes one of these worktrees and its project (confirmation when it has uncommitted changes); the branch is kept, and a window open on it goes back to the main folder. Ticket worktrees go with their ticket.
- **Editor window**: explorer panel on the left, central editor with a tab bar, tools panel on the right.
- **Icon rails** on both sides, the full height of the window, each with a top and a bottom group: four tool zones (top left, bottom left, bottom right, top right). Each icon toggles its tool; clicking the active icon hides it; each zone shows one tool at a time.
  - Top left: file explorer, global search, Git, kanban.
  - Top right: AI assistant, database explorer, structure, conflicts, infos. Infos stacks three sections: properties (project, target, pod, active tab), connections (local SSH keys, hosts of `~/.ssh/config`) and extensions (language servers).
  - Bottom left: Console, the consoles as tabs (terminals, build output, commands).
  - Bottom right: Problems, with two tabs: the diagnostics of the language servers and their output. A dot on its icon signals errors.
- **Bottom strip**: the tools of the two bottom zones, under the side panels and the editor. One shared height; side by side, the border between them can be dragged; alone, a tool takes the whole width.
- **Split view**: recursive pane tree (split right, split down). A file open in several panes shares one buffer, never two copies.

## 6. Editor

### 6.1 Syntax highlighting

- Language detected by extension first, then by content (shebang, `<?php`, `package main`, `server {` blocks).
- At least PHP, JavaScript, TypeScript, Python, Go and nginx configuration.
- One grammar per module: an ordered list of `{token type, regex}` rules; the first rule matching at the current position wins. Adding a language does not touch the engine.
- Rendered with the **CSS Custom Highlight API** (`CSS.highlights`, `::highlight()`): one `Highlight` per token type, no `<span>` in the DOM, no caret jumps.
- API limits: only `color`, `background-color`, `text-decoration`, `text-shadow`, `font-style`, `caret-color` and `text-emphasis-color` apply. No `font-weight` (so no bold); italics are possible.
- Re-tokenising invalidates the `Range`s: tokenising is incremental and the editor renders in blocks for large files.

### 6.2 Find in the open file

- Floating bar at the top right of the editor, toggled by `F3`.
- Query field and three options: `Aa` (match case), `ab|` (whole word), `.*` (regular expression).
- `n / N` counter, previous/next buttons wrapping around, close button.
- Text mode escapes the query; regex mode uses it as is. An invalid pattern shows an error instead of the counter.
- Rendered with the same API: a `search-match` highlight for all matches and a higher-priority `search-current` for the current one.

### 6.3 Navigation shortcuts

- `Ctrl+Arrow`: native browser behaviour, not intercepted.
- `Alt+Arrow`: jump between case boundaries inside a word (`foo|Bar`, `XML|Http|Request`, around `_` and `-`, between letters and digits). `Shift+Alt+Arrow` extends the selection.
- `Ctrl+Alt+R`: opens the conflict resolution dialog of the current file (no effect without a conflict).

## 7. Keyboard shortcuts

- **Centralised** binding table: an action (`search.find`, `conflict.resolve`, `view.splitRight`…) maps to a key combination stored by physical `code`, not by `key`.
- Default **QWERTY** and **AZERTY** presets (two data files).
- Layout detection with `navigator.keyboard.getLayoutMap()` when available (Chromium), otherwise a manual setting.
- User changes are stored as overrides (`action → binding`) on top of the preset, and can be reset per action.
- Assigning a combination checks for collisions and asks before overwriting.
- Menus and the command palette show remapped shortcuts without a restart.

## 8. Synchronisation and conflicts (files changed by other tools)

- The editor works on an in-memory copy of each file, never directly on the pod stream.
- Three versions per open file:
  - `base`: content when loaded;
  - `local`: current buffer, with unsaved changes;
  - `remote`: new version pushed by the pod (typically after an AI agent changed it), with a revision number.
- On each `remote`: line-based **three-way merge (diff3)**.
- **Clean merge**: applied silently, `base` becomes `remote`, discreet toast.
- **Conflict**: the file enters a conflict state; the local buffer is not overwritten until the user decides.
- Conflict UI: warning banner on the tab, a **Conflicts** tool listing conflicted files, and a three-pane resolution dialog (local changes, editable result, new version) with per-block accept buttons.

## 9. Session and persistence

**Pod side, per project**: open files, cursor position per file, consoles, split layout (tree, sizes, active file per pane). Pushed over the WebSocket on every change (debounced for the cursor), restored on load or reconnection, so the session is the same from another browser or machine.

**Settings** (`~/.web-ide/settings.json`): themes, fonts, shortcuts, custom highlighting rules, language.

- Full snapshot history on each significant change; entries are never modified.
- A `current` pointer to the active entry; a rollback creates a new entry copying the old one, so a rollback can be undone.
- Bounded number of entries (oldest purged); forced snapshot before importing highlighting rules.
- Every browser connected to the same pod shares the settings.

## 10. Settings, projects and detached windows

### Settings

Large modal with navigation on the left: themes, fonts, keyboard shortcuts, syntax highlighting (add, edit, export rules as JSON per language), language of the interface.

### Home page and projects

- Simple project list. A project is **local** or **SSH**, with an optional title and description (name derived from the path or the host otherwise).
- The project registry lives in the pod. Each project has its own URL, `/project/:id`, with its own pod session.
- Every project is a git repository: creating a project runs `git init` (first branch `main`, an empty first commit when the folder is empty) unless the folder is already in a repository, and adds the optional remote as `origin`. An SSH host not reachable at creation (password) gets it at the first opening (`gitSetup` pending in the registry).
- **Project icon**: a glyph (a subset of Lucide) or 1 to 3 characters on a shape (circle, rounded square, square, hexagon, diamond) filled with a colour or a two-colour gradient in one of 8 directions. Shown on the home page, and as the favicon of every window of the project so that browser tabs tell the projects apart; the worktree of a ticket shows the icon of its project with a dot. The page draws the SVG and saves it in `.ide/icon.svg` with its description in `.ide/icon.json`; a project without one gets one generated (initials, colour and shape from its id).
- Each project has a `.ide` folder: project settings, database connections (without secrets), conversations of the assistant and the kanban (both ignored by git).

### Detached windows

Each panel has an id and can be opened alone:

- `/project/:id/editor`
- `/project/:id/console/:consoleId`
- `/project/:id/tool/:toolId`

Each window is one more WebSocket client on the same pod session; the shared buffers keep them in sync.

## 11. Database explorer

Tool of the right panel for SQLite, PostgreSQL and Redis. All connectivity goes through the pod.

### Tree

- Connections > databases > tables > columns (name and type) and indexes.
- Redis has typed keys (`string`, `hash`, `list`, `set`, `zset`, `stream`) with their TTL, under each `dbN`.
- Context menus: connection (edit, duplicate, delete, refresh), table (view data, view DDL, empty query), column (copy name, copy qualified name), index (view definition).
- Double-clicking a table opens the table view. Status indicator per connection: connected, error, untested.

### Connections

- SQLite: file path (local or over SSH). PostgreSQL: host, port, database, user, password, SSL mode. Redis: host, port, database index, password.
- **Test** button in the form.
- Password either remembered by the pod or asked at each opening (kept for the session).
- Optional **SSH tunnel** per connection (PostgreSQL and Redis).
- Connection settings are stored in the project's `.ide` folder; the page only receives non-sensitive metadata.

### SQL console

- Highlighting by query language (SQL, or Redis commands).
- Several statements separated by `;`; the active one (at the cursor) is outlined. Running with several statements opens a keyboard-driven picker preselected on the active one. `Ctrl+Enter` runs.
- Results in a grid with duration and affected rows; history; auto-commit on/off, commit, rollback, cancel.

### Table view

Paged content of a table without writing a query, sharing the connection of the console.

## 12. Code navigation (LSP)

Goal: PhpStorm-like navigation (declaration and usages, implementations, type declaration, super method, related symbols, tests).

- **LSP** (Language Server Protocol over JSON-RPC): the pod runs the servers, the editor talks LSP through the WebSocket.
- Servers: `gopls` (Go), `intelephense` or `phpactor` (PHP), `pyright` (Python), `typescript-language-server` (JavaScript/TypeScript).
- Requests: `definition`, `references`, `implementation`, `typeDefinition`, `documentSymbol`, `workspace/symbol`, `completion`, `rename`, `formatting`, plus `didChange` for edits. The pod guarantees that a request is sent after the `didChange` notifications received before it.
- Built outside LSP: super method, related symbols, tests (naming convention or per-project configuration).
- **One server per project and language**, with the project root as `rootUri`. The pod detects languages (`composer.json`, `package.json`, `go.mod`…), routes by file extension and stops servers a while after the last window leaves.

### Sources outside the project

Definitions may point outside the project (`lib.es5.d.ts` in `node_modules/typescript/lib`, PHP stubs, `GOROOT/src`, `.pyi` files bundled with pyright). The pod reads any reachable absolute path; such tabs are **read-only** and absent from the explorer. On an SSH project, paths are resolved on the remote host.

### Git

- Git panel: branch (switch, create), ahead/behind, commit message (amend), conflicted / staged / changed sections with stage, unstage, discard, and the recent history.
- Pull, push and fetch run in a terminal of the bottom panel (credential prompts stay interactive).
- Side-by-side diff tab (working tree ↔ index, or index ↔ HEAD); gutter markers for added, changed and removed lines while typing.
- `git` runs through the same executor as the rest, so it also works on an SSH host.

## 13. AI assistant

Right-panel tool talking to a **llama.cpp** or **Ollama** server (address and optional API key, never sent back to the page).

- Markdown answers with highlighted code and Mermaid diagrams, collapsible reasoning, live token counters.
- Agent loop with tools: files (read, search, edit with confirmation or automatically), language servers, shell commands, IDE (open a file, focus a panel), consoles, kanban, questions to the user (`ask_user`).
- Instructions and skills loaded the same way as Claude Code (`CLAUDE.md`, `AGENTS.md`, `.claude/skills`…), editable system prompt, Build / Plan / Briefing modes (the Briefing mode questions the user and writes kanban tickets), context compaction.
- Conversations stored per project in SQLite; an answer survives a page reload and can be followed from another window.
- Local speech recognition: dictation and audio files are transcribed in the browser by Whisper; audio never leaves the page.

## 14. Decisions

- **Q1**: the pod pushes the whole file (`fs.changed`) on each change, after 150 ms of stability (AI tools often write in several steps).
- **Q2**: "accept both" concatenates local then remote.
- **Q3**: credentials are never written in `.ide`. Remembered passwords go to `~/.web-ide/secrets.json` (mode 0600, same model as `~/.pgpass`, not encrypted); otherwise they stay in the pod's memory for the session.
- **Q4**: Redis keys get a dedicated node (type and TTL) under each `dbN`.
- **Q5**: systemd user service (`make service`) or one-off run (`make run`). No tray icon.
- **Q6**: the pod listens on `127.0.0.1` and refuses other machines. `-allow-remote` allows remote access, protected by the token only.
