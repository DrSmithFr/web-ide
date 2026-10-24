# Services plugin

The HTTP services this pod shares with the other pods of the account (Ollama, the web UI of a daemon): [relay.md, *Services*](../../docs/design/relay.md#services). A built-in plugin ([plugins.md](../../docs/design/plugins.md)), pod-wide like Previews: no tool in the projects, so it is locked (`required`: a project has nothing to turn off); `plugin.json` is its manifest.

- **Pod** (`*.go`, package `services`): `manager.go` keeps `services.json` in the data folder of the pod (`{id, name, port, enabled}`, target `127.0.0.1:<port>`, one service per port, 0600) and the pods attached to each service, in memory (name, since, requests, bytes, last request); `peer.go` is its handler of the routes of the pods (`Host.HandlePeer`, `/_peer/services/`): `POST <id>/$attach` and `$detach`, then `<id>/<path>` proxied to the service (`httputil.ReverseProxy`, answers streamed, WebSocket upgraded, no cookie, host `127.0.0.1:<port>`). The global methods `services.list`, `save`, `delete`, `cut`; the event `services.changed`; the services that are on, advertised as `services: [{id, name}]` (`Host.Advertise`).
- **Page** (`web/`, the chunk `plugin-services`): `SharedServices.tsx`, a part of the home page (`home(api)`).
- **E2e** (`e2e/services.cjs`): the section of the home page.

## Invariants

- The core lets only a pod of the account reach the handler (its certificate proved on the LAN link or in the handshake of a pod-to-pod tunnel); the plugin then refuses a service that is off or unknown (404, the same answer) and a pod that is not attached (403).
- No port nor address leaves the pod: the other pods see `{id, name}` only.
- The attachments are in memory: a pod links again after a restart (#79).

## Tests

`go test ./plugins/services` (the store, refusals, counters, Cut, a streamed answer read token by token, a WebSocket echo); the routes of the core: `pod/server/peer_test.go`, `pod/lan` (`TestAdvertisedAndPeerIdentity`); e2e `services`.
