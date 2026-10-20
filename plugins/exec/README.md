# Exec plugin

The Console tool (terminals and tasks) and the saved commands of a project. It provides the `terminal` and `commands` capabilities. A built-in plugin: [plugins.md](../../docs/design/plugins.md); `plugin.json` is its manifest.

- **Pod** (`*.go`, package `exec`): The `console.*` and `commands.*` methods, kept as named through the namespaces of its manifest. The tools of the consoles stay the assistant's, offered only with `terminal` on (`toolNeeds` in `pod/server/handlers_plugins.go`).
- **Page** (`web/`, the chunk `plugin-exec`): `consoles.tsx` (one xterm per console), the saved commands `commands.ts` and `CommandsModal.tsx`.
- **E2e** (`e2e/`, run by `e2e/run.sh`): commands.

The page module imports the plugin API only (`@ide/api`, `@ide/types`, `@ide/capabilities`); the Go package imports the core of `pod/`.
