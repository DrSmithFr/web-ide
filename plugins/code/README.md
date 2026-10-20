# Code plugin

Locked (`required`): the file editor, the default view of a text buffer. A built-in plugin: [plugins.md](../../docs/design/plugins.md); `plugin.json` is its manifest.

- **Pod** (`*.go`, package `code`): No method: the files and buffers are the core's.
- **Page** (`web/`, the chunk `plugin-code`): `FileEditor.tsx`, the view of the file tabs; `status.ts`, its items of the status bar.
- **E2e** (`e2e/`, run by `e2e/run.sh`): editor.

The page module imports the plugin API only (`@ide/api`, `@ide/types`, `@ide/capabilities`); the Go package imports the core of `pod/`.
