# Plugins (draft)

Status: **under discussion**, nothing is decided or developed. This page records the direction explored on 2026-10-16 and the questions still open.

## Vision

The IDE becomes a **window manager**: the core handles the connection (pod, relay, LAN, identity), the layout (rails, tool zones, panes, tabs, session), the settings and the **documents**. Everything else is a plugin, enabled per project: Git, Console, kanban, Docker, database, assistant, agents, questions, conflicts, structure… Even the text editor is a plugin: the default view of a text document, one alternative view among others (rendered Markdown, diff…).

The tools that ship with the IDE are **built-in plugins**: same manifest, same API as an external one, compiled into the binary. The API is then complete by construction, since the IDE's own tools use nothing else.

## Why the documents stay in the core

The buffer of a file, its sync with the disk (`buffer.synced`, `fs.changed`) and its conflicts are read by the assistant, the language servers, the diff, the conflict resolution and every alternative view. The view of a document is a plugin; the document itself is not. A rendered Markdown view reads the document, not the disk, so it follows the edits live.

## Shape of a plugin

A folder (`~/.web-ide/plugins/<id>/` for an external one):

```
plugin.json   id, version, apiVersion, permissions, provides / requires, contributions
web/index.js  ES module, already built, loaded by the page
bin/…         optional backend: a process speaking JSON-RPC over stdio, any language
i18n/fr.json  its own catalog (the English text is the key, as in the IDE)
```

- **Backend**: a built-in plugin implements a Go interface inside the pod; an external one is a process started by the pod on demand and stopped a while after the last window leaves, like a language server. Same contract on both transports. Go's `plugin` package is ruled out (same toolchain, same build, Linux only).
- **Front end**: the module receives an `api` object: `rpc()` to its backend, events (`fs.changed`, `git.changed`, `session.changed`…), `openFile`, `t()`, the components of `ui/`. Solid is shared through an import map, so a plugin does not ship its own.
- **Lazy loading**: only the plugins enabled on the project are loaded, which also shortens the first load (the iPhone through the relay).

## Contribution points

| Point | Notes |
|---|---|
| Tab view | `TabKind` (`state/project.ts`) becomes open: `plugin:<id>/<view>`, with a serializable state for the session (diff, table, ticket today) |
| Alternative view | by file pattern (`openWith`), source ↔ rendered toggle or side by side; the editor is the default view of text |
| Tool | `zones.ts` takes plugin ids, each with a default zone; a badge on the rail icon (like Problems) |
| Assistant tools | schema + backend handler, offered to the integrated assistant, to Claude Code (`/mcp`) and to the ACP agents from one declaration |
| Commands | Search Everywhere, keymap shortcuts, menus (explorer, editor, tab context menus) |
| Settings | a JSON schema rendered as a section of the settings, synced like the others |
| Languages | grammars (already one module per language), language server command, test patterns |
| Decorations | explorer badges, status bar items |

## Services between plugins

Plugins depend on each other: Docker, Git and the kanban open consoles; the kanban needs the worktrees of Git and the conversations of the assistant. A manifest declares what it `provides` and `requires` (`console` provides `terminal`, `git` provides `worktrees`, `kanban` requires both). Disabling a plugin on a project warns about the plugins that require it.

The assistant becomes a **consumer**: today `agent_tools.go` and `mcp_ide.go` know Docker, the consoles, the previews; tomorrow each plugin declares its tools and the assistant offers those of the plugins enabled on the project (a shorter prompt where Docker is off).

Coupling measured on the front end (imports, 2026-10-16): Docker, database, Git and Console depend on the core (`ui/`, `pod/rpc`, `i18n`, `keys`, `state/project`) plus `console/consoles`, `llm/previews` or `editor/doc` only; the kanban and above all the assistant (`llm/`, which imports kanban, docker, doodle, console, editor, questions…) are the hubs.

## Migration order (if decided)

One ticket per step, the e2e suites of the moved tool passing before each merge:

1. Core: manifest, registry, services, open zones and tab kinds, per-project enabling.
2. Docker: the least coupled, it proves the API.
3. Database, then Console (the `terminal` service).
4. Git (the `worktrees` service), then the editor and rendered Markdown as views of a document.
5. Kanban, then the assistant last.

`apiVersion: 1` is frozen only after Git: until then every plugin is internal and the API may change with each step.

## Open questions

1. **Trust and sandbox.** Behind the relay, the page holds the device key and signs updates and power requests; a module loaded in the page has the same rights. Trusted plugins with declared permissions shown at install (the VS Code model), or views in a sandboxed iframe of another origin talking by `postMessage` (safer, heavier, and the single origin of the relay complicates it)?
2. **SSH projects.** Does the backend of a plugin run on the pod (through `runtime`, like the assistant tools) or on the remote host (`runsOn: "target"`)?
3. **Install.** A git URL and a tag, `web/` shipped built? Enabled per project in `.ide/`? The list of installed plugins synced between pods?
4. **Developing a plugin.** Load from a folder with hot reload, a `make plugin` scaffold?
5. **How far.** Is the "everything is a plugin" core worth the refactor, or do the editor, Git, Console, the assistant and the kanban stay in the core with only the leaf tools (Docker, database, Markdown render) as plugins?
