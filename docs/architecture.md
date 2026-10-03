# Architecture

How Web IDE is built, where things live, and the traps already met. Read [spec.md](spec.md) for the intended behaviour.

## Overview

```
browser (SolidJS app)  ──WebSocket JSON-RPC──▶  pod (Go binary)
                                                 ├─ local disk, or SSH/SFTP host
                                                 ├─ terminals (PTY), commands
                                                 ├─ language servers (LSP)
                                                 ├─ databases (SQLite, PostgreSQL, Redis)
                                                 ├─ git
                                                 └─ model servers (llama.cpp, Ollama)
```

The front end is built by Vite and embedded in the pod binary (`pod/webdist`), so the page and the WebSocket share one origin: no mixed content, and pairing is a cookie.

## Repository layout

| Path | Content |
|---|---|
| `pod/` | The pod: `main.go` (flags, wiring), `internal/…` packages, `webdist/` (embedded front end) |
| `web/` | The front end (SolidJS + TypeScript, Vite) |
| `e2e/` | Browser tests (Playwright core driving headless Chromium) |
| `docs/` | Specification, architecture, kanban design |

## Data on disk

- `~/.web-ide/` (pod data, `-data` flag): `config.json` (address, workspace), `token`, `projects.json`, `settings.json` (with history), `sessions/<project>.json`, `secrets.json` (0600), `known_hosts` (trust on first use, in addition to `~/.ssh/known_hosts`), `sql-history/`, `llm.json` (model servers), `system-prompt.md` / `plan-prompt.md`, `models/hf/` (speech models), `chats/` and `kanban/` (bases of SSH projects).
- `<project>/.ide/`: `connections.json` (database connections, no secret), `project.json` (`lsp`: command per language; `tests`: pattern per extension, e.g. `{".php": "{name}Spec.php"}`), `chats.db` (conversations), `kanban.db` (tickets), `worktrees/` (one git worktree per ticket in development). `.ide/.gitignore` keeps the bases and the worktrees out of git.

## Protocol

JSON over one WebSocket per window. Request `{id, method, params}` → response `{id, result | error}`; events pushed as `{event, data}` (`fs.changed`, `buffer.synced`, `session.changed`, `console.output`, `lsp.diagnostics`, `git.changed`, `llm.delta`, `kanban.changed`…). `id: 0` is a notification without answer; `$/cancel` cancels a request. Some methods run in order per group (document changes, terminal input), and a language server request waits for the document changes sent before it (`barrier` in `server.go`).

Errors carry a code (`error`, `canceled`, `auth_required`, `db_password`) and a message translated into the language of the window.

## Pod (`pod/internal`)

| Package | Role |
|---|---|
| `server` | HTTP, pairing, WebSocket RPC; handlers per domain (`handlers*.go`) |
| `runtime` | One per open project: file system, watches, revisions, buffers shared between windows, consoles, LSP, databases, git |
| `fsx` | Local and SFTP file systems, watching (fsnotify or polling) |
| `sshx` | SSH agent, keys, passwords, host keys (TOFU), connection pool |
| `execx` | Local or SSH processes, PTYs |
| `console` | Terminals and commands with scrollback |
| `lsp` | Language servers per project and language |
| `db` | SQLite (modernc), PostgreSQL (pgx), Redis (go-redis), SSH tunnels |
| `git` | Git panel operations |
| `search` | Project-wide search (RE2) and file list |
| `llm` | Model servers, chat completions as jobs that survive the page, conversations (SQLite), instructions and skills |
| `kanban` | Tickets (SQLite), workflow rules, ticket git operations (worktrees, diff, merge, rebase) |
| `hfcache` | Hugging Face files downloaded once and served offline (speech models) |
| `i18n` | Translation of the messages sent to the page |
| `projects`, `sessions`, `settings`, `store`, `config` | Registry, sessions, settings with history, data folder, configuration |
| `sshtest` | In-memory SSH server for tests |

## Front end (`web/src`)

| Folder | Role |
|---|---|
| `editor/` | `Doc` (buffer, revisions, undo), `EditorView` (block rendering, Custom Highlight API), tokenizer and grammars, three-way merge, line diff, sub-word moves, find bar |
| `state/` | Open project and session (tabs, split tree, panels), settings, git state |
| `keys/` | Binding table and QWERTY / AZERTY presets |
| `lsp/` | Client, completion, edits, rename and formatting |
| `ui/` | Editor area, diff view, overlays (modal, prompt, pick list, context menu), toasts, icons |
| `panels/` | Explorer, global search, Git, connections |
| `tools/`, `db/`, `console/`, `conflict/`, `settings/`, `pages/` | Right-panel tools, database explorer, terminals, conflict dialog, settings modal, pages |
| `llm/` | AI assistant: state, agent loop, tools, prompt, Markdown, attachments, speech recognition |
| `kanban/` | Board, ticket view, workflow actions, linked conversations |
| `i18n/` | `t()` and the catalogs (English source strings, French translation) |

### AI assistant

- Module-level state (`llm/state.ts`) survives panel switches; conversations are saved in the pod one save at a time.
- `llm/agent.ts`: tool loop (30 steps max), compaction (automatic past a threshold, manual, or asked by the model), Plan / Build modes, `ask_user` (the turn stops until the answers come), message queue, resume after reload (`llm.attach`), one window runs a conversation (`llm.claim`) while others follow it.
- `llm/prompt.ts`: editable system prompt template, instruction files and skills (loaded like Claude Code), linked ticket and role instructions.
- `llm/tools.ts`, `llm/kanbanTools.ts`: tools the model can call. File changes are confirmed with a diff unless "apply without asking" is on; shell commands run without confirmation (Plan mode asks for commands that may change something).
- Speech recognition runs in a Web Worker (transformers.js, WebGPU or WebAssembly); model files come through the pod cache.

### Kanban

See [kanban.md](kanban.md). A ticket worktree is registered as a hidden child project `<parent>-t<n>` (`projects.PutChild`); its kanban and conversations are those of the parent. A development session started from the main window saves the conversation with `running: {}` and opens the worktree window with `?assistant=1`, which resumes it.

## Building and testing

```
make build          # front end (Vite) then pod binary bin/web-ide-pod (front end embedded)
make dev            # pod with -allow-remote on 0.0.0.0:4433 + Vite on 0.0.0.0:5173 (hot reload)
make test           # go vet + go test + tsc
make e2e            # browser tests, all suites (a few minutes)
./e2e/run.sh git    # one suite: editing features restore+ git lsp llm agent chat plan kanban kanbanai kanbangit speech perf
```

- Each e2e suite gets a fresh pod with temporary data and a workspace copied from `e2e/fixtures`; a suite ending with `+` reuses the previous pod. The assistant suites use a scripted fake OpenAI-compatible server. Chromium comes from the Playwright cache or `CHROME=…`; the `speech` suite downloads `whisper-tiny` once (kept in `~/.cache/web-ide-e2e/models`); the `lsp` suite needs `gopls`.
- Optional database driver tests against real servers: `WEBIDE_TEST_PG=host:port:user:pass WEBIDE_TEST_REDIS=host:port:pass go test ./internal/db/`.
- Code navigation needs the language servers in the pod's `PATH` (`gopls` also needs `go`).

## Pitfalls

- **Solid empties a container** whose only child is dynamic: a node mounted by hand (the editor view) needs its own element (`.editor-mount`).
- **View callbacks run under `untrack`** (`useEditorView`), otherwise an effect calling `setSelection` subscribes to what the callback reads.
- **Editor blocks use `width: max-content`**, otherwise lengthening the longest line re-lays out the whole file.
- **No `push(...bigArray)`** (stack overflow on large pastes): copy in a loop.
- **A Solid store merges objects**: `setChat('running', {})` clears nothing; write `{ stream: undefined }`.
- **DOMPurify** drops attributes containing `-->` (Mermaid sources are stored URI-encoded) and HTML inside `foreignObject` (Mermaid uses `htmlLabels: false`).
- **Git**: ticket commits start with `#<n>`, so `rebase --continue` and `commit --no-edit` run with `core.commentChar=auto`; diff prefixes are forced (`--src-prefix=a/ --dst-prefix=b/`) because user settings such as `diff.mnemonicPrefix` change them; after a merge the branch has nothing left against its base, so the change is frozen in the ticket at merge time.
- **Shell**: `pkill -f <pattern>` also kills the command running it; use `pkill -x web-ide-pod`. Start a test pod with `setsid` / `< /dev/null`, otherwise a pipe stays open.
- **E2E tests** must wait actively (`waitForFunction`): language servers start cold. `<option>` elements are never "visible" for Playwright (`state: 'attached'`).

## Known limitations

- Code navigation was tested with gopls only; intelephense, phpactor, pyright and typescript-language-server were never tried.
- Tested in Chromium only.
- SSH was tested with the in-memory test server, not against a real host; ticket worktrees on SSH projects are untested.
- Global search uses RE2 syntax (no lookbehind).
- SQLite on an SSH project goes through the `sqlite3` command on the host (autocommit only).
- The microphone needs a secure context (localhost or HTTPS): with `-allow-remote` over plain HTTP, dictation is disabled (audio files are still transcribed).
