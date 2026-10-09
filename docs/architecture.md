# Architecture

How Web IDE is built, where things live, and the traps already met. Read [spec.md](spec.md) for the intended behaviour.

## Overview

```
browser (SolidJS app)  ──WebSocket JSON-RPC──▶  pod (Go binary)
                                                 ├─ local disk, or SSH/SFTP host
                                                 ├─ terminals (PTY), in the keeper ─unix socket─▶ web-ide-keeper
                                                 ├─ commands
                                                 ├─ language servers (LSP)
                                                 ├─ databases (SQLite, PostgreSQL, Redis)
                                                 ├─ git, docker
                                                 └─ model servers (llama.cpp, Ollama)
```

The front end is built by Vite and embedded in the pod binary (`pod/webdist`), so the page and the WebSocket share one origin: no mixed content, and pairing is a cookie.

The local terminals run in a second service, the **keeper** (`web-ide-pod keeper`, installed as `~/.local/lib/web-ide/web-ide-keeper` and the unit `web-ide-keeper.service`): it spawns them, holds their PTY and their output numbered by offset. The pod talks to it over `<data>/keeper.sock` and can restart freely: when it opens a project again, it adopts the terminals the keeper still runs for it (their console id, title and kind travel as metadata of the process) and the page attaches again from the offset it has. Without a keeper (tests, `-keeper off`, no socket), the pod runs the terminals itself and they end with it. Language servers stay in the pod.

For an SSH project the keeper opens its own connection (`keeper/ssh.go`): the pod dials first and checks the host key (first use, its questions), then gives the keeper that key, pinned, and the secrets of the connection (kept in memory). The terminals and the commands of the agent of the project run on it (target `ssh:<key>`); the pod builds their remote command.

The keeper updates itself without losing its processes (`keeper/upgrade.go`): `web-ide-pod keeper -upgrade` (or `SIGHUP`) has it re-execute its binary in the same process. Its processes stay its children; their PTY masters and pipes and the listening socket cross the exec as inherited descriptors, the output rings, the metadata and the ended HTTP requests go through `keeper-state.json`. What cannot cross waits: the HTTP requests in flight and the SSH commands are waited for (10 min by default, `-force` cancels them), the SSH terminals are closed. A failed exec leaves the keeper as it was. `install.sh` stops the pod, updates the keeper this way when its protocol changed, then starts the pod.

The keeper also relays HTTP requests (`keeper/http.go`): the completions of the agent go through it (`llm.Relay`), so the answer being written is kept outside the pod. The conversations running are listed in `<data>/running.json`; a pod that stops saves nothing more, and the next one takes them back before it listens (`server/agent_resume.go`): the completion awaited is read again from the relay (the parser replays it from the start, the model is not asked again), the command of a tool call that ran in the keeper (`bash` as a piped process, `run_command` in its console) is followed again until its deadline, the other tool calls without result run again (an approval is asked again). A run whose stream the keeper does not have is closed as interrupted, as without keeper.

## Repository layout

| Path | Content |
|---|---|
| `pod/` | The pod: `main.go` (flags, wiring), `internal/…` packages, `webdist/` (embedded front end) |
| `web/` | The front end (SolidJS + TypeScript, Vite) |
| `e2e/` | Browser tests (Playwright core driving headless Chromium); `e2e/shots/` makes the pictures of the documentation |
| `claude-mod/` | Claude Code mod: ticket and changed files above the prompt, opened in the IDE (`/open`) |
| `docs/` | User guide, specification, architecture, kanban, doodle and relay designs, pictures (`docs/images`) |

## Data on disk

- `~/.web-ide/` (pod data, `-data` flag): `keeper.sock` (socket of the keeper, 0600), `keeper-state.json` (only during an update of the keeper), `running.json` (conversations running, taken back by the next pod), `config.json` (address, workspace), `token`, `projects.json`, `settings.json` (with history), `clipboard.json` (clipboard history), `sessions/<project>.json` (layout, tabs, tool zones, explorer options, recent files, and per file the cursor, folds and chosen indentation), `secrets.json` (0600), `known_hosts` (trust on first use, in addition to `~/.ssh/known_hosts`), `sql-history/`, `llm.json` (model servers), `system-prompt.md` / `plan-prompt.md` / `briefing-prompt.md`, `models/hf/` (speech models), `chats/` and `kanban/` (bases of SSH projects), `icons/<project>.svg` (copy of the project icons for the home page: an SSH project is not reached to list it).
- `<project>/.ide/`: `connections.json` (database connections, no secret), `tunnels.json` (tunnels of an SSH project), `folders.json` (folder marks: source, tests, excluded), `project.json` (`lsp`: command per language; `tests`: pattern per extension, e.g. `{".php": "{name}Spec.php"}`), `chats.db` (conversations), `kanban.db` (tickets), `worktrees/` (one git worktree per ticket in development), `icon.svg` and `icon.json` (project icon, drawn by the page: `ui/projectIcon.ts`, `ui/IconEditor.tsx`). `.ide/.gitignore` keeps the bases and the worktrees out of git.

## Protocol

JSON over one WebSocket per window. Request `{id, method, params}` → response `{id, result | error}`; events pushed as `{event, data}` (`fs.changed`, `buffer.synced`, `session.changed`, `console.output`, `lsp.diagnostics`, `git.changed`, `llm.delta`, `agent.update`, `kanban.changed`…). `id: 0` is a notification without answer; `$/cancel` cancels a request. Some methods run in order per group (document changes, terminal input), and a language server request waits for the document changes sent before it (`barrier` in `server.go`).

`POST /mcp` is the MCP endpoint for Claude Code (`server/mcp*.go`, JSON-RPC over HTTP without streaming, the token as a bearer): tools and prompts on the kanban, see [kanban.md](kanban.md#claude-code). `/open?path=&line=` opens a file in the windows of its project (`server/open.go`).

Errors carry a code (`error`, `canceled`, `auth_required`, `db_password`) and a message translated into the language of the window.

## Pod (`pod/internal`)

| Package | Role |
|---|---|
| `server` | HTTP, pairing, WebSocket RPC; handlers per domain (`handlers*.go`) |
| `runtime` | One per open project: file system, watches, revisions, buffers shared between windows, consoles, LSP, databases, git, folder marks; text files decoded to UTF-8 with LF and encoded back in their format (`textfmt.go`) |
| `fsx` | Local and SFTP file systems, watching (fsnotify or polling) |
| `sshx` | SSH agent, keys, passwords, host keys (TOFU), connection pool |
| `execx` | Local or SSH processes, PTYs |
| `console` | Terminals and commands with scrollback and output offsets; run by the keeper when there is one, adopted again after a restart of the pod |
| `keeper` | The keeper: protocol (frames of a length, a JSON header and raw bytes), server (processes known by pid and descriptors, output rings, attachments from an offset, GC of ended processes; HTTP relay of the completions; SSH connections; update by re-exec) and the client of the pod (reconnection, attachments resumed from their offset) |
| `lsp` | Language servers per project and language |
| `db` | SQLite (modernc), PostgreSQL (pgx), Redis (go-redis), SSH tunnels |
| `git` | Git panel operations |
| `preview` | App previews: a reverse proxy per preview (cookie of the IDE, or the token of a public one), exposed by `tailscale serve` / `funnel` (`Tailscale` interface, faked in tests), stopped with the command of the app |
| `tunnels` | Local ports forwarded to the SSH host of a project (open while a window of the pod is connected, closed `TunnelIdle` after the last one) |
| `docker` | Docker tool: status, Compose stack, containers, inspect, stats, log streams (the `docker` command, JSON formats only) |
| `search` | Project-wide search (RE2) and file list |
| `llm` | Model servers, chat completions as jobs that survive the page, conversations (SQLite), instructions and skills |
| `agent` | What the agent of the assistant needs apart from the server: the stored conversation, the prompts and tool definitions, the Plan mode command rules, the line diff of a change, the questions of `ask_user`, the sub-agents (`subagents.go`); the loop itself is in `server/agent*.go` |
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
| `state/` | Open project and session (tabs, split tree, tool zones), settings, git state, folder marks, the phone layout (`mobile.ts`: the view shown, the keyboard, no zoom) |
| `keys/` | Binding table and QWERTY / AZERTY presets |
| `lsp/` | Client, completion, edits, rename and formatting |
| `popups/` | Search Everywhere, Recent Files and the switcher, paste from history (the history itself: `ui/clipboard.ts`) |
| `ui/` | Editor area, status bar, diff view, overlays (modal, prompt, pick list, context menu), toasts, icons, empty states, keyboard navigation of the toolbars and tab bars (`roving.ts`) |
| `panels/` | Explorer (with file type icons, `fileIcons.tsx`), global search, Git tool (`panels/git/`: tabs, tree of changes, graph lanes `graph.ts`, commit detail) |
| `tools/`, `db/`, `console/`, `conflict/`, `settings/`, `pages/` | Right-panel tools, database explorer, Console and Problems tools, conflict dialog, settings modal, pages |
| `llm/` | AI assistant: state, agent loop, tools, prompt, Markdown, attachments, speech recognition, sub-agents (`SubAgents.tsx`), doodles (`llm/doodle/`: document model, SVG rendering, export, modal), the board of a conversation (`llm/board/`), app previews (`previews.ts`, `PreviewCard.tsx`) |
| `kanban/` | Board, ticket view, workflow actions, linked conversations |
| `docker/` | Docker tool: lists and polling (`state.ts`), detail pane, logs with ANSI colors (`ansi.ts`), disk usage, tunnels (and their list on the home page), app previews |
| `i18n/` | `t()` and the catalogs (English source strings, French translation) |

### AI assistant

- The agent runs in the pod (`server/agent*.go`, `internal/agent`): the turn loop without step limit, the tools on the runtime of the project (a worktree for a development), compaction (automatic past a threshold, manual, or asked by the model), Orchestrator / Build / Plan / Briefing modes (the Orchestrator tools in `server/agent_orchestrator.go`, `agent/orchestrator.go`, `kanban/day.go`), `ask_user` and plans (the turn stops until the user answers), the message queue, resume from the last completed step (`agent.resume`) or retry from the user message (`agent.retry`), both with the model chosen when it differs from the one of the conversation (a third button, "Resume with <model>"). A conversation goes on with no window open; several run at once, each model server running `parallel` of them (1 by default) while the others wait in a queue. Sub-agents (`server/agent_sub.go`, `agent/subagents.go`) are conversations with a `parent`: their events reach the other side through `deliver` (queued while it runs, kept while it waits for the user, else added and the conversation started), always outside the locks of the runs; a run that ends with messages queued meanwhile starts again. An Orchestrator adopts a conversation of its own (`agent_adopt`): `agent.Adopted` keeps its tools (plus `agent_note` / `agent_report`, no nudge to report) and its prompt says it is followed; events sent to it run it where it works (`runProject`).
- The pod is the only writer of a running conversation: every change is saved and sent as `agent.update` (state, fields, messages from an index) to the windows of the project and of its worktrees; the answer being written is followed with `llm.attach`. Changes and commands that need the user wait in the pod (`approval`, answered by `agent.approve` from any window), with an `agent.attention` event. `open_file` and `focus` run in a window of the project (`agent.ui`, answered by `agent.ui.result`), the one showing the conversation first.
- `llm/agent.ts` is the client: it shows the conversation from these events and sends what the user does (`agent.send`, `agent.answer`, `agent.plan`, `agent.stop`…), with the options of the page (apply without asking, thinking, compaction, Plan model, active file, Docker profiles). Module-level state (`llm/state.ts`) survives panel switches.
- `internal/agent/texts.go`: the default templates of the modes and the instructions of the roles of a ticket; `llm/prompt.ts` shows them in the settings (the pod builds the prompt: `agent.prompt`). File changes are confirmed with a diff unless "apply without asking" is on; shell commands run without confirmation (Plan mode runs reading commands and the build, test and lint commands of the project freely, and asks for the others: `internal/agent/commands.go`).
- Speech recognition runs in a Web Worker (transformers.js, WebGPU or WebAssembly); model files come through the pod cache.

### Kanban

See [kanban.md](kanban.md). A ticket worktree is registered as a hidden child project `<parent>-t<n>` (`projects.PutChild`); its kanban and conversations are those of the parent. Another worktree opened from the menu bar is a child project `<parent>-w<hash of its path>` (`projects.PutWorktree`, `server/handlers_worktrees.go`). Project windows are named `project-<id>` so that opening a project again brings back its window.

A window opens one project (`project.open`: its session) and attaches the other worktrees of the repository it shows (`project.attach`, `state/project.ts`): the runtime of each stays open while the window lives. A request names the worktree it runs in (`project` beside `method`; the server hands the handler a `Client` view with that project), and the events of an attached runtime reach the window tagged with their `project`. The page picks the worktree of a request in one place (`setScope`): the worktree of the file for `fs.*`, `git.*`, `lsp.*`, `buffer.*` and `folders.*` with a path (the deepest attached root holding it), none for the session, the database connections and the worktree list, else the worktree shown; consoles remember theirs (`requestIn`). `project()` and `root()` are those of the worktree shown, `home()` the project of the window. A ticket conversation records the project it works in (`ticket.project`) and keeps it (`runProject`).

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
./e2e/run.sh git    # one suite: editing editor features restore+ keyboard git projects explorer lsp llm agent chat plan doodle kanban kanbanai kanbangit docker tunnels preview subagents orchestrator mobile i18n speech perf keeper
```

- Each e2e suite gets a fresh pod with temporary data and a workspace copied from `e2e/fixtures`; a suite ending with `+` reuses the previous pod. The suites run without keeper, but `keeper`: `run.sh` starts one next to its pod and gives it the script restarting the pod (`E2E_RESTART_POD`). The assistant suites use a scripted fake OpenAI-compatible server. Chromium comes from the Playwright cache or `CHROME=…`; the `speech` suite downloads `whisper-tiny` once (kept in `~/.cache/web-ide-e2e/models`); the `lsp` suite needs `gopls`; the `docker` suite needs Docker with Compose and the `postgres:17-alpine` image (skipped otherwise); the `tunnels` suite builds `sshtestd` (Go) and opens an SSH project on it. `e2e/bin` (first in the `PATH` of the pod) holds fake `claude`, `gh` and `tailscale` commands: the `preview` suite keeps its previews on `127.0.0.1`.
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
- **Shell**: never `pkill -x web-ide-pod` nor `pkill -f web-ide-pod`: the pod of the IDE (the service, which runs the assistant and its conversations) has the same name and dies with it, and `-f` also matches the keeper (`web-ide-keeper keeper`), which ends every terminal. Restart the pod with `systemctl --user restart web-ide-pod` only, and update the keeper with `web-ide-keeper keeper -upgrade`, never `systemctl --user restart web-ide-keeper`: a restart ends every terminal. Kill a test pod by the PID you kept (`$!`); `pkill -f <pattern>` also kills the command running it. Start a test pod with `setsid` / `< /dev/null`, otherwise a pipe stays open.
- **Copy through `copyText`** (`ui/clipboard.ts`), not `navigator.clipboard.writeText`: the text then goes to the clipboard history. A popup gives the focus back with `keepFocus` (`state/focus.ts`): focusing the editor again puts its caret at the start otherwise.
- **E2E tests** must wait actively (`waitForFunction`): language servers start cold. `<option>` elements are never "visible" for Playwright (`state: 'attached'`).

## Known limitations

- Code navigation was tested with gopls only; intelephense, phpactor, pyright and typescript-language-server were never tried.
- Tested in Chromium only.
- SSH was tested with the in-memory test server, not against a real host; ticket worktrees on SSH projects are untested. The Docker tool of an SSH project is untested (only its tunnels, through the test server).
- Global search uses RE2 syntax (no lookbehind).
- SQLite on an SSH project goes through the `sqlite3` command on the host (autocommit only).
- The microphone needs a secure context (localhost or HTTPS): with `-allow-remote` over plain HTTP, dictation is disabled (audio files are still transcribed).
