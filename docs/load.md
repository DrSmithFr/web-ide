# How many users the hosted relay holds

Measured on the relay host itself (#72): a second relay instance, the same machine, the same
limits, driven over the network by `pod/internal/relayload` (see `make load-relay`).

## What is measured

`relayload` speaks the relay's own protocol — HTTP pages, the pod and browser WebSocket links,
and real WebRTC tunnels through the embedded TURN server, with the token handshake on the channel
and a payload pumped through it. Sessions are ramped, then held: the relay is measured *carrying*
them, not carrying the ramp. `/_relay/status` is sampled while it happens, so the numbers below
are what the relay itself reported.

```
make load-relay LOAD_TIER=signal LOAD_PODS=20 LOAD_SESSIONS=100
LOAD_TIER=static|signal|turn · LOAD_PODS · LOAD_SESSIONS · LOAD_MB · LOAD_RAMP · LOAD_HOLD
```

`scripts/relay-load.sh` installs the test instance on the relay host: its own unit
(`antennae-relay-test`), its own data folder, its own ports (4431 / 4443 / TURN 3479) and a TURN
port range of its own — the hosted relay is never touched.

## The host

OVH VPS: 6 vCPU, 11 GiB RAM. Both units run with `MemoryMax=1G`. The hosted relay
(`antennae-relay`) is started with `-turn-ports 49160-49999`: **840 relay ports**. The test
instance runs with a range chosen for the run (500 ports, then 3000).

## The numbers (2026-10-15, v1.13.0)

| tier | run | result |
| --- | --- | --- |
| static (pages, Service Worker) | 20 s, 4 workers | 4621 requests, p50 34 ms, p95 35 ms, 71.6 MB read, 30 Mbit/s, relay 35 MB RSS |
| signal (pod + browser links) | 20 pods, 100 sessions | 100 up, 0 failed, p50 179 ms, relay 42 MB RSS, 179 fds, 224 goroutines |
| turn (real tunnels) | 4 pods, 20 sessions | 20 up, p50 2.97 s, **80 TURN allocations** |
| turn, 500 relay ports | 50 pods, 500 sessions | 129 up, 371 failed, allocations saturated at 500 |
| turn, 3000 relay ports | 50 pods, 500 sessions | 421 up, 79 failed, p50 3.11 s, p95 3.30 s, max 4.26 s, relay peak **128 MB RSS, 3233 fds, 3294 goroutines, 1778 allocations** |

## The wall that came first: relay ports

**A relayed session costs four TURN allocations.** Each end of a tunnel (the browser, the pod)
asks TURN for a relay address over UDP and over TCP: 2 × 2. The 20-session run shows it exactly
(20 sessions → 80 allocations), and the 500-session run shows what it means: with 500 relay ports
the relay stopped at 129 tunnels, the allocations pinned at 500, the rest failing with a channel
that never opened. Memory, file descriptors and goroutines were nowhere near their ceiling.

So the capacity of a relay is `relay ports / 4` simultaneous relayed tunnels:

- the hosted relay today, 840 ports: **about 210 tunnels**;
- 500 simultaneous sessions need a range of **at least 2000 ports** (`-turn-ports 49160-51999`).

Two ways to lift it, in order of what they cost:

1. **Widen the range.** `-turn-ports` is a flag; the firewall must carry the range. Nothing else
   changes, and the ports are the cheapest resource a VPS has.
2. **Halve the allocations per session.** The TCP transport of TURN is only a fallback for a
   client whose UDP is blocked; refusing it on the relay's ICE URLs would leave 2 allocations per
   session, doubling the tunnels for the same range.

## What the relay was doing at 500 sessions

128 MB resident (of the 1 GB ceiling), 3233 file descriptors (of 524288 allowed), 3294
goroutines, no restart, and nothing in its log. The 79 failures of that run came from the load
generator side: 500 pion peer connections, ~3600 sockets and the TURN refresh of them all on one
laptop, whose pod links dropped (`min pods online 0` in the samples). A run that goes past that
needs the generator on its own machine — the relay was not the limit there.

## Reading a run

`relayload -json` answers the samples of `/_relay/status` taken during the run: `load.pods`,
`load.browsers`, `load.identity` (the links it carries), `load.turn` (allocations open),
`load.turnPorts` (the size of the range it was given — the wall), `load.rss`, `load.fds`,
`load.goroutines`. The report prints the last sample; `-json` keeps the whole curve.
