# Hello plugin

The plugin of the browser tests, registered only with `WEBIDE_TEST_PLUGINS`: a tool, an RPC method, an assistant tool, a status bar item. A built-in plugin: [plugins.md](../../docs/design/plugins.md); `plugin.json` is its manifest.

- **Pod** (`*.go`, package `hello`): `hello.go`.
- **Page** (`web/`, the chunk `plugin-hello`): `index.tsx`.
- **E2e**: none of its own.

Its assertions are in the `plugins` suite of the core (`e2e/suites/plugins.cjs`).

The page module imports the plugin API only (`@ide/api`, `@ide/types`, `@ide/capabilities`); the Go package imports the core of `pod/`.
