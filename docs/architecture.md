# Architecture

How Web IDE is built, where things live, and the traps already met. Read [spec.md](spec.md) for the intended behaviour.

## Overview

```
browser (SolidJS app)  ──WebSocket JSON-RPC──▶  pod (Go binary)
                                                 ├─ local disk, or SSH/SFTP host
                                                 ├─ terminals (PTY), commands
                                                 ├─ language servers (LSP)
                                                 ├─ databases (SQLite, PostgreSQL, Redis)
                                                 ├─ git, docker
                                                 └─ model servers (llama.cpp, Ollama)
```

The front end is built by Vite and embedded in the pod binary (`pod/webdist`), so the page and the WebSocket share one origin: no mixed content, and pairing is a cookie.

## Repository layout

| Path | Content |
|---|---|
| `pod/` | The pod: `main.go` (flags, wiring), `internal/…` packages, `webdist/` (embedded front end) |
| `web/` | The front end (SolidJS + TypeScript, Vite) |
| `e2e/` | Browser tests (Playwright core driving headless Chromium); `e2e/shots/` makes the pictures of the documentation |
| `docs/` | User guide, specification, architecture, kanban and doodle designs, pictures (`docs/images`) |

## Data on disk

- `~/.web-ide/` (pod data, `-data` flag): `config.json` (address, workspace), `token`, `projects.json`, `settings.json` (with history), `clipboard.json` (clipboard history), `sessions/<project>.json` (layout, tabs, tool zones, explorer options, recent files, and per file the cursor, folds and chosen indentation), `secrets.json` (0600), `known_hosts` (trust on first use, in addition to `~/.ssh/known_hosts`), `sql-history/`, `llm.json` (model servers), `system-prompt.md` / `plan-prompt.md` / `briefing-prompt.md`, `models/hf/` (speech models), `chats/` and `kanban/` (bases of SSH projects), `icons/<project>.svg` (copy of the project icons for the home page: an SSH project is not reached to list it).
- `<project>/.ide/`: `connections.json` (database connections, no secret), `tunnels.json` (tunnels of an SSH project), `folders.json` (folder marks: source, tests, excluded), `project.json` (`lsp`: command per language; `tests`: pattern per extension, e.g. `{".php": "{name}Spec.php"}`), `chats.db` (conversations), `kanban.db` (tickets), `worktrees/` (one git worktree per ticket in development), `icon.svg` and `icon.json` (project icon, drawn by the page: `ui/projectIcon.ts`, `ui/IconEditor.tsx`). `.ide/.gitignore` keeps the bases and the worktrees out of git.

## Protocol

JSON over one WebSocket per window. Request `{id, method, params}` → response `{id, result | error}`; events pushed as `{event, data}` (`fs.changed`, `buffer.synced`, `session.changed`, `console.output`, `lsp.diagnostics`, `git.changed`, `llm.delta`, `kanban.changed`…). `id: 0` is a notification without answer; `$/cancel` cancels a request. Some methods run in order per group (document changes, terminal input), and a language server request waits for the document changes sent before it (`barrier` in `server.go`).

`POST /mcp` is the MCP endpoint for Claude Code (`server/mcp*.go`, JSON-RPC over HTTP without streaming, the token as a bearer): tools and prompts on the kanban, see [kanban.md](kanban.md#claude-code).

Errors carry a code (`error`, `canceled`, `auth_required`, `db_password`) and a message translated into the language of the window.

## Pod (`pod/internal`)

| Package | Role |
|---|---|
| `server` | HTTP, pairing, WebSocket RPC; handlers per domain (`handlers*.go`) |
| `runtime` | One per open project: file system, watches, revisions, buffers shared between windows, consoles, LSP, databases, git, folder marks; text files decoded to UTF-8 with LF and encoded back in their format (`textfmt.go`) |
| `fsx` | Local and SFTP file systems, watching (fsnotify or polling) |
| `sshx` | SSH agent, keys, passwords, host keys (TOFU), connection pool |
| `execx` | Local or SSH processes, PTYs |
| `console` | Terminals and commands with scrollback |
| `lsp` | Language servers per project and language |
| `db` | SQLite (modernc), PostgreSQL (pgx), Redis (go-redis), SSH tunnels |
| `git` | Git panel operations |
| `tunnels` | Local ports forwarded to the SSH host of a project (open while a window of the pod is connected, closed `TunnelIdle` after the last one) |
| `docker` | Docker tool: status, Compose stack, containers, inspect, stats, log streams (the `docker` command, JSON formats only) |
| `search` | Project-wide search (RE2) and file list |
| `llm` | Model servers, chat completions as jobs that survive the page, conversations (SQLite), instructions and skills |
| `kanban` | Tickets (SQLite), workflow rules, ticket git operations (worktrees, diff, merge, rebase) |
| `hfcache` | Hugging Face files downloaded once and served offline (speech models) |
| `i18n` | Translation of the messages sent to the page |
| `clipboard` | History of the texts copied in the IDE, shared by every window |
| `projects`, `sessions`, `settings`, `store`, `config` | Registry, sessions, settings with history, data folder, configuration |
| `sshtest` | SSH server for tests (in memory; `sshtestd` runs it for the browser tests) |

## Front end (`web/src`)

| Folder | Role |
|---|---|
| `editor/` | `Doc` (buffer, revisions, undo, file format, indentation), `EditorView` (block rendering, Custom Highlight API, indentation guides, whitespace overlay, multiple carets, folding), `carets.ts` (words, occurrences), `folding.ts` (fold ranges), `indent.ts` (indentation detection), tokenizer and grammars, three-way merge, line diff, sub-word moves, find bar |
| `state/` | Open project and session (tabs, split tree, tool zones), settings, git state, folder marks |
| `keys/` | Binding table and QWERTY / AZERTY presets |
| `lsp/` | Client, completion, edits, rename and formatting |
| `popups/` | Search Everywhere, Recent Files and the switcher, paste from history (the history itself: `ui/clipboard.ts`) |
| `ui/` | Editor area, status bar, diff view, overlays (modal, prompt, pick list, context menu), toasts, icons, empty states, keyboard navigation of the toolbars and tab bars (`roving.ts`) |
| `panels/` | Explorer (with file type icons, `fileIcons.tsx`), global search, Git tool (`panels/git/`: tabs, tree of changes, graph lanes `graph.ts`, commit detail) |
| `tools/`, `db/`, `console/`, `conflict/`, `settings/`, `pages/` | Right-panel tools, database explorer, Console and Problems tools, conflict dialog, settings modal, pages |
| `llm/` | AI assistant: state, agent loop, tools, prompt, Markdown, attachments, speech recognition, doodles (`llm/doodle/`: document model, SVG rendering, export, modal) |
| `kanban/` | Board, ticket view, workflow actions, linked conversations |
| `docker/` | Docker tool: lists and polling (`state.ts`), detail pane, logs with ANSI colors (`ansi.ts`), disk usage, tunnels (and their list on the home page) |
| `i18n/` | `t()` and the catalogs (English source strings, French translation) |

### AI assistant

- Module-level state (`llm/state.ts`) survives panel switches; conversations are saved in the pod one save at a time.
- `llm/agent.ts`: tool loop without step limit, compaction (automatic past a threshold, manual, or asked by the model), Build / Plan / Briefing modes, `ask_user` (the turn stops until the answers come), message queue, resume after reload (`llm.attach`), one window runs a conversation (`llm.claim`) while others follow it.
- `llm/prompt.ts`: editable system prompt template, instruction files and skills (loaded like Claude Code), linked ticket and role instructions.
- `llm/tools.ts`, `llm/kanbanTools.ts`: tools the model can call. File changes are confirmed with a diff unless "apply without asking" is on; shell commands run without confirmation (Plan mode runs reading commands and the build, test and lint commands of the project freely, and asks for the others: `llm/commands.ts`).
- Speech recognition runs in a Web Worker (transformers.js, WebGPU or WebAssembly); model files come through the pod cache.

### Kanban

See [kanban.md](kanban.md). A ticket worktree is registered as a hidden child project `<parent>-t<n>` (`projects.PutChild`); its kanban and conversations are those of the parent. Another worktree opened from the menu bar is a child project `<parent>-w<hash of its path>` (`projects.PutWorktree`, `server/handlers_worktrees.go`). Project windows are named `project-<id>` so that opening a project again brings back its window. A development session started from the main window saves the conversation with `running: {}` and opens the worktree window with `?assistant=1`, which resumes it.

## Languages

- The interface is translated with `t()` (`web/src/i18n`): the English text is the key, `fr.json` gives the French one, `{name}` marks a parameter and `tn()` picks the plural. A key may carry a context (`'menu|Edit'`) when one English word has two translations. Data tables keep English labels and are translated where they are shown. The language is a setting (`language`: auto, en, fr), auto follows the browser.
- Each window tells the pod its language (`client.lang`); errors created with `i18n.New` / `i18n.Errorf` are translated when they are sent (`pod/internal/i18n/fr.json`). Lines of the history of a ticket are stored as `{"key", "params"}` and translated by the page.
- Text read by the model (system prompt, tool descriptions and results) is always English; the model answers in the language of the user.
- `npm run check` and the Go tests of `internal/i18n` fail on a missing French translation. The e2e suites run in English (`E2E_LOCALE` to change it); the `i18n` suite checks French and the switch.

## Building and testing

```
make build          # front end (Vite) then pod binary bin/web-ide-pod (front end embedded)
make dev            # pod with -allow-remote on 0.0.0.0:4434 and data in ~/.web-ide-dev + Vite on 0.0.0.0:5173 (hot reload)
make shots          # pictures of docs/images, replaying e2e/shots/recording.json.gz (see docs/guide.md)
make service        # this build as a systemd user service started at boot (scripts/install.sh)
make test           # go vet + go test + tsc
make e2e            # browser tests, all suites (a few minutes)
./e2e/run.sh git    # one suite: editing editor features restore+ keyboard git projects explorer lsp llm agent chat plan doodle kanban kanbanai kanbangit docker tunnels i18n speech perf
```

- Each e2e suite gets a fresh pod with temporary data and a workspace copied from `e2e/fixtures`; a suite ending with `+` reuses the previous pod. The assistant suites use a scripted fake OpenAI-compatible server. Chromium comes from the Playwright cache or `CHROME=…`; the `speech` suite downloads `whisper-tiny` once (kept in `~/.cache/web-ide-e2e/models`); the `lsp` suite needs `gopls`; the `docker` suite needs Docker with Compose and the `postgres:17-alpine` image (skipped otherwise); the `tunnels` suite builds `sshtestd` (Go) and opens an SSH project on it.
- Optional database driver tests against real servers: `WEBIDE_TEST_PG=host:port:user:pass WEBIDE_TEST_REDIS=host:port:pass go test ./internal/db/`.
- Code navigation needs the language servers in the pod's `PATH` (`gopls` also needs `go`). The service keeps the `PATH` of the shell that installed it.
- Versions: `make build` stamps the binary with `git describe` (`web-ide-pod -version`, shown on the home page). Pushing a tag `v*` runs `.github/workflows/release.yml`, which builds the Linux and macOS archives and publishes the release with the notes of that version in `CHANGELOG.md`; `scripts/install.sh v1.2.3` installs one as the service.

## Pitfalls

- **Solid empties a container** whose only child is dynamic: a node mounted by hand (the editor view) needs its own element (`.editor-mount`).
- **View callbacks run under `untrack`** (`useEditorView`), otherwise an effect calling `setSelection` subscribes to what the callback reads.
- **Editor blocks use `width: max-content`**, otherwise lengthening the longest line re-lays out the whole file.
- **Editor rows are not lines** once something is folded: positions go through `lineTop` / `lineAtY` (`rowOf`, `lineOfRow`), never `line * lineHeight`. Folding splits a block (`splitText` then a move to a new element), which loses the DOM selection: it is set again afterwards.
- **The editor overlays share the global CSS**: a class such as `.tab` (editor tabs) also styles an element of the whitespace overlay; overlay elements use short editor-specific names.
- **The browser draws no caret at the end of a non-empty selection**: with several carets the editor hides the native caret and draws them all.
- **`caretPositionFromPoint` at the left edge** of the text can land in the gutter: `offsetAt` keeps the point inside the content.
- **No `push(...bigArray)`** (stack overflow on large pastes): copy in a loop.
- **A Solid store merges objects**: `setChat('running', {})` clears nothing; write `{ stream: undefined }`.
- **DOMPurify** drops attributes containing `-->` (Mermaid sources are stored URI-encoded) and HTML inside `foreignObject` (Mermaid uses `htmlLabels: false`).
- **Git**: git speaks the language of the user, so its messages are never parsed (`git.Show` asks `cat-file -e` first); ticket commits start with `#<n>`, so `rebase --continue` and `commit --no-edit` run with `core.commentChar=auto`; diff prefixes are forced (`--src-prefix=a/ --dst-prefix=b/`) because user settings such as `diff.mnemonicPrefix` change them; after a merge the branch has nothing left against its base, so the change is frozen in the ticket at merge time.
- **Shell**: `pkill -f <pattern>` also kills the command running it; use `pkill -x web-ide-pod`. Start a test pod with `setsid` / `< /dev/null`, otherwise a pipe stays open.
- **Copy through `copyText`** (`ui/clipboard.ts`), not `navigator.clipboard.writeText`: the text then goes to the clipboard history. A popup gives the focus back with `keepFocus` (`state/focus.ts`): focusing the editor again puts its caret at the start otherwise.
- **E2E tests** must wait actively (`waitForFunction`): language servers start cold. `<option>` elements are never "visible" for Playwright (`state: 'attached'`).

## Known limitations

- Code navigation was tested with gopls only; intelephense, phpactor, pyright and typescript-language-server were never tried.
- Tested in Chromium only.
- SSH was tested with the in-memory test server, not against a real host; ticket worktrees on SSH projects are untested. The Docker tool of an SSH project is untested (only its tunnels, through the test server).
- Global search uses RE2 syntax (no lookbehind).
- SQLite on an SSH project goes through the `sqlite3` command on the host (autocommit only).
- The microphone needs a secure context (localhost or HTTPS): with `-allow-remote` over plain HTTP, dictation is disabled (audio files are still transcribed).
