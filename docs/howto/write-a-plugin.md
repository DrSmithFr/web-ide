# Write a plugin

Write an external plugin in its own repository: views that run in the IDE, a backend if it needs one, published as tagged releases that the IDE installs, checks and confines.

An external plugin is untrusted by design ([plugins.md](../design/plugins.md#phase-3-external-plugins)): its views run in a sandboxed frame without network, its backend under Landlock and seccomp, and both reach the IDE only through what the user granted. Start from the example, [plugin-greet](https://github.com/antennae-space/plugin-greet): fork it, rename it, change it.

## The repository

```
plugin.json                    the manifest
web/index.js                   the page module: one ES module, already built
i18n/fr.json, i18n/es.json     its texts in the other languages (English is the key)
backend/…                      the sources of its backend (optional)
.github/workflows/release.yml  a tag v* publishes the release with its archives
```

A release carries one archive per architecture, `<id>-linux-amd64.tar.gz` and `<id>-linux-arm64.tar.gz`, holding at their root `plugin.json`, the `index.js` of `web/`, `i18n/` and the backend's executable (`bin/<name>`). No toolchain runs on the pod: build everything in the workflow.

## The manifest

```json
{
  "id": "greet",
  "name": "Greet",
  "version": "1.0.0",
  "apiVersion": 1,
  "ide": ">=1.23.0 <2",
  "plugins": { "git": "^1" },
  "assets": "https://github.com/you/plugin-greet/releases/download/v{version}/greet-{arch}.tar.gz",
  "backend": "bin/greet",
  "permissions": ["files:read", "core:fs.list"],
  "contributes": {
    "tools": [{ "id": "greet", "label": "Greet", "icon": "sparkle", "zone": "right" }],
    "tabViews": [{ "id": "note", "label": "Greet note" }],
    "commands": [{ "id": "hello", "label": "Say hello from Greet" }],
    "assistantTools": ["greet_count"],
    "toolSchemas": { "greet_count": { "description": "Counts the files at the root of the project.", "properties": {} } }
  }
}
```

- `id`: lowercase letters, digits and dashes; not the id of a built-in plugin. Its methods are `<id>.*`, its events `<id>.*`.
- `version`: the version of the release; the tag is `v` + it, and the IDE checks they match.
- `apiVersion`: `1`.
- `ide`: the versions of the IDE it runs on, a constraint (`^1.24`, `~1.24`, `>=1.23 <2`, `||` between alternatives). `plugins`: the plugins it needs, with a constraint each.
- `assets`: the address of its archives, `{version}` and `{arch}` (`linux-amd64`, `linux-arm64`) replaced; https only.
- `backend`: its executable in the archive, `bin/<name>`; none for a plugin made of views only.
- `permissions`: what it asks, each one granted by the user or not (see *Grants*).
- `contributes`: its tools (an icon of a rail: `left`, `bottomLeft`, `bottomRight`, `right`), tab views, commands, and its assistant tools, each described in `toolSchemas` (a description and the JSON Schema properties of its arguments; its backend runs them).

## The page module

The `index.js` of `web/` is one ES module, with what it needs bundled in (its own UI library if any). The IDE runs it in an `<iframe sandbox="allow-scripts">` of opaque origin whose CSP forbids the network: no `fetch`, no storage, no access to the IDE's page. It calls `ide.register`:

```js
ide.register({
  views: {
    greet: (el, ide) => { el.textContent = ide.t('Hello from Greet') },        // the tool "greet"
    note: (el, ide) => { el.textContent = JSON.stringify(ide.view.state) },  // the tab view "note"
  },
  commands: { hello: (ide) => ide.toast(ide.t('Hello!')) },
})
```

Each tool and tab view is a frame of its own, running the module and the view of its id; the commands run in a hidden frame. The sandbox API, `ide`:

| | |
|---|---|
| `ide.view` | `{id, state}`: the view of the frame and the state of its tab |
| `ide.rpc(method, params)` | a promise: its own `<id>.*` methods (its backend), or a method of the core with the grant `core:<method>` |
| `ide.on(event, f)` | the events of its namespace (`<id>.*`, sent by its backend) |
| `ide.openFile(path, line)`, `ide.openView(view, state, title)`, `ide.setViewState(state)` | open a file, a tab of one of its views, save the state of its tab |
| `ide.t(text, params)`, `ide.tn(n, one, other, params)`, `ide.lang` | its texts, from its `i18n/<lang>.json` |
| `ide.toast(text, kind)` | a message of the IDE (`info`, `warn`, `error`, `ok`) |
| `ide.theme`, `ide.onTheme(f)` | the colors of the IDE, also set as CSS variables (`var(--bg)`, `var(--fg)`…) |

The keys pressed in the frame with a modifier, Escape and F1-F12 reach the IDE's shortcuts.

## The backend

The IDE starts it on the first call of a project, through `web-ide-pod plugin-exec`, and stops it a while after the last window of the project leaves. It speaks JSON-RPC 2.0 over its stdin and stdout, one message per line (8 MB at most); its stderr goes to `~/.web-ide/plugin-data/<id>/backend.log`.

| From the IDE | |
|---|---|
| `initialize {apiVersion, plugin, version, grants, project: {id, root, local}}` | answer `{}` |
| `call {method, params, lang}` | an `<id>.*` method of one of its views: answer its result |
| `tool {name, args, lang}` | one of its assistant tools: answer `{content, summary, error}` |
| `shutdown` | answer, then exit |

| To the IDE | |
|---|---|
| `core {method, params}` (with an id) | a method of the core in the project, `fs.list` say: refused without `core:<method>` |
| `emit {event, data}` (no id) | an event to the windows of the project, `<id>.*` only |

A call waits two minutes at most; a backend that ends three times in a minute stays off until the project opens again. It runs confined: it reads its own folder and the system, writes its data folder (its `HOME` and `TMPDIR`), reads the project with `files:read`, writes it with `files:write`, reaches the network with `net` only; never the home, nor `~/.web-ide`, nor the other processes. A project over SSH is not on the pod: reach its files with `core` calls. Build it static (`CGO_ENABLED=0` for Go): it starts with the system's libraries only. A kernel without Landlock does not run it.

## Grants

The user grants each permission at the install, again for a new one asked by an update, and revokes them in *Settings → Plugins*, effective at once:

- `files:read`, `files:write`: its backend reads, or reads and writes, the project's files;
- `net`: its backend reaches the network (TCP, UDP);
- `exec`: running programs (reserved);
- `core:<method>`: its views and its backend call that method of the core (`core:fs.list`, `core:fs.read`…); the files permissions do not open them.

Ask for the least, and handle a refusal: a view or a backend without a grant gets an error, `<method> is not granted to <name>`.

## Versions and releases

1. Change `version` in `plugin.json`, commit, tag `v<version>`, push the tag: the workflow builds the archives and publishes the release.
2. Never move a tag once released. The IDE keeps the sha256 of the archive it installed (`~/.web-ide/plugins.lock`) and refuses another one under the same version: release a new version instead.
3. A pre-release (`v1.1.0-rc1`) is installed only by a constraint naming it (`=1.1.0-rc1`): use it to try a version before releasing it.

The IDE resolves like Composer: it lists the tags of the repository, takes the highest matching the user's constraint whose `plugin.json` fits the IDE (`ide`), the API and the installed plugins (`plugins`), and says why when none does.

## Install it

*Settings → Plugins → Install a plugin*: the address of the repository (https, ssh, or a folder of the machine) and a version constraint (empty: the latest). It is installed on this pod and used at once by the open project; another project uses it once its box is ticked.
