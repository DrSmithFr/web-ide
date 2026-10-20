# Search plugin

The Search tool and the text provider of Search Everywhere. A built-in plugin: [plugins.md](../../docs/design/plugins.md); `plugin.json` is its manifest.

- **Pod** (`*.go`, package `search`): `search.grep` (`search.files` stays core); the search itself is `pod/search`.
- **Page** (`web/`, the chunk `plugin-search`): `GlobalSearch.tsx`, the text provider `text.ts`.
- **E2e**: none of its own.

The page module imports the plugin API only (`@ide/api`, `@ide/types`, `@ide/capabilities`); the Go package imports the core of `pod/`.
