# `relay`

The relay: signaling, STUN/TURN, the blob store, the identity and pairing link, the bootstrap page and its Service Worker (`web/`, embedded), Web Push, teams and tiers. It is not a binary of its own: `web-ide-pod relay` (`pod/main.go`) runs it, so a pod and a relay ship as one file. The design is in [relay.md](../docs/design/relay.md), the hosted relay and its costs in [load.md](../docs/design/load.md), running your own in [self-host-relay.md](../docs/howto/self-host-relay.md).

- `*.go`: the package `relay` (its files are listed in the *Network and identity* block of [the pod's packages](../pod/packages.md#network-and-identity)).
- `relayload/`: drives a relay with synthetic traffic over its network protocols, never its Go API; `cmd/relayload/` is its command (`make relayload`, `make load-relay`).
- `web/`: the bootstrap page, the Service Worker and the routes, embedded with `go:embed`.

## Invariants

- The relay imports only [`lib/`](../lib/README.md) (`identity`, `i18n`), never `pod/`: what the pod and the relay share lives there.
- The relay never holds a private key, a phrase or a project name: it checks signatures and caches ciphertext ([relay.md](../docs/design/relay.md)).

## Tests

`go test ./relay/...` from the root; `node --test scripts/routes-test.mjs` (the routes of the Service Worker); e2e: `relay`, `identity`, `multipod`, `push`, `sync`, `quota`, `renew`.
