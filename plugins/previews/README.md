# Previews plugin

The Port forwards and previews tool. It requires `terminal` and provides `previews`. A built-in plugin: [plugins.md](../../docs/design/plugins.md); `plugin.json` is its manifest.

- **Pod** (`*.go`, package `previews`): The `tunnels.*` and `preview.*` methods, global; the managers of the forwards and previews are the server's (`pod/forwards`, `pod/preview`), lent through `Host.Pod()`; the forwards open with an SSH project and close after the last window through the lifecycle hooks.
- **Page** (`web/`, the chunk `plugin-previews`): The tool, the previews service `previews.ts`, the open forwards of the home page `OpenForwards.tsx`.
- **E2e** (`e2e/`, run by `e2e/run.sh`): forwards, preview.

The `forwards` suite builds `sshtestd` (`pod/sshtest`) and opens an SSH project on it.

The page module imports the plugin API only (`@ide/api`, `@ide/types`, `@ide/capabilities`); the Go package imports the core of `pod/`.
