# Docker plugin

The Docker tool: Compose projects, containers, images, volumes, disk usage, logs. A built-in plugin: [plugins.md](../../docs/design/plugins.md); `plugin.json` is its manifest.

- **Pod** (`*.go`, package `docker`): The `docker.*` methods, the log streams held by the window, the assistant tools `docker_ps` and `docker_logs` with the Compose profiles of the conversation as options. The `docker` command runs through `pod/docker` in the runtime of the project.
- **Page** (`web/`, the chunk `plugin-docker`): Lists and polling `state.ts`, detail pane, logs with ANSI colors, disk usage; `plugin.ts` holds its `api` and its desk state, nothing else of the core is imported.
- **E2e** (`e2e/`, run by `e2e/run.sh`): docker.

The e2e suite needs Docker with Compose and the `postgres:17-alpine` image (skipped otherwise); it never prunes.

The page module imports the plugin API only (`@ide/api`, `@ide/types`, `@ide/capabilities`); the Go package imports the core of `pod/`.
