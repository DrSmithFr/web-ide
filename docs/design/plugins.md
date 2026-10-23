# Plugins

Status: **phases 1 and 2 done, phase 3 decided** (2026-10-20, [Phase 3](#phase-3-external-plugins): external plugins, confined). Milestone *Plugins, phase 1*: the core of the plugin system (#113), then Docker (#114) and the database (#115) moved onto it, all built-in, one lineage. The core is in place (#113), and Docker (#114) and the Database explorer (#115) are plugins: phase 1 is done. Whether Git, the editor, the kanban and the assistant become plugins too is decided after Docker (see [Decisions](#decisions)). The direction was explored on 2026-10-16.

## Vision

The IDE becomes a **window manager**: the core handles the connection (pod, relay, LAN, identity), the layout (rails, tool zones, panes, tabs, desks), the settings and the **buffers**. Everything else is a plugin, enabled per project: Git, Console, kanban, Docker, database, assistant, agents, questions, conflicts, structure… Even the text editor is a plugin: the default view of a buffer, one alternative view among others (rendered Markdown, diff…).

The tools that ship with the IDE are **built-in plugins**: same manifest, same API as an external one, compiled into the binary. The API is then complete by construction, since the IDE's own tools use nothing else.

## Why the buffers stay in the core

The buffer of a file, its sync with the disk (`buffer.synced`, `fs.changed`) and its conflicts are read by the assistant, the language servers, the diff, the conflict resolution and every alternative view. The view of a buffer is a plugin; the buffer itself is not. A rendered Markdown view reads the buffer, not the disk, so it follows the edits live.

## Shape of a plugin

A folder: `plugins/<id>/` of the repository for a built-in one (#133), `~/.web-ide/plugins/<id>/<version>/` for an external one, installed from its repository ([Phase 3](#phase-3-external-plugins)):

```
plugin.json   id, name, version, apiVersion, permissions, provides / requires, contributes
web/index.js  ES module, already built, loaded by the page
bin/…         optional backend: a process speaking JSON-RPC over stdio, any language
i18n/fr.json  its own catalog (the English text is the key, as in the IDE)
```

A built-in plugin has the same shape, with its sources instead of built files: `plugin.json` and its Go package (`*.go`) at the top, `web/` (its page module, built into the chunk `plugin-<id>`), `e2e/` (its browser suites, found by `e2e/run.sh`), `README.md`.

- **Backend**: a built-in plugin implements the Go interface `plugins.Plugin` inside the pod (`plugins/<id>/*.go`, its `plugin.json` embedded, listed by `pod/plugins/builtin`): `Register(host)` adds its RPC methods, named `<id>.<method>`, and its assistant tools, each one a schema and a handler that gets the project's `runtime`; an external one is a process started by the pod on demand and stopped a while after the last window leaves, like a language server. Same contract on both transports. Go's `plugin` package is ruled out (same toolchain, same build, Linux only).
- **Front end**: the module's `activate(api)` returns the components of its contributions, by id (`tools`, `views`, `commands`). The `api` (`web/src/plugins/api.ts`): `rpc()` to its backend, `on()` for the events (`fs.changed`, `git.changed`, `plugins.changed`…), `openFile`, `openView` (a tab of one of its views, with a serializable state), `t()`, the port forwards and the app previews of the core (`forwards`, `previews`: core features any plugin may show), and components of `ui/`. Solid is shared through an import map, so a plugin does not ship its own. A built-in plugin's `web/` is imported by the bundle (`web/src/plugins/registry.tsx`), given the same `api`; it imports the API as `@ide/api`, `@ide/types`, `@ide/capabilities` (an alias of `web/src/plugins/`), and one `node_modules` at the root (npm workspaces) gives it the page's `solid-js`.
- **Example**: Docker. `plugins/docker/` (manifest, the `docker.*` methods, the log streams, `docker_ps` / `docker_logs`) and `plugins/docker/web/` (the tool; `plugin.ts` keeps the `api` it was given, the only door to the core).
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
| Decorations | explorer badges (#121), status bar items of the focused tab or tool (#127) |

Phase 1 opens the points the moved tools need: tool, tab view, assistant tools, commands (#113). The others open when a plugin needs them.

## Capabilities between plugins

Plugins depend on each other: Docker, Git and the kanban open consoles; the kanban needs the worktrees of Git and the conversations of the assistant. A manifest declares the **capabilities** it `provides` and `requires` (`console` provides `terminal`, `git` provides `worktrees`, `kanban` requires both). Disabling a plugin on a project warns about the plugins that require it. (Not *services*: a service is an HTTP service shared between pods, see the [glossary](../../GLOSSARY.md).)

As long as Console and Git stay in the core, `terminal` and `worktrees` are provided by the core itself, so a plugin can already require them.

The assistant becomes a **consumer**: today `assistant/defs.go`, `assistant_tools.go` and `mcp_ide.go` know Docker, the consoles, the previews; tomorrow each plugin declares its tools and the assistant offers those of the plugins enabled on the project (a shorter prompt where Docker is off).

Coupling measured on the front end (imports, 2026-10-16): Docker, database, Git and Console depend on the core (`ui/`, `web/src/pod/rpc.ts`, `i18n`, `keys`, `state/project`) plus `console/consoles`, `llm/previews` or `editor/doc` only; the kanban and above all the assistant (`llm/`, which imports kanban, docker, doodle, console, editor, questions…) are the hubs. The Docker tool held the Port forwards and Previews tabs: since #114 they are a tool of the core of their own, *Port forwards and previews* (the Previews plugin since #119: `plugins/previews/web/`), also offered to plugins through the `api`.

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

## What Exec added to the API

Exec (#116) is the first plugin the rest of the IDE *uses*: a dozen parts of the page open or show consoles.

- **Capabilities are services.** The plugin providing one registers its implementation (`api.provide('terminal', …)`); the core asks `capability('terminal')` (`web/src/plugins/capabilities.ts`), another plugin `api.use('terminal')`. It is `undefined` while the provider is off, and the caller hides what needs it (*New terminal* in the tile launcher, a console tab falls back). The types of the services are the core's: `terminal` (consoles, open, show, the terminal view, navigate) and `commands` (the saved commands, their editor).
- On the pod, `capabilityOn(rt, c)`: the core tools of the assistant and the MCP that need a capability (`toolNeeds`: the consoles and saved commands need `terminal`) are offered and run only with it. They stay the assistant's: its approvals and its resume after a restart hold them, and they move with AI.
- **Namespaces** (`namespaces` in the manifest): the methods and actions of a plugin keep the names they had in the core (`console.create`, `commands.run`, `console.new`); a name with a prefix of the namespaces is taken as it is, by `Host.Handle` and by `api.rpc`.
- Events: `EmitOthers` (all the windows of the project but the caller: `console.created`) and `EmitFamily` (the main project and its worktrees: `commands.changed`).
- Page: `rpcIn` / `notifyIn` (a worktree attached to the window), a **channel** of the tunnel per terminal, `project.current` / `root` / `attached`, `showTool`, `tiles`, `theme` (font, terminal colors, tab size), `editor.View`, `keys.setDynamic` (the actions of the saved commands), `ui.WorktreeChip` and `ui.tabDrop`, the **hosts** contribution (a modal mounted by the window: the commands editor), commands with `inTerminal`.
- The console shown and the order of the console tabs moved from `session.bottom` to the desk state of the plugin (`plugins.exec`), migrated when read. The Problems tool left the console module for the core (`tools/problems.tsx`) until Languages.
- The setup command of the old kanban settings moves to the saved commands when the project opens.

## What Search added to the API

- **Search providers** (`searchProviders` contribution, `web/src/popups/searchProviders.ts`): Search Everywhere is a shell with a tab per provider (id, label, placeholder, order, `inAll`: its share of All, `delay`, `max`, `minChars`, `find`). The core gives the files, the symbols and the actions; Search gives the text. A tab whose provider is off falls back to All.
- **Tool placement**: a tool of the manifest names the tool it comes `before` in its zone by default (Search before Git, the Database explorer before the Assistant), so that a plugin keeps the place the tool had in the core.
- `api.rpc` takes an `AbortSignal`; `api.project.relPath`.
- On the pod, the plugin holds `search.grep`; `search.files` stays core (the explorer, Search Everywhere, the assistant's `find_files`), and so does the assistant's `search_text`, which moves with AI.

## What Git added to the API

- **Diff sources** (`diffSources` contribution, `web/src/ui/diffSources.ts`): the diff view is the core's, the sides of a `diff` tab come from its `source` (git when unset, as the desks saved before): `load(tab, work)`, `live`, `revision`, `label`, `Actions` (Stage / Unstage). `api.openDiff` opens a diff tab of the plugin's source.
- The **git service** (`capability('git')`): the status, its revision, the state of a file or a folder (explorer colors, Recent Files), the ignored files, the branches (project bar, kanban base), a file at a revision (the change markers of the editor).
- `worktrees` is the Git plugin's: the core provides no capability any more; *Start development* of the kanban needs it.
- The tab and the heights of the Git tool moved to its desk state (`plugins.git`, migrated).
- `fmtAgo`, `ui.FileIcon`, `project.basename`, `files.read`.

## What Previews added to the API

- **Global methods** (`Host.HandleGlobal`): a method without a project (`Call.Runtime` nil), always answered, for the home page and the pod-wide state: `tunnels.all`, `tunnels.close`, `tunnels.closeAll`, `preview.*` (a preview names its project).
- **The pod lent to built-ins** (`Host.Pod()`): the runtime of another project, the name and host of a project, events to every window or to a project, and, until #137, the pod-wide managers of the port forwards and of the previews (now the Previews plugin's). `Window.Project()`.
- **Lifecycle hooks**: `Host.OnProjectOpen(f)` (a window opens a project where the plugin is enabled: the enabled port forwards of an SSH project open) and `Host.OnWindows(f)` (the number of windows of the pod, in order: the forwards close `ForwardIdle` after the last one). The server knows nothing of the forwards any more.
- **Home contribution** (`PluginModule.home(api)`): a component of the home page, whatever the projects (the open port forwards).
- The **previews** capability (list, open, close, public, URL), used by the assistant's card, `share_preview` of the agents and the ▶ menu of the Console; `share_preview` and `list_previews` stay the assistant's, offered with `previews` on. `api.transport.inTunnel`.
- `api.forwards` and `api.previews` (#114) are gone: the previews are `api.use('previews')`. A removal is a change of version, but no plugin outside the IDE existed yet: v1 keeps its number, this exception is the only one.

## What Languages added to the API

- **Buffer events** (`api.buffers`): the buffers of the window (`list`, `get`, `version`), and `onOpen`, `onSave`, `onClose`; the plugin announces them to the servers (`didOpen`, `didChange`, `didSave`, `didClose`), the core no longer does.
- **The active editor** (`api.editor.active()`): the text view of the active tab and its buffer, null when another editor has the focus. The code actions (`lsp.definition`, `references`, `rename`, `format`, `hover`, `nav.fileStructure`…) are commands of the plugin with the same ids (shortcuts kept), run on it; the menus offer only the actions that are there.
- The **language** capability, asked by the editor of the core: the diagnostics of a file, the completion (null when a file has no server: the editor offers the words of the buffer), Ctrl+click, the Problems tool. The completion widget and its helpers (`editor/completion.ts`) and the LSP text edits (`editor/textEdits.ts`) stay the editor's.
- `api.editor.serverLanguage` / `serverLanguageId`, `wordStart`, `isWordChar`, `toOffsets`, `applyToString`, `settings`; `api.project.fileUri` / `pathFromUri`; `api.files.write`; `api.notify`; `api.activeFile`.
- The tab of the Problems tool moved to the desk state of the plugin (migrated). The assistant tools `lsp_*` stay the assistant's, offered with `language` on.

## What Files and Code added to the API

- **Locked plugins** (`required` in the manifest): always enabled; `plugins.set` refuses to turn them off, Settings → Plugins shows them checked, greyed, *Always on*. Files and Code have no method of their own: the files and the buffers are the core's (`fs.*`, `buffer.*`).
- **`fileView`**: the view of the file tabs, given by Code (the editor of a file buffer, its find bar, rendered Markdown, the change markers, the editing actions while it is active). The editing kit stays the core's, shared with the SQL consoles and the docs: `Doc`, `EditorView`, the completion widget, the find bar, `MarkdownView` (`api.editor`); a view registers its editor (`api.editor.register`) for `api.editor.active()`.
- **Explorer contributions**: `explorerItems(entry)` (Exec: *Open a terminal here*) and `fileDecorations` (`ignored(paths)`, `state(path, dir)`: Git's colors); the Explorer asks no other plugin. The **files** service: `reveal`, `createIn`.
- `api.buffers` (`load`, `error`, `save`, `cursor`, `saveCursor`, `target`), `api.conflicts.open`, `api.keys.actionOf` / `run`, `api.folders` (the marks of the folders), `api.explorer`, `api.recentFiles`, `api.activePath`, `api.project.name`. The folders expanded and the options of the Explorer moved to the desk state of Files (migrated).
- `api.desk().set` changes the state in place, created once from a copy of the defaults: an array pushed (the folders expanded) is seen, and the defaults are never shared.

## What Kanban added to the API

- **The tickets service** (`tickets`): the tickets of the project, a summary, a whole ticket with its blockers, open a ticket, the board, a milestone, an objective, start a conversation or the development of a ticket, the labels of a status and of a role, the worktree of a ticket. The assistant (the ticket bar), the Orchestrator and the project bar (the worktrees of the tickets, the ticket banner) use it, and hide their ticket parts without it.
- **The assistant and agents services**: `assistant` (the conversation shown, a new one, send, open one) and `agents` (the catalog, show a session, the stats of a ticket), which the Kanban starts its conversations and sessions through; the AI plugin provides them (#123).
- **View contributions**: `viewTitle(tab)`, the title of a tab from its state (`#126 Status bar…`), and `aloneViews`, the views alone in their tile on a desk of tiles, like a tool (the board).
- The board, ticket, milestone and objective tabs are views `plugin:kanban/<view>`, the number in the tab state; a desk saved before is migrated. The links `?ticket=`, `?milestone=`, `?objective=` open them through the service. The hosts of the plugins are mounted in a detached tool window too.
- `api.panes.closeTab`, `api.project.info` / `showWorktree`, `api.ui.Markdown` / `HelpLink`, `api.fmtSize`, `api.podPath`.
- On the pod: the methods moved to `plugins/kanban`; the core services reach it through `Pod().Services()` (store, projects, config, keeper, the assistant's model, desks, the token and the MCP address; the kanban manager is the plugin's own since #135). The `kanban_*` tools of the assistant (AI) call its exports, offered with `tickets` on; since #135 the MCP ones are its own and the AI's (`Host.MCPTool`).

## What AI added to the API

- **The AI plugin** (`ai`): the Assistant and Agents tools, the turn loop and its tools, sub-agents, the Orchestrator, compaction, doodles and the board of a conversation, speech recognition. It provides `assistant` (with `navigate`, the previous or next conversation: the side buttons of the mouse and the shortcuts, which stay the core's like those of the consoles) and `agents` (with the stats of the project, shown by the Info tool). Off: no Assistant nor Agents, `assistant.*` refused; the MCP for Claude Code stays the core's, but `request_doodle` (the doodle editor is the AI's).
- **Interface calls** (`ui.call`, answered with `ui.result`): the pod asks a window to run a tool of the interface (open a file or a diff, focus a panel, show a message, offer a preview), for the MCP and the assistant alike (`ui/uiCalls.ts`, `server/ui_calls.go`). A plugin adds its own: the **`uiTools`** contribution (AI: a page of its board, a doodle asked by an agent).
- What stays core: rendered Markdown and its diagrams in full screen (`ui/markdown.ts`, `ui/Markdown.tsx`, `ui/DiagramViewer.tsx`, now mounted by every project window), the question card of `ask_user` (`questions/AskCard.tsx`, `ask.ts`), the Questions tool.
- `api.window.detached`, `api.settings.open`, `api.plugins.assistantOptions`, `api.questions` (`AskCard`, `Form`, `formAnswerText`, `answerStrings`, `showsQuestions`), `api.editor.lineHunks`, `api.ui.openDiagram` / `Switch`, `api.project.rootOf` / `resolve`.
- On the pod: `Host.OnStart` / `OnStop` (the conversations that ran are taken back, saved when the pod stops); `Pod.Stopping`, `Plugin(id)` (a plugin uses another's exports: the AI the Kanban's), `UICall`, `AssistantTools` / `RunAssistantTool` (the tools of the other plugins), `Notify` (a `Note` since #135), `NoteAsks`, `CommandsText` / `StartSaved`, `SharePreview`; `Window.Watch` (the conversation a window shows); `Services.Models`. The kit of the tools (arguments, results, console tools, a conversation read) moved to `internal/assistant`, the shell commands without a terminal to `runtime` (`RunShell`, `FollowShell`), both shared with the MCP.

## Loading on demand

The page module of each built-in is a chunk of its own, `plugin-<id>` (`web/vite.config.ts`: rolldown's `advancedChunks`, only the files of the plugin's folder, without their dependencies, so what the core shares stays with it; a library only a plugin uses, such as xterm, gets a chunk loaded with it). The registry (`plugins/registry.tsx`) maps each id to a loader and loads a plugin when it is first enabled in the window, the locked ones first; the module is then activated once, and the contributions follow as each arrives. A disabled plugin is never downloaded.

- While a module loads, its tools and its tab views show a **placeholder** (*Loading…*), not the fallback of a disabled plugin; the file tabs wait for Code.
- What asks a service as the project opens waits for it (`whenProvided`: a `?ticket=` link waits for the Kanban, `?chat=` for the AI).
- The modules subscribe to the events of the pod as they load: a gateway keeps those subscribed before the activation (the AI's `on`).
- The home page loads only the built-ins that have a part of it (`homeIds`: Previews).
- Measured by the `perf` suite (2026-10-19, local): the home page went from 1636 KB of JS (one bundle) to 827 KB; a project window with every plugin enabled still loads them all (1682 KB, about 150 ms to the editor and its tools either way), less for each plugin off (the AI 256 KB, the Kanban 127 KB, Exec and xterm 355 KB…).

## What the status bar added to the API

- **`StatusItem`**: `id`, `text`, `icon`, `title`, `testid`, and an `action` (a click) or a `menu` (its options, opened above the item). Read in a memo: an item changes with what it shows.
- **Contributions**: `fileStatus(tab)` (a file tab; the plugin giving `fileView`: Code), `viewStatus(tab)` (a tab of the plugin's views: Kanban, a ticket's status, priority and size) and `toolStatus[toolId]()` (a tool in focus, in a zone or as a tab).
- The core keeps the bar (`ui/StatusBar.tsx`): the desks on its left, the choice of the focus (`focusPart`: the active tab of the focused pane, or the tool shown in the focused zone; a phone: the editor view) and the rendering. The file items (caret, line separator, encoding, indentation, language) moved to Code (`plugins/code/status.ts`); `api.editor.cursor()` reads the caret the view sets.

## What the layout added to the API

Moving the business code into the plugins (#134, milestone *Repository layout*) found two gaps on the pod side:

- **Parts of the runtime** (`runtime.Part(key, make)`, `pod/runtime/parts.go`): the per-project state of a plugin (its manager, its connections), made on first use; a part that is a `Stopper` stops with the project (`Stop(false)`) or the pod (`Stop(true)`: what the keeper holds survives), an `Idler` is told a while after the last window left. The runtime exposes what makers need: `Emit`, `Pool`, `Store`, `SettingsID`. The language servers, the database connections and docker are parts; their packages are the `internal/` of their plugin, so nothing else can import them.
- **Capabilities on the pod side** (`pod/caps`): `Host.Provide(name, impl)` for a capability of the manifest's `provides`, `Pod.Capability(rt, name)` (nil when no plugin enabled on the project provides it); each one an interface of that file. `Language` (Languages): the `lsp_*` tools of the AI reach the servers through it; `Search` (Search, #137): the search_text tool of the AI; `Previews` (Previews, #137): share_preview and list_previews of the AI and the MCP.
- **The pod-wide managers leave the pod** (#137): the port forwards and the previews are the Previews plugin's; `Pod.Forwards()`, `Pod.Previews()` and `Pod.SharePreview` are gone, `Pod.Authorized` (a request with the token, for a private preview) and `Host.HandlePreview` (the previews asked through the tunnel) take their place. A removal is a change of version, but no plugin outside the IDE exists yet: v1 keeps its number, as for `api.forwards` in #119. The consoles and the saved commands stay core (`pod/console`, `pod/commands`): the Exec plugin keeps its methods and its page side, and is locked (decision 6).
- **Tools for the MCP only** (#135): `Host.MCPTool(tool)`, its name listed in the manifest's `contributes.mcpTools` (one namespace with `assistantTools`). Its `Run` gets the `MCPScope` of the call: the project of the folder of the client (`cwd`) and its main project, the ticket of the worktree, the public address of the IDE, the language of the interface; `Needs` names a capability it also needs. Refused where its plugin is disabled, like an assistant tool. The schema helpers are `plugins.MCPSchema`, `Str`, `StrList`, `Enum`. The `kanban_*` ticket tools are the Kanban's, `kanban_conversation`, `kanban_reply` and `kanban_answer` the AI's (with `Needs: tickets`, through the Kanban's `TicketTool`); the saved commands, the tools of the IDE and `ask_user` stay the core's.
- **The kanban leaves the pod** (#135): its package is `plugins/kanban/kanban` (not `internal/`: the AI uses its types) and its manager is made by the plugin in `Register`, closed in `OnStop`; `Services.Kanban` is gone, the AI reaches it through `Pod.Plugin("kanban")`. `Pod.Notify(Note)` sends any push notification (kind, path, tag, urgency, texts per language) and replaces `Notify(asks, …)` and `NotifyFeedback`: the AI and the Kanban write their texts, the server only sends. The `OnStop` hooks run the last registered first, so a plugin stops before those it uses. The migration of the old kanban setup setting into a saved command is the plugin's (`OnProjectOpen`, and before a worktree is set up). Removals again, kept in v1 as in #119 and #137.

## API v1

Frozen with #118: `apiVersion: 1` in every manifest. An addition keeps v1; a removal or a change of what follows is a new version.

**Manifest** (`plugin.json`): `id`, `name`, `version`, `apiVersion`, `required`, `permissions`, `provides` / `requires` (capabilities), `namespaces` (prefixes of its methods and actions besides its id), `contributes`: `tools` (`id`, `label`, `icon`, `zone`, `before`), `tabViews`, `assistantTools`, `mcpTools` (#135), `commands` (`id`, `label`, `category`, `keys`, `replaces`, `inTerminal`).

**Pod** (`pod/plugins`): `Plugin{Manifest(), Register(Host)}`; `Host.Handle(method, handler)`, `Host.HandleGlobal`, `Host.AssistantTool(tool)`, `Host.MCPTool(tool)` (#135), `Host.Pod()`, `Host.OnProjectOpen`, `Host.OnWindows`, `Host.OnStart`, `Host.OnStop`, `Host.Provide` (#134), `Host.HandlePreview` (#137); `Pod.Capability` (#134), `Pod.Notify(Note)` (#135); a `Call` carries the `Runtime` of the project, `Params`, `Lang`, the `Window` (push, held streams), `Emit` / `EmitOthers` / `EmitFamily`, the `Options` of a conversation; `Result`, `UsageError`, `CodedError`.

**Page** (`web/src/plugins/api.ts`): `rpc` / `rpcIn` / `notify` / `notifyIn` / `channel`, `on`, `t` / `tn` / `fmtDate` / `fmtAgo`, `project`, `files`, `buffers`, `activeFile`, `ssh`, `desk` (its state in the desk), `provide` / `use` (capabilities), `showTool`, `tiles`, `theme`, `openFile` / `openView` / `setViewState` / `openDiff` / `openText`, `panes`, `editor`, `keys`, `transport`, `ui`. The module's `activate(api)` returns its contributions: `tools`, `views`, `fileView`, `commands`, `hosts`, `searchProviders`, `diffSources`, `explorerItems`, `fileDecorations`, `assistantOptions`, `viewTitle`, `aloneViews`, `fileStatus`, `viewStatus`, `toolStatus`, `uiTools`, `opened`; its `home(api)` a part of the home page.

**Capabilities**: on the pod, the interfaces of `pod/caps` (`Language`, `Search`, `Previews`); on the page, `terminal` and `commands` (Exec), `git` and `worktrees` (Git), `previews` (Previews), `language` (Languages), `files` (Files), `tickets` (Kanban), `assistant` and `agents` (AI), their types in `web/src/plugins/capabilities.ts`.

## Decisions

Taken on 2026-10-19:

1. **Trust**: built-in plugins are trusted code, with their permissions declared in the manifest. Phase 1 has only built-in plugins, so nothing is installed yet. *Superseded for external plugins on 2026-10-20*: they are confined, views and backend ([Phase 3](#phase-3-external-plugins)).
2. **SSH projects**: the backend of a plugin runs on the pod and reaches the project through `runtime` (local or SSH), as the assistant tools do. A `runsOn: "target"` may come later.
3. **Install**: not in phase 1. A plugin is enabled or disabled per project in *Settings → Plugins*, saved in `.ide/plugins.json` of the main project (`{"disabled": ["docker"]}`): every other plugin is enabled, so a new built-in shows on every project.
4. **Developing a plugin**: not in phase 1. Loading from a folder with hot reload comes once the API is stable.
5. **How far**: step by step. Phase 1 is the core, Docker, then the database. After it: everything becomes a plugin ([Phase 2](#phase-2-everything-is-a-plugin)).

Added on 2026-10-23, at the end of the *Repository layout* milestone:

6. **How far, answered**: not everything. **Explorer, Editor and the consoles stay core and locked** — `pod/console` and `pod/commands` stay packages of the pod, the Exec plugin keeps its page side, its `console.*` / `commands.*` methods and is `required` like Files and Code; the rest of the business code is in plugins (`plugins/search`, `plugins/previews`, languages, database, docker…). Security does not depend on this split: built-in plugins are trusted code and external ones are confined (phase 3), but keeping the terminal in the core means no external plugin can ever provide it.

## Phase 2: everything is a plugin

Decided on 2026-10-19, once Docker and the database had proved the API (decision 5 answered: yes). The core keeps the window manager: connection, layout, desks, tabs, settings, keymap, buffers and their sync, conflict resolution, help, the plugin registry; plus **shells** that plugins feed:

- **Search Everywhere** is a shell; each plugin contributes a **search provider** (files, symbols, actions, text, tickets…), a new contribution point (#117).
- **The diff view** is generic (two texts, or a change); Git, the kanban and the assistant give it their content (diff sources, #118).
- **Questions** stays in the core: the assistant, the agents and `ask_user` of the MCP all use it.

The plugins, grouped by domain (one plugin, several tools and views), in their order (milestone *Plugins, phase 2*: #116 Exec, #117 Search, #118 Git, #119 Previews, #120 Languages, #121 Files and Code, #122 Kanban, #123 AI, #124 loading on demand, #125 semantic search):

| Plugin | Tools and views | Provides / requires |
|---|---|---|
| Exec | Console tool (terminals, tasks), project commands (#116, done; locked, #137) | provides `terminal`, `commands` |
| Search | Search tool (text, regex), the text provider of Search Everywhere (#117, done) | provides `search` (the search_text tool of the AI, #137) |
| Git | Git tool, git content of the diff view (#118, done) | provides `worktrees`, `git` |
| Previews | Port forwards and previews tool (#119, done) | requires `terminal`, provides `previews` |
| Languages | language servers, Problems, Structure, the Symbols provider (#120, done) | provides `language` |
| Files | Explorer, file provider (locked, #121, done) | provides `files` |
| Code | file editor, the default view of a text buffer (locked, #121, done) | |
| Kanban | Kanban tool, board, ticket, milestone and objective views (#122, done; its store and its MCP ticket tools, #135; its assistant tools are the AI's) | provides `tickets`, requires `terminal`, `worktrees` |
| AI | Assistant, Agents, doodles, whiteboard (#123, done) | provides `assistant`, `agents` |

- **Locked plugins** (`required` in the manifest): Files and Code cannot be disabled, nor Exec since #137 (decision 6); Settings → Plugins shows them checked and greyed.
- **Languages apart from Code**: the editor without language servers (a slow SSH project).
- `apiVersion: 1` is frozen after Git ([API v1](#api-v1)).
- Then the **page modules load on demand** (`import()`, #124, done): a disabled plugin is not downloaded ([Loading on demand](#loading-on-demand)).
- A **semantic search** (embeddings by a local model) is a new provider of Search, after the move.

## Migration order

One ticket per step, the e2e suites of the moved tool passing before each merge:

1. Core: manifest, registry, capabilities, open zones and tab kinds, assistant tools from the plugins, per-project enabling. *(phase 1, #113, done; proved by the test plugin `hello`)*
2. Docker: the least coupled, it proves the API. *(phase 1, #114, done)*
3. Database *(phase 1, #115, done)*, then Console (the `terminal` capability).
4. Git (the `worktrees` capability), then the editor and rendered Markdown as views of a buffer.
5. Kanban, then the assistant last.

`apiVersion: 1` is frozen after Git (#118, [API v1](#api-v1)).

## Phase 3: external plugins

Decided on 2026-10-20 (milestone *Plugins, phase 3*, #128-#131). Built-in plugins stay trusted code in the bundle and the pod; an **external plugin** is installed from its own repository and **confined from the start**, views and backend, a model stricter than Zed's.

### Why confined

A plugin of this IDE reaches more than the machine it runs on. The pod's device key (`~/.web-ide/device.key`) lets its holder pass for the pod with the relay and the pods of the LAN, which open their terminals to it; `secrets.json` holds the API keys of the vault. In the browser, the device key is non-extractable but **usable** by any script of the origin while the session is unlocked (an hour after its last use, [relay](relay.md)), and a window on `127.0.0.1` holds the terminals of every LAN pod without a passkey. VS Code, PhpStorm (unconfined) or Zed (WebAssembly, but native language servers unconfined) risk one machine; here, one plugin risks every machine of the account.

| Part of a plugin | What a hostile one gets, unconfined | Severity |
|---|---|---|
| Backend `bin/` | the machine as the user, the pod's key (so the other pods), the vault, `~/.ssh`; lasts after the IDE closes | critical |
| View on the origin, `127.0.0.1` | every LAN pod: terminals, files, power, no passkey | critical |
| View on the origin, relay | every pod of the account while the session is unlocked, plus a fake passkey prompt | high to critical |
| View in a sandboxed iframe | what its grants allow, through the proxy | low |
| Manifest, catalogs | data only | very low |

### Source, versions, lock

- **One git repository per plugin**, one semver tag per version. Its `plugin.json` adds to [API v1](#api-v1): `ide` (a semver constraint on the IDE, `^1.24`), versioned requirements of other plugins (`"plugins": {"git": "^1"}`), besides the capabilities, and `assets`.
- **Release assets**: the tag carries one archive per architecture (`linux-amd64`, `linux-arm64`): `plugin.json`, `index.js` in `web/`, built as one file (its own Solid and CSS), `bin/` when it has a backend, `i18n/`. No compiled code in the repository, no toolchain on the pod.
- **Resolution like Composer**: the pod lists the tags (`git ls-remote --tags`), takes the highest matching the constraint and compatible with the IDE, the API and the installed plugins, downloads the asset of its architecture and checks it.
- **The lock** (`~/.web-ide/plugins.lock`): per plugin its URL, constraint, version, asset sha256 and grants. **Per pod, not synced**: a project opened on a pod lacking one of its plugins says so. A locked version is never downloaded again under the same tag: an asset whose hash changed is refused.

### Grants

The manifest's `permissions` become **grants**, accepted one by one by the user at install, asked again when an update adds one, revocable in *Settings → Plugins*: the project's files (read, or read-write), network, core methods it may call (`fs.read`, `openFile`…), running programs. Without a grant, the backend and the view are refused at the boundary, not by the plugin's goodwill.

### Sandboxed views

- An `<iframe sandbox="allow-scripts">` without `allow-same-origin`: an **opaque origin**, so no access to the page, IndexedDB, `localStorage`, cookies, the Service Worker or the top navigation.
- The page fetches the bundle itself (through the tunnel on the relay origin: the Service Worker does not serve an opaque origin) and injects it as `srcdoc` with a CSP `default-src 'none'; script-src 'unsafe-inline'; style-src 'unsafe-inline'; img-src data: blob:; connect-src 'none'`: no network, so nothing it is given can leave. A plugin needing the network asks its backend.
- One `MessageChannel` carries a **sandbox API**, a serializable part of the page API: `rpc` (its own `<id>.*` methods and the granted core ones), `on`, `openFile`, `openView`, `setViewState`, `t`, `toast`, the theme tokens, the shortcuts and the focus relayed. No shared `ui/` components; nothing drawn outside its frame.
- Contribution points open to external plugins: tools, tab views, commands, settings, assistant tools; the others open when one needs them.

### Confined backend

- `bin/` is started by a re-exec of the pod (`web-ide-pod plugin-exec`) that applies **Landlock** (files: its folder read-only, the project as granted, nothing else; so neither `~/.web-ide/` nor the home) and **seccomp**, then execs it; TCP connect and bind only with the network grant (Landlock network rules). Its children inherit the confinement.
- JSON-RPC over stdio, the same contract as `plugins.Plugin`; started on demand, stopped a while after the last window leaves, like a language server.
- A kernel without Landlock refuses to start it: no unconfined fallback.

### Steps

1. Install from a git repository (#128): resolution, assets, lock, *Settings → Plugins* (install by URL, update, remove, grants); listed, not runnable yet.
2. Sandboxed views (#129): the first usable external plugin, UI only.
3. Confined backend (#130), alongside #129.
4. Grants enforced end to end (#131), an example plugin in its own repository (`hello` moved out), the how-to *Write a plugin*.

## Open questions

- Answered on 2026-10-20 ([Phase 3](#phase-3-external-plugins)): the install source (a git repository, semver tags, release assets, a lock per pod, not synced) and the views of an external plugin (a sandboxed iframe).
- A catalog of plugins (like Packagist), signed releases: after phase 3.
