# Search plugin

The Search tool and the text provider of Search Everywhere. A built-in plugin: [plugins.md](../../docs/design/plugins.md); `plugin.json` is its manifest.

- **Pod** (`*.go`, package `search`): `search.grep`; the search itself is `internal/search` (RE2 locally, grep over SSH). It provides the `search` capability (`caps.Search`), which the search_text tool of the assistant needs. The list of the files (`search.files`, the explorer, find_files) is the runtime's (`pod/runtime/files.go`), so it answers without the plugin.
- **Page** (`web/`, the chunk `plugin-search`): `GlobalSearch.tsx`, the text provider `text.ts`.
- **E2e**: none of its own.

The page module imports the plugin API only (`@ide/api`, `@ide/types`, `@ide/capabilities`); the Go package imports the core of `pod/`.
