# `plugins`

The built-in plugins of the IDE, one folder each, in the shape of an external plugin ([plugins.md](../docs/design/plugins.md#shape-of-a-plugin)): `plugin.json` (the manifest), the Go package of its pod side (`*.go`, listed by `pod/plugins/builtin`), `web/` (its page module, the chunk `plugin-<id>`, loaded when the plugin is first enabled), `e2e/` (its browser suites, found by `e2e/run.sh`) and its `README.md`.

| Plugin | What it is |
|---|---|
| [ai](ai/README.md) | Assistant, Agents, doodles, the board |
| [code](code/README.md) | The file editor (locked) |
| [database](database/README.md) | The Database explorer |
| [docker](docker/README.md) | The Docker tool |
| [exec](exec/README.md) | Console and saved commands |
| [files](files/README.md) | The Explorer (locked) |
| [git](git/README.md) | The Git tool, worktrees |
| [hello](hello/README.md) | The plugin of the browser tests |
| [kanban](kanban/README.md) | Kanban, tickets, milestones, objectives |
| [languages](languages/README.md) | Language servers, Problems, Structure |
| [previews](previews/README.md) | Port forwards and app previews |
| [search](search/README.md) | The Search tool |

The plugin system itself stays in the core: `pod/plugins` (manifest, registry, enabling) and `web/src/plugins` (registry, the `api`, types, capabilities).

## Invariants

- A page module imports only the plugin API, as `@ide/api`, `@ide/types` and `@ide/capabilities` (an alias of `web/src/plugins`, in `web/tsconfig.json` and `web/vite.config.ts`). One `node_modules` at the root (npm workspaces) gives it the page's `solid-js`.
- Only the files of `plugins/<id>/web` go to its chunk; what the core shares with it stays in the core bundle.
- The texts of its page go through `t()` and the catalogs of `web/src/i18n` (checked by `web/scripts/i18n-check.mjs`, which reads the plugins too).
- A new e2e suite goes into the list of `e2e/run.sh`, which refuses a suite file missing from it.

## Tests

`go test ./plugins/...` from the root; `./e2e/run.sh <suite>` for one of its suites.
