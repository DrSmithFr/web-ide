# Self-hosting the relay

One binary, one machine, one domain: `web-ide-pod relay` is the whole relay — signaling, STUN and
TURN, the bootstrap page and its Service Worker, the identity tables, Web Push, and its SQLite
base. A self-hosted relay has **every feature of the hosted one, without limits** (`-quota` unset),
and your pods keep working whatever relay they were given: nothing about the machine is stored in a
pod or in a passkey, only the URL of the relay.

## What you need

- A machine with a public address (IPv4, and IPv6 if you publish an AAAA), ports 80, 443, 3478
  open, and a range of UDP ports for the TURN relay sockets.
- A domain (or a subdomain) pointing at it: an `A` record, an `AAAA` one if you serve IPv6, and a
  `CAA` allowing `letsencrypt.org`. Nothing else: the app lives on that origin, and the passkeys of
  the accounts are bound to it — **never move the app to another origin**.
- TLS is taken care of by the binary (`-domain`): Let's Encrypt through `autocert`, with the
  TLS-ALPN-01 check served on 443 itself, the cache under `<data>/acme`. No certbot, no separate
  web server, no renewal job.

## Run it

```sh
web-ide-pod relay -data /var/lib/antennae \
  -addr 0.0.0.0:4430 -tls-port 443 -domain relay.example.org \
  -turn-ports 49160-49999 -push-subject mailto:you@example.org
```

| Flag | What it does |
| --- | --- |
| `-data` | where `relay.db`, the VAPID key and the ACME cache live (default `~/.web-ide-relay`) |
| `-addr` | the plain HTTP listener (the page, the signaling, the Service Worker) |
| `-tls-port` | the TLS port: HTTPS, TURN over TLS (RFC 8489) and the ACME check, demuxed by ALPN (a client without ALPN — Pion — is read after the handshake: STUN goes to TURN) |
| `-domain` | serve this domain: the certificate comes from Let's Encrypt (`-acme-staging` for the test CA) |
| `-cert`, `-key` | a certificate of your own instead (reloaded when the files change, no restart) |
| `-turn-ip`, `-turn-ip6` | the addresses named in the ICE URLs (auto-detected when unset) |
| `-turn-ports` | the range the TURN relay sockets are bound in, e.g. `49160-49999` (any free port when unset) |
| `-quota` | bytes one account may move through TURN per month (`10GB`); **unset: no limit** |
| `-push-subject` | the contact of the Web Push claims (`mailto:` or `https:`) |
| `-github-env` | a `KEY=VALUE` file (0600) with `GITHUB_CLIENT_ID`, `GITHUB_CLIENT_SECRET`, `GITHUB_TOKEN`, `GITHUB_WEBHOOK_SECRET`: without them the relay has no GitHub link and no supporters |

Without `-domain` and without `-cert/-key`, the relay makes a **self-signed** certificate on first
run: fine on a tailnet, a browser will complain.

The ICE URLs name the domain alone when the relay has one — `turn:relay.example.org:3478` over UDP
and `turns:relay.example.org:443?transport=tcp` — so a client behind a restrictive network reaches
TURN on 443. Each TURN URL costs a relay port on each end of a tunnel (see [load.md](load.md)):
the addresses and TCP 3478 are only named when there is no domain.

## systemd

```ini
# /etc/systemd/system/relay.service
[Unit]
Description=Web IDE relay
After=network-online.target
Wants=network-online.target

[Service]
ExecStart=/usr/local/bin/web-ide-pod -data /var/lib/relay -addr 0.0.0.0:4430 -tls-port 443 \
  -domain relay.example.org -turn-ports 49160-49999 -push-subject mailto:you@example.org relay
User=relay
Group=relay
Restart=always
RestartSec=3
CapabilityBoundingSet=CAP_NET_BIND_SERVICE
AmbientCapabilities=CAP_NET_BIND_SERVICE
NoNewPrivileges=true
ProtectSystem=strict
ProtectHome=true
PrivateTmp=true

[Install]
WantedBy=multi-user.target
```

`scripts/deploy-relay.sh --setup --host <user@host>` writes exactly this (with the values of the
hosted relay), plus the firewall rules, unattended upgrades and the nightly backup timer, and
running it again changes nothing.

## Firewall

```
22, 80, 443 tcp          # ssh, the redirect, the page + TURN over TLS + ACME
3478 tcp, 3478 udp       # STUN and TURN control
49160-49999 udp          # the TURN relay sockets (-turn-ports)
```

## Backups

The base holds the accounts, the devices, the pairings, the pods, the sync blobs, the TURN month
and the GitHub links. `sqlite3 relay.db .backup` (or the timer of `--setup`) is enough; the ACME
cache is worth keeping too, so a rebuilt machine does not spend the rate limits again.
`scripts/relay-watch.sh --pull` copies them off the machine and keeps 30 days.

## Pointing your pods at it

Settings > Remote access of a pod shows the relay field, pre-filled with `wss://antennae.space`:
type your relay there and connect, or start the pod with `-relay wss://relay.example.org` (it is
saved in `config.json`). Turning remote access off ends the link without touching anything else.

**Moving to another relay** (including from the hosted one to your own): the pods and browsers keep
their keys, but the *sync* key wraps live in the base of the old relay — a fresh relay logs
`relayclient: sync: no wrap` until an admin device of the account wraps them again (Settings >
Remote access > Sync), or until the base of the old relay is restored.

## What a self-hosted relay does not do

Nothing is missing: no quota, no GitHub link needed, no supporter tier. The relay never holds a
private key, the phrase, the pod token, the code or the project names in clear — self-hosting
changes who holds the base, not what the relay can see.
