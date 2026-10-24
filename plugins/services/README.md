# Services plugin

The HTTP services this pod shares with the other pods of the account (Ollama, the web UI of a daemon), and its links to theirs: [relay.md, *Services*](../../docs/design/relay.md#services). A built-in plugin ([plugins.md](../../docs/design/plugins.md)), pod-wide like Previews: no tool in the projects, so it is locked (`required`: a project has nothing to turn off); `plugin.json` is its manifest.

- **Pod** (`*.go`, package `services`): `manager.go` keeps `services.json` in the data folder of the pod (`{id, name, port, enabled}`, target `127.0.0.1:<port>`, one service per port, 0600) and the pods attached to each service, in memory (name, since, requests, bytes, last request); `peer.go` is its handler of the routes of the pods (`Host.HandlePeer`, `/_peer/services/`): `POST <id>/$attach` and `$detach`, then `<id>/<path>` proxied to the service (`httputil.ReverseProxy`, answers streamed, WebSocket upgraded, no cookie, host `127.0.0.1:<port>`). The global methods `services.list`, `save`, `delete`, `cut`; the event `services.changed`; the services that are on, advertised as `services: [{id, name}]` (`Host.Advertise`).
- **Links** (`links.go`): `service-links.json` (`{pod, service, port, scope, on}`, 0600); a link that is on attaches on its pod (`RelinkHeader` when the user turned it on: only that brings back a pod that was cut) and listens on `127.0.0.1` or `0.0.0.0`, on the port typed, else the port of the service, else the next free one; its reverse proxy sends each request through `Pod.Peers().Transport()` (the LAN first, else the relay). A 403 (the pod restarted and forgot it) attaches again by itself; a 409 (cut) turns the link off. States `on`, `offline` (502), `cut`, `gone`, `port-taken`; links on start with the pod (`OnStart`). Methods `services.remote` (the services of the other pods, from what they advertise, with the links), `services.link`, `services.forget`; event `services.links`.
- **Page** (`web/`, the chunk `plugin-services`): a part of the home page (`home(api)`): `SharedServices.tsx` (this pod's, with the pods attached and their route), `LinkedServices.tsx` (the other pods'), `RouteBadge.tsx`.
- **E2e** (`e2e/services.cjs`): the section of the home page.

## Invariants

- The core lets only a pod of the account reach the handler (its certificate proved on the LAN link or in the handshake of a pod-to-pod tunnel); the plugin then refuses a service that is off or unknown (404, the same answer) and a pod that is not attached (403).
- No port nor address leaves the pod: the other pods see `{id, name}` only.
- The attachments and the cuts are in memory: a pod that restarts is attached again by the next request of each link (a cut pod is not: 409).

## Tests

`go test ./plugins/services` (the store, refusals, counters, Cut, a streamed answer read token by token, a WebSocket echo; the links: next free port, typed port taken, offline, cut, re-attached after a restart of either pod); the core: `pod/server/peer_test.go`, `pod/lan` (`TestAdvertisedAndPeerIdentity`), `pod/multipod` (`TestPeerClient`), `pod/relayclient` (`TestServicesPodToPod`, a real tunnel); e2e `services` and `multipod`.
