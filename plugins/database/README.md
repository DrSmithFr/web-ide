# Database plugin

The Database explorer: connections, tree, SQL consoles and table views. A built-in plugin: [plugins.md](../../docs/design/plugins.md); `plugin.json` is its manifest.

- **Pod** (`*.go`, package `database`): The `db.*` methods; a connection needing its password answers the coded error `db_password`. The connections are held by `internal/db`, a part of the runtime of the project (`conns.go`), closed with it.
- **Page** (`web/`, the chunk `plugin-database`): `connections.ts`, the tree, the console and table views `views.ts`, whose state, the text of a console included, goes with the tab.
- **E2e**: none of its own.

The page module imports the plugin API only (`@ide/api`, `@ide/types`, `@ide/capabilities`); the Go package imports the core of `pod/`.
