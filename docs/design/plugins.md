# Plugins

Status: **phase 1 done, phase 2 decided** (2026-10-19, [Phase 2](#phase-2-everything-is-a-plugin)). Milestone *Plugins, phase 1*: the core of the plugin system (#113), then Docker (#114) and the database (#115) moved onto it, all built-in, one lineage. The core is in place (#113), and Docker (#114) and the Database explorer (#115) are plugins: phase 1 is done. Whether Git, the editor, the kanban and the assistant become plugins too is decided after Docker (see [Decisions](#decisions)). The direction was explored on 2026-10-16.

## Vision

The IDE becomes a **window manager**: the core handles the connection (pod, relay, LAN, identity), the layout (rails, tool zones, panes, tabs, desks), the settings and the **buffers**. Everything else is a plugin, enabled per project: Git, Console, kanban, Docker, database, assistant, agents, questions, conflicts, structure… Even the text editor is a plugin: the default view of a buffer, one alternative view among others (rendered Markdown, diff…).

The tools that ship with the IDE are **built-in plugins**: same manifest, same API as an external one, compiled into the binary. The API is then complete by construction, since the IDE's own tools use nothing else.

## Why the buffers stay in the core

The buffer of a file, its sync with the disk (`buffer.synced`, `fs.changed`) and its conflicts are read by the assistant, the language servers, the diff, the conflict resolution and every alternative view. The view of a buffer is a plugin; the buffer itself is not. A rendered Markdown view reads the buffer, not the disk, so it follows the edits live.

## Shape of a plugin

A folder (`~/.web-ide/plugins/<id>/` for an external one, later):

```
plugin.json   id, name, version, apiVersion, permissions, provides / requires, contributes
web/index.js  ES module, already built, loaded by the page
bin/…         optional backend: a process speaking JSON-RPC over stdio, any language
i18n/fr.json  its own catalog (the English text is the key, as in the IDE)
```

- **Backend**: a built-in plugin implements the Go interface `plugins.Plugin` inside the pod (`pod/internal/plugins/<id>/`, its `plugin.json` embedded): `Register(host)` adds its RPC methods, named `<id>.<method>`, and its assistant tools, each one a schema and a handler that gets the project's `runtime`; an external one is a process started by the pod on demand and stopped a while after the last window leaves, like a language server. Same contract on both transports. Go's `plugin` package is ruled out (same toolchain, same build, Linux only).
- **Front end**: the module's `activate(api)` returns the components of its contributions, by id (`tools`, `views`, `commands`). The `api` (`web/src/plugins/api.ts`): `rpc()` to its backend, `on()` for the events (`fs.changed`, `git.changed`, `plugins.changed`…), `openFile`, `openView` (a tab of one of its views, with a serializable state), `t()`, the port forwards and the app previews of the core (`forwards`, `previews`: core features any plugin may show), and components of `ui/`. Solid is shared through an import map, so a plugin does not ship its own. A built-in plugin is a folder of `web/src/plugins/` imported by the bundle, given the same `api`.
- **Example**: Docker. `pod/internal/plugins/docker/` (manifest, the `docker.*` methods, the log streams, `docker_ps` / `docker_logs`) and `web/src/plugins/docker/` (the tool; `plugin.ts` keeps the `api` it was given, the only door to the core).
- **The manifest is the pod's**: the page gets the manifests and their state on the project in `project.open`, then `plugins.changed`; the page module brings only the components.
- **Lazy loading**: only the plugins enabled on the project are loaded, which also shortens the first load (the iPhone through the relay).

## Contribution points

| Point | Notes |
|---|---|
| Tab view | `TabKind` (`state/project.ts`) becomes open: `plugin:<id>/<view>`, with a serializable state for the desk (diff, table, ticket today) |
| Alternative view | by file pattern (`openWith`), source ↔ rendered toggle or side by side; the editor is the default view of text |
| Tool | `zones.ts` takes plugin ids, each with a default zone (after the tools of the core), and keeps the place of a disabled one; a badge on the rail icon (like Problems); a tool also renders as a tile ([desks](desks.md)) |
| Assistant tools | schema + backend handler, offered to the assistant, to Claude Code (`/mcp`) and to the ACP agents from one declaration. The assistant gets those of the plugins enabled on its project; the MCP endpoint serves every project (the `cwd` comes with each call), so it lists them all and refuses a call where the plugin is disabled |
| Commands | Search Everywhere, keymap shortcuts, menus (explorer, editor, tab context menus) |
| Settings | a JSON schema rendered as a section of the settings, synced like the others |
| Languages | grammars (already one module per language), language server command, test patterns |
| Decorations | explorer badges, status bar items |

Phase 1 opens the points the moved tools need: tool, tab view, assistant tools, commands (#113). The others open when a plugin needs them.

## Capabilities between plugins

Plugins depend on each other: Docker, Git and the kanban open consoles; the kanban needs the worktrees of Git and the conversations of the assistant. A manifest declares the **capabilities** it `provides` and `requires` (`console` provides `terminal`, `git` provides `worktrees`, `kanban` requires both). Disabling a plugin on a project warns about the plugins that require it. (Not *services*: a service is an HTTP service shared between pods, see the [glossary](../../GLOSSARY.md).)

As long as Console and Git stay in the core, `terminal` and `worktrees` are provided by the core itself, so a plugin can already require them.

The assistant becomes a **consumer**: today `assistant/defs.go`, `assistant_tools.go` and `mcp_ide.go` know Docker, the consoles, the previews; tomorrow each plugin declares its tools and the assistant offers those of the plugins enabled on the project (a shorter prompt where Docker is off).

Coupling measured on the front end (imports, 2026-10-16): Docker, database, Git and Console depend on the core (`ui/`, `pod/rpc`, `i18n`, `keys`, `state/project`) plus `console/consoles`, `llm/previews` or `editor/doc` only; the kanban and above all the assistant (`llm/`, which imports kanban, docker, doodle, console, editor, questions…) are the hubs. The Docker tool held the Port forwards and Previews tabs: since #114 they are a tool of the core of their own, *Port forwards and previews* (`web/src/forwards/`), also offered to plugins through the `api`.

## What Docker added to the API

Moving Docker (#114) found what the API of #113 lacked; each gap was added to the API, not worked around:

- **The calling window** (`plugins.Call.Window`): events pushed to it (`docker.log`) and streams it holds until it stops them or leaves (`Hold` / `Release`).
- **Options of a conversation** (`Call.Options`): what the page module gives for the assistant (`assistantOptions`: the Compose profiles chosen in the tool), read by its assistant tools. The MCP passes none; the tool then takes its own parameters.
- **Usage errors** (`plugins.UsageError`), counted apart by the statistics of the assistant.
- **A plain summary** of a tool result (`Result.Summary`, raw: a text to translate or a plain one).
- On the page: the plugin's **state in the desk** (`api.desk(defaults)`, saved under `plugins.<id>` of the desk; the former `docker` key is moved there), the **project** (`isLocal`), the **terminal** capability (`terminal.open`), menus, prompt and ANSI colors in `ui`.

## What the database added to the API

- **Events to every window of the project** (`Call.Emit`: `db.changed`, `db.consoleState`).
- **Coded errors** (`plugins.CodedError{Code, Data}`): `db_password` reaches the page as before, which asks the password and tries again; the core no longer knows the error of the database.
- **Tab views with their state**: `plugin:database/sql` and `plugin:database/table`, their connection, database, table and the text of a console in `tab.state` (`api.setViewState`), so the state goes with the tab when it splits or moves to another desk. A desk saved before (kinds `sql` / `table`, `sqlText`) is migrated when read.
- On the page: the **editor** (`api.editor`: `Doc`, the view of a tab, the cursor of the status bar), the **keymap** (`api.keys`; a command of the manifest has default `keys` and `replaces`, the former id whose shortcuts still apply: `sql.execute` is now `database.execute`), the **panes** (`isActive`, `activate`), `openText`, `Modal`, `pick`, `copyText`, `tn`, `fmtDate`, `ssh.info`, and the `opened` contribution (called when the project opens, after a reconnection, or when the plugin is enabled again).

## Decisions

Taken on 2026-10-19:

1. **Trust**: plugins are trusted code, with their permissions declared in the manifest and shown at install (the VS Code model). Phase 1 has only built-in plugins, so nothing is installed yet. A sandboxed iframe for third-party views stays possible later, behind the same `api`.
2. **SSH projects**: the backend of a plugin runs on the pod and reaches the project through `runtime` (local or SSH), as the assistant tools do. A `runsOn: "target"` may come later.
3. **Install**: not in phase 1. A plugin is enabled or disabled per project in *Settings → Plugins*, saved in `.ide/plugins.json` of the main project (`{"disabled": ["docker"]}`): every other plugin is enabled, so a new built-in shows on every project.
4. **Developing a plugin**: not in phase 1. Loading from a folder with hot reload comes once the API is stable.
5. **How far**: step by step. Phase 1 is the core, Docker, then the database. After it: everything becomes a plugin ([Phase 2](#phase-2-everything-is-a-plugin)).

## Phase 2: everything is a plugin

Decided on 2026-10-19, once Docker and the database had proved the API (decision 5 answered: yes). The core keeps the window manager: connection, layout, desks, tabs, settings, keymap, buffers and their sync, conflict resolution, help, the plugin registry; plus **shells** that plugins feed:

- **Search Everywhere** is a shell; each plugin contributes a **search provider** (files, symbols, actions, text, tickets…), a new contribution point.
- **The diff view** is generic (two texts, or a change); Git, the kanban and the assistant give it their content.
- **Questions** stays in the core: the assistant, the agents and `ask_user` of the MCP all use it.

The plugins, grouped by domain (one plugin, several tools and views), in their order (milestone *Plugins, phase 2*: #116 Exec, #117 Search, #118 Git, #119 Previews, #120 Languages, #121 Files and Code, #122 Kanban, #123 AI, #124 loading on demand, #125 semantic search):

| Plugin | Tools and views | Provides / requires |
|---|---|---|
| Exec | Console tool (terminals, tasks), project commands, their assistant tools | provides `terminal` |
| Search | Search tool (text, regex), the text provider of Search Everywhere | |
| Git | Git tool, git content of the diff view | provides `worktrees` |
| Previews | Port forwards and previews tool | requires `terminal` |
| Languages | language servers, Problems, Structure, `lsp_*` tools, symbol provider | provides `symbols` |
| Files | Explorer, file provider (locked) | |
| Code | file editor, the default view of a text buffer (locked) | |
| Kanban | Kanban tool and views, its MCP and assistant tools | requires `terminal`, `worktrees` |
| AI | Assistant, Agents, doodles, whiteboard | |

- **Locked plugins** (`required` in the manifest): Files and Code cannot be disabled; Settings → Plugins shows them checked and greyed.
- **Languages apart from Code**: the editor without language servers (a slow SSH project).
- `apiVersion: 1` is frozen after Git.
- Then the **page modules load on demand** (`import()`): a disabled plugin is not downloaded.
- A **semantic search** (embeddings by a local model) is a new provider of Search, after the move.

## Migration order

One ticket per step, the e2e suites of the moved tool passing before each merge:

1. Core: manifest, registry, capabilities, open zones and tab kinds, assistant tools from the plugins, per-project enabling. *(phase 1, #113, done; proved by the test plugin `hello`)*
2. Docker: the least coupled, it proves the API. *(phase 1, #114, done)*
3. Database *(phase 1, #115, done)*, then Console (the `terminal` capability).
4. Git (the `worktrees` capability), then the editor and rendered Markdown as views of a buffer.
5. Kanban, then the assistant last.

`apiVersion: 1` is frozen only after Git: until then every plugin is internal and the API may change with each step.

## Open questions

- External plugins: the install source (a git URL and a tag, `web/` shipped built), whether the list of installed plugins syncs between pods.
- Views of an external plugin: same origin with the device key in reach, or a sandboxed iframe (the single origin of the relay complicates it).
