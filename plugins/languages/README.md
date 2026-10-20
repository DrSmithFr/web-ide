# Languages plugin

The language servers, Problems, Structure and the Symbols provider, apart from the editor. It provides the `language` capability. A built-in plugin: [plugins.md](../../docs/design/plugins.md); `plugin.json` is its manifest.

- **Pod** (`*.go`, package `languages`): The `lsp.*` methods; the servers are the runtime's (`pod/lsp`), started on demand, stopped when idle; the `lsp_*` tools stay the assistant's.
- **Page** (`web/`, the chunk `plugin-languages`): The client and the code actions `client.ts`, rename and formatting `refactor.ts`, the completion of the servers, the buffers announced to them `sync.ts`, the Problems and Structure tools.
- **E2e** (`e2e/`, run by `e2e/run.sh`): lsp.

The e2e suite needs `gopls`.

The page module imports the plugin API only (`@ide/api`, `@ide/types`, `@ide/capabilities`); the Go package imports the core of `pod/`.
