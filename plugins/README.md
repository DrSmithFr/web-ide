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
- An external plugin (installed from its own repository, [plugins.md](../docs/design/plugins.md#phase-3-external-plugins)) has no folder here: its page module is one built file run in sandboxed frames (`web/src/plugins/external.tsx`, `sandbox.ts`), calling `ide.register` and the sandbox API; its backend, when it has one, runs confined (`pod/plugins/confine`) and speaks JSON-RPC over stdio (`pod/plugins/backend`).
- Its texts (page module, manifest labels, messages of its Go package) are in its own catalogs, `plugins/<id>/i18n/<lang>.json`; a text shared with the core stays in the core's (`web/src/i18n`, `lib/i18n`), where `api.t()` looks second. Its Go package adds them in `init` (`i18n.go`: `i18n.Add`); a sub-package tested alone adds them in its tests (`plugins/ai/llm/i18n_test.go`). Checked by `web/scripts/i18n-check.mjs` and the `lib/i18n` tests ([plugins.md](../docs/design/plugins.md#shape-of-a-plugin)).
- A new e2e suite goes into the list of `e2e/run.sh`, which refuses a suite file missing from it.

## Tests

`go test ./plugins/...` from the root; `./e2e/run.sh <suite>` for one of its suites.
