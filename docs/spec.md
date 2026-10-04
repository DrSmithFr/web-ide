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
|    |           | Status bar  1:1 LF …  |   |     |
|    +-----------+-----------+-----------+---+     |
|    | Console (tabs)        | Problems      |     |
+----+-----------------------+---------------+-----+
```

- **Menu bar**, from left to right: home button; project icon (click: icon editor), project title and the **worktree selector** (current branch, or the ticket of a worktree window); the File, Edit, Navigate… menus; then on the right conflicts, pod status (connection state, download and upload rate in bytes/s over a sliding window of about one second) and settings.
- **Worktree selector**: lists the main folder, the worktrees of the tickets (with their status) and the other worktrees of the repository; choosing one opens its window, or brings back the one already open. *Open a branch…* checks an existing or new branch out in its own worktree (`.ide/worktrees/b-<branch>`) opened in its own window: the main folder is never switched, so its uncommitted changes never get in the way. The setup command of the kanban (`npm install`…) runs in a console of the new worktree window. *Remove a worktree…* deletes one of these worktrees and its project (confirmation when it has uncommitted changes); the branch is kept, and a window open on it goes back to the main folder. Ticket worktrees go with their ticket.
- **Editor window**: explorer panel on the left, central editor with a tab bar, tools panel on the right.
- **Status bar** under the editor area (one for all the split panes, about the active one): caret position and selected characters (a click opens *Go to line*), then for a file its line separator (`LF`/`CRLF`), encoding and indentation, and the language. A click on the line separator or the encoding converts the file; a click on the indentation chooses tabs or 2, 4 or 8 spaces for this file (section 6.7).
- **Icon rails** on both sides, the full height of the window, each with a top and a bottom group: four tool zones (top left, bottom left, bottom right, top right). Each icon toggles its tool; clicking the active icon hides it; each zone shows one tool at a time.
  - Top left: file explorer, global search, Git, kanban.
  - Top right: AI assistant, database explorer, structure, conflicts, infos. Infos stacks three sections: properties (project, target, pod, active tab), connections (local SSH keys, hosts of `~/.ssh/config`) and extensions (language servers).
  - Bottom left: Console, the consoles as tabs (terminals, build output, commands).
  - Bottom right: Problems, with two tabs: the diagnostics of the language servers and their output. A dot on its icon signals errors. Docker (section 14).
- **Moving tools**: an icon is dragged to another group of the rails (same zone to reorder it); a shown tool stays shown in its new zone. The placement is saved in the session; *View › Reset the tool layout* restores the default one.
- **Tabs**: file, console and Problems tabs share one look. File and console tabs are reordered by drag and drop (a marker shows where the tab lands; file tabs also move to another pane); the order of the console tabs is saved in the session.
- **Focus**: the part of the window last clicked or typed in (a tool zone or an editor pane) has the focus; menus, the palette and dialogs leave it unchanged. Its active tab and its rail icon are in the accent color; the active tabs and icons of the other open tools are white (the text color in a light theme). The accent color is the one of the theme or one picked in the settings.
- **Focus outline** (settings, *View › Focus outline*, checked in the menu while on): a 1px accent outline drawn over the edges of the focused part.
- **Visual focus mode** (settings, *View › Visual focus mode*, checked in the menu while on): everything but the focused part and the menu bar is monochrome, rail icons included: the backgrounds of the theme stay, the other colors (text, borders, highlighting) become tones between the background and the text color of the theme; images and terminal text are grayed.
- **File explorer**: a click selects a row, a double click (or `Enter`) opens the file and gives the editor the focus, or opens/closes a folder; the arrows move in the tree. Recognized file types have an icon (a colored badge for languages, a colored line icon for data, images, archives, locks, Docker, git files…). Git colors, as JetBrains IDEs: untracked red, added green, modified blue, conflict dark red, left out by `.gitignore` olive (`git check-ignore`, inherited by their content); a folder takes the color of its changes. Toolbar: locate the active file, expand all (except ignored, excluded, `.git` and `node_modules` folders, 300 folders at most), collapse all, and an *Options* menu: new file or folder, show hidden files, show excluded folders, open files with a single click, always select the opened file, refresh all (options saved in the session).
- **Folder marks** (context menu of a folder): *Source folder* (blue), *Test folder* (green), *Excluded folder* (orange, its content dimmed). Kept in `.ide/folders.json` (shared with the repository). Excluded folders are left out of the global search, *Go to file* and the file tools of the assistant.
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

- `Ctrl+Arrow`: native browser behaviour, not intercepted (with several carets, the editor moves them all by word).
- `Alt+Arrow`: jump between case boundaries inside a word (`foo|Bar`, `XML|Http|Request`, around `_` and `-`, between letters and digits). `Shift+Alt+Arrow` extends the selection.
- `Ctrl+Alt+R`: opens the conflict resolution dialog of the current file (no effect without a conflict).

### 6.4 Display

- The text starts right against the gutter, without a left margin.
- **Indentation guides** (setting, on by default): a thin vertical line at each indentation level, the step being the most frequent indentation increase of the file (else the tab size); blank lines take the smaller indentation of the lines around them. The guide of the block holding the caret (or opened by the caret line) is brighter.
- **Whitespace** (setting and *View › Show whitespace*, off by default): a dot for each space, an arrow for each tab, `↵` at each line end, drawn faintly over the text by an overlay copying the visible lines in transparent characters (exact widths, tab stops included).

### 6.5 Folding

- Ranges: brackets outside strings and comments for the C-like languages (the line of the closing bracket is hidden too and its text follows the placeholder: `func main() {⋯}`), the indentation for Python, YAML, HTML and plain text, the headings for Markdown (outside code fences).
- A chevron in the gutter (shown on hover, always for a folded range) folds or unfolds; a folded range shows a `⋯` placeholder after its header, a click on it unfolds.
- `Ctrl+-` / `Ctrl+=` fold and unfold the block of the caret, `Ctrl+Shift+-` / `Ctrl+Shift+=` every block (numeric keypad too; on AZERTY, `-` is the key of the `6`). Also in the *Code* menu.
- Hidden lines stay in the DOM, in blocks set to `display: none`: offsets, copy and search are unchanged.
- An edit inside a range, or adding or removing lines at its header, unfolds it; moving the caret to a hidden line (go to line, search, navigation) unfolds it. Typing on the header line keeps the fold.
- Folded ranges are kept in the session with the cursor of the file.

### 6.6 Multiple carets

- The primary caret is the browser selection; the other selections are drawn by the editor (a `Highlight` for their text) and follow the edits made elsewhere. With several carets, the editor draws them all, the primary one included (the browser shows no caret at the end of a selection).
- `Alt+J` selects the word at the caret, then adds the next occurrence of the selected text (whole words when started from the word at the caret), which becomes the primary caret; `Shift+Alt+J` removes the last one added; `Ctrl+Alt+Shift+J` selects every occurrence. The first and the last are also in the *Edit* menu.
- Column selection: drag with the middle button, or with `Alt+Shift` and the left button. Lines too short for a caret at the column of the mouse are left out. The paste of the primary selection that Linux does when the middle button is released is dropped.
- `Alt+click` adds a caret or removes the one clicked. `Escape`, a click or a move made by the browser (`Ctrl+Home`, select all, page keys) leaves a single caret.
- Typing, `Backspace`/`Delete` (also by word or line), `Enter` (auto-indent), `Tab`, the arrows (`Ctrl` by word, `Alt` by sub-word), `Home`/`End` and their `Shift` variants act on every caret; each such edit is one undo step, and undo leaves a single caret. A dead key or an input method types at every caret too. Copy joins the selections one per line (the lines of the carets when nothing is selected); a paste with as many lines as there are carets gives one line to each.

### 6.7 File format and indentation

- **Encoding**: the pod decodes UTF-8, UTF-8 with BOM, UTF-16 (with BOM) and, for other non-binary data, Windows-1252; anything else opens as binary. The editor always works on UTF-8 text.
- **Line separator**: CRLF line ends become LF in the editor. A file is written back in its encoding and with its line separator (the most frequent one when they were mixed). A character the encoding cannot hold makes the save fail with a message, and the file keeps its format.
- **Indentation** of each file is detected (tabs, or the most frequent increase of spaces): `Tab`, auto-indent and the guides follow it; files without indentation follow the settings.
- The status bar (section 5) shows the three and changes them: converting the line separator or the encoding saves the file at once (with its unsaved changes); choosing an indentation only changes what the editor inserts from then on, and is kept in the session.

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

**Pod side, per project**: open files, per file the cursor position, folded ranges and chosen indentation, explorer options, consoles, split layout (tree, sizes, active file per pane). Pushed over the WebSocket on every change (debounced for the cursor), restored on load or reconnection, so the session is the same from another browser or machine.

**Settings** (`~/.web-ide/settings.json`): themes, fonts, shortcuts, custom highlighting rules, language.

- Full snapshot history on each significant change; entries are never modified.
- A `current` pointer to the active entry; a rollback creates a new entry copying the old one, so a rollback can be undone.
- Bounded number of entries (oldest purged); forced snapshot before importing highlighting rules.
- Every browser connected to the same pod shares the settings.

## 10. Settings, projects and detached windows

### Settings

Large modal with navigation on the left: themes (with the accent color and the visual focus mode), fonts, editor (tab size, indentation with spaces, current line, indentation guides, whitespace), keyboard shortcuts, syntax highlighting (add, edit, export rules as JSON per language), language of the interface.

### Home page and projects

- Simple project list. A project is **local** or **SSH**, with an optional title and description (name derived from the path or the host otherwise).
- The project registry lives in the pod. Each project has its own URL, `/project/:id`, with its own pod session.
- Every project is a git repository: creating a project runs `git init` (first branch `main`, an empty first commit when the folder is empty) unless the folder is already in a repository, and adds the optional remote as `origin`. An SSH host not reachable at creation (password) gets it at the first opening (`gitSetup` pending in the registry).
- **Project icon**: a glyph (a subset of Lucide) or 1 to 3 characters on a shape (circle, rounded square, square, hexagon, diamond) filled with a colour or a two-colour gradient in one of 8 directions. Shown on the home page, and as the favicon of every window of the project so that browser tabs tell the projects apart; the worktree of a ticket shows the icon of its project with a dot. The page draws the SVG and saves it in `.ide/icon.svg` with its description in `.ide/icon.json`; a project without one gets one generated (initials, colour and shape from its id).
- Each project has a `.ide` folder: project settings, folder marks, database connections (without secrets), conversations of the assistant and the kanban (both ignored by git).

### Detached windows

Each panel has an id and can be opened alone:

- `/project/:id/editor`
- `/project/:id/tool/:toolId`, any tool of the four zones, the Console with all its tabs included.

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

- Git tool: a branch bar (switch, create, ahead/behind, pull, push, fetch), then two tabs; the shown tab and the heights of the bottom areas are saved in the session.
- **Commit** tab: the changed files as a tree drawn like the explorer (git colors, file icons, chains of single folders joined on one row, `a/b`), under a *Changes* root row. Each row ends with a check box: checked when the file is staged, partial when it is staged with changes left (or, on a folder, when part of its files are); a click stages or unstages the file, the folder or everything. Checking a conflicted file marks it resolved (confirmed when it still holds conflict markers). A click opens the diff against HEAD, a double click the file; arrows move, `Space` toggles the box; context menu: differences, open, stage, unstage, discard, copy the path. At the bottom (height set by a handle): commit message, *Amend* (filled with the last message), *Commit*, *Commit and push* with a menu to push with `--force-with-lease` or `--force` (confirmed).
- **History** tab: the commits of the current branch with their graph, as `git log --graph --pretty='%h -%d %s (%an %ar - %ad)'` (lanes drawn in SVG, merged branches beside), loaded 100 at a time while scrolling; a search on message, author or hash (no graph then). A click shows the commit at the bottom (height set by a handle): actions (copy the hash, new branch at the commit, revert, reset soft / mixed / hard), author, dates, parents, full message, and the changed files as a tree; a file opens its diff against the first parent. The same actions are in the context menu of a commit.
- Pull, push and fetch run in a terminal of the bottom panel (credential prompts stay interactive). A branch without upstream is pushed with `-u origin <branch>`.
- Side-by-side diff tab (working tree ↔ index, index ↔ HEAD, working tree ↔ HEAD, or a commit ↔ its first parent); gutter markers for added, changed and removed lines while typing.
- `git` runs through the same executor as the rest, so it also works on an SSH host.

## 13. AI assistant

Right-panel tool talking to a **llama.cpp** or **Ollama** server (address and optional API key, never sent back to the page).

- Markdown answers with highlighted code and Mermaid diagrams, collapsible reasoning, live token counters.
- Agent loop with tools: files (read, search, edit with confirmation or automatically), language servers, shell commands, IDE (open a file, focus a panel), consoles, kanban, questions to the user (`ask_user`).
- Instructions and skills loaded the same way as Claude Code (`CLAUDE.md`, `AGENTS.md`, `.claude/skills`…), editable system prompt, Build / Plan / Briefing modes (the Briefing mode questions the user and writes kanban tickets), context compaction.
- Conversations stored per project in SQLite; an answer survives a page reload and can be followed from another window.
- Local speech recognition: dictation and audio files are transcribed in the browser by Whisper; audio never leaves the page.

## 14. Docker

Tool of the bottom right zone (beside Problems) driving Docker and Docker Compose. Everything goes through the `docker` command run by the executor, so on an SSH project Docker is the one of the SSH host. Docker missing, its daemon unreachable or not allowed (user outside the `docker` group): the tool shows the error of `docker` and a hint, never `sudo`.

Three tabs (the shown tab is saved in the session), and a detail pane beside the list of the first two:

- **Project**: the services of the Compose file at the project root (`compose.yaml`, `compose.yml`, `docker-compose.yaml`, `docker-compose.yml`, with the override files Compose loads itself). A first *Stack* row, then one row per container (a declared service without container shows as *not created*): state dot, name, image, published ports, CPU and memory. Profiles declared by the file are chosen in a menu (saved in the session). Without Compose file, the tab says so.
- **Host**: every container of the host (`docker ps -a`), grouped by Compose project.
- **Disk**: space used by images, containers, volumes and the build cache (total, active, reclaimable), with the list of images and volumes. Prune buttons, each confirmed: stopped containers, dangling images, unused images, anonymous volumes, all unused volumes (stronger confirmation: data is lost), build cache. An unused image or volume is removed from its context menu.

### Actions

- Stack and each service: *Start* (`up -d`), *Stop*, *Restart* (same container), *Recreate* (`up -d --force-recreate`), *Rebuild* (`up -d --build`), *Pull* (`pull` then `up -d`); the stack also has *Down* (confirmed) and *Down with volumes* (`down -v`, the project name typed to confirm). Start, stop and restart run in the pod (the row shows a spinner, an error a toast); the others run as tasks in the Console, their output visible.
- Containers of the Host tab: start, stop, restart, remove (confirmed, `rm -f`), logs, shell.
- *Shell*: a Console terminal running `docker exec -it` with `bash`, or `sh` when bash is missing; *Shell as root* adds `-u root`.
- The lists refresh every few seconds while the tool is shown.

### Detail pane

- **Infos**: image, state and health, command, creation date, ports (`host:port → container port/protocol`), mounts (type, source → destination, read-only), networks (name, IP address, aliases), and the live values: CPU, memory (usage / limit), network and block I/O, processes. Instant values from `docker stats --no-stream`, refreshed every few seconds while shown.
- **Logs**: the last 500 lines, then followed live (`docker logs -f`, `docker compose logs -f` for the Stack row, with a service filter). Text filter, timestamps shown or hidden, ANSI colors kept, scrolling up pauses the following, at most 10,000 lines kept.

## 15. Decisions

- **Q1**: the pod pushes the whole file (`fs.changed`, decoded to UTF-8 with LF line ends, with its encoding and line separator) on each change, after 150 ms of stability (AI tools often write in several steps).
- **Q2**: "accept both" concatenates local then remote.
- **Q3**: credentials are never written in `.ide`. Remembered passwords go to `~/.web-ide/secrets.json` (mode 0600, same model as `~/.pgpass`, not encrypted); otherwise they stay in the pod's memory for the session.
- **Q4**: Redis keys get a dedicated node (type and TTL) under each `dbN`.
- **Q5**: systemd user service (`make service`) or one-off run (`make run`). No tray icon.
- **Q6**: the pod listens on `127.0.0.1` and refuses other machines. `-allow-remote` allows remote access, protected by the token only.
