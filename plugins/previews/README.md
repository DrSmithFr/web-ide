# Previews plugin

The Port forwards and previews tool. It requires `terminal` and provides `previews`. A built-in plugin: [plugins.md](../../docs/design/plugins.md); `plugin.json` is its manifest.

- **Pod** (`*.go`, package `previews`): The `tunnels.*` and `preview.*` methods, global; the managers are the plugin's, one per pod: `internal/forwards` (local ports that reach an address seen from the SSH host of a project, closed `ForwardIdle` after the last window; their specs in `.ide/forwards.json`, `specs.go`) and `internal/preview` (a reverse proxy per preview, the cookie of the IDE or the token of a public one, exposed by `tailscale serve` / `funnel`; the server hands it the previews asked through the tunnel, `Host.HandlePreview`). Both close when the pod stops. It provides the `previews` capability (`caps.Previews`: `Share`, `List`, `share.go`), which the share_preview and list_previews tools of the assistant and the MCP use.
- **Page** (`web/`, the chunk `plugin-previews`): The tool, the previews service `previews.ts`, the open forwards of the home page `OpenForwards.tsx`.
- **E2e** (`e2e/`, run by `e2e/run.sh`): forwards, preview.

The `forwards` suite builds `sshtestd` (`pod/sshtest`) and opens an SSH project on it.

The page module imports the plugin API only (`@ide/api`, `@ide/types`, `@ide/capabilities`); the Go package imports the core of `pod/`.
