# Self-hosting the relay

Run your own relay on a machine with a domain, so your pods and browsers reach each other from anywhere.

One binary, one machine, one domain: `web-ide-pod relay` is the whole relay — signaling, STUN and
TURN, the bootstrap page and its Service Worker, the identity tables, Web Push, and its SQLite
base. A self-hosted relay has **every feature of the hosted one, without limits** (`-quota` unset),
and your pods keep working whatever relay they were given: nothing about the machine is stored in a
pod or in a passkey, only the URL of the relay.

## Prerequisites

- A machine with a public address (IPv4, and IPv6 if you publish an AAAA), Linux with systemd, and root on it.
- A domain or subdomain pointing at it: an `A` record, an `AAAA` one if you serve IPv6, and a `CAA` allowing `letsencrypt.org`.
- Ports 80, 443 and 3478 open, and a range of UDP ports for the TURN sockets (see *Firewall*).
- The `web-ide-pod` binary of the [release](https://github.com/antennae-space/web-ide/releases/latest) for the machine (`web-ide-pod-linux-amd64.tar.gz` or `-arm64`), or one built [from the sources](../install/from-sources.md).

## Steps

1. Point the domain at the machine and open the ports of *Firewall*.
2. Install the binary in `/usr/local/bin/web-ide-pod`, and create a system user for it: `sudo useradd --system --no-create-home --shell /usr/sbin/nologin relay` (the unit's `StateDirectory=relay` creates `/var/lib/relay`, the data folder).
3. Write the unit of *systemd* below with your domain and contact (`-domain`, `-push-subject`), then:

   ```sh
   sudo systemctl daemon-reload && sudo systemctl enable --now relay
   ```

   The certificate is issued by Let's Encrypt on the first start (no certbot, no renewal job).
4. On each pod, Settings > Remote access: type `wss://relay.example.org` and connect, or start the pod with `-relay wss://relay.example.org` (saved in `config.json`). The pod prints its remote link:

   ```
   remote : https://relay.example.org/#pod=<id>&t=<token>
   ```

5. Open that link from another network, then [create the account](account-and-devices.md) to pair your other browsers, phones and pods.

To try the relay on one machine first, see *Quick start on one machine*; `scripts/deploy-relay.sh --setup --host <user@host>` does steps 2 and 3 and more (below).

## Check

- `systemctl status relay` is active, and `https://relay.example.org` loads a page with a valid certificate.
- A pod connected to it shows its remote link in Settings > Remote access, and the link opens the app from another network.
- `journalctl -u relay` shows no certificate or bind error.

## Troubleshooting

| Symptom | Fix |
| --- | --- |
| The browser complains about the certificate | Without `-domain` or `-cert/-key` the relay makes a self-signed one. Give it the domain, with port 443 reachable (the ACME check is served on 443 itself; `-acme-staging` tests with the test CA). |
| `-tls-port` cannot bind | A port below 1024 needs root or `CAP_NET_BIND_SERVICE` (the unit has it); TURN over TLS is simply off when it cannot be bound. |
| A client behind a strict network does not connect | Check 443 and the UDP range of `-turn-ports`; keep that range below the ephemeral range of the kernel. |
| `relayclient: sync: no wrap` in the log of a pod | After a move to another relay, an admin device wraps the sync keys again (Settings > Remote access > Sync). |
| The pod cannot be woken or powered off | The relay is older than the pods: update it with them (see *Pointing your pods at it*). |

## Reference

### Quick start on one machine

The relay is an optional piece: it only introduces the browser and the pod, the traffic between them is encrypted and the relay never sees your token. Run your own:

```sh
web-ide-pod relay            # signaling + STUN/TURN + bootstrap page, SQLite in ~/.web-ide-relay
```

Its own data folder (`~/.web-ide-relay`, `-data` only when given explicitly), so it does not share the pod's. Options: `-addr` (`127.0.0.1:4430`), `-turn-port` (3478), `-turn-ip` (the IP announced to clients), `-tls-port` (443, shared with HTTPS and TURN over TLS; a port below 1024 needs root, and TURN over TLS is simply off when it cannot be bound), `-push-subject` (the contact given to the push services, `mailto:` or `https:`; the VAPID key of the notifications is made once in `vapid.json`). Open the link the pod prints — `#pod=<id>&t=<token>`, the token stays in the hash and never reaches the relay.

Then start a pod with `-relay http://127.0.0.1:4430` (or `relay` in `config.json`). The pod prints its remote link, also shown in Settings > Remote access:

```
remote : http://127.0.0.1:4430/#pod=<id>&t=<token>
```

Open that link from any browser on another network: a small bootstrap page and a Service Worker come from the relay, the app itself comes from your pod through the tunnel. The pod keeps one outbound WebSocket to its relay (no port to open on the machine) and reconnects by itself; the token is proved inside the DataChannel, bound to the fingerprints of both sides. If the relay is down, `127.0.0.1` keeps working as before. TURN credentials are short-lived and given only to a registered, online pod. See [relay.md](../design/relay.md).

### What you need

- A machine with a public address (IPv4, and IPv6 if you publish an AAAA), ports 80, 443, 3478
  open, and a range of UDP ports for the TURN relay sockets.
- A domain (or a subdomain) pointing at it: an `A` record, an `AAAA` one if you serve IPv6, and a
  `CAA` allowing `letsencrypt.org`. Nothing else: the app lives on that origin, and the passkeys of
  the accounts are bound to it — **never move the app to another origin**.
- TLS is taken care of by the binary (`-domain`): Let's Encrypt through `autocert`, with the
  TLS-ALPN-01 check served on 443 itself, the cache under `<data>/acme`. No certbot, no separate
  web server, no renewal job.

### Run it

```sh
web-ide-pod relay -data /var/lib/antennae \
  -addr 0.0.0.0:4430 -tls-port 443 -domain relay.example.org \
  -turn-ports 20000-20999 -push-subject mailto:you@example.org
```

| Flag | What it does |
| --- | --- |
| `-data` | where `relay.db`, the VAPID key and the ACME cache live (default `~/.web-ide-relay`) |
| `-addr` | the plain HTTP listener (the page, the signaling, the Service Worker) |
| `-tls-port` | the TLS port: HTTPS, TURN over TLS (RFC 8489) and the ACME check, demuxed by ALPN (a client without ALPN — Pion — is read after the handshake: STUN goes to TURN) |
| `-domain` | serve this domain: the certificate comes from Let's Encrypt (`-acme-staging` for the test CA) |
| `-cert`, `-key` | a certificate of your own instead (reloaded when the files change, no restart) |
| `-turn-ip`, `-turn-ip6` | the addresses named in the ICE URLs (auto-detected when unset) |
| `-turn-ports` | the range the TURN relay sockets are bound in, e.g. `20000-20999` (any free port when unset). Keep it below the ephemeral range of the kernel (`32768-60999` on Linux), whose outgoing connections would take its ports. A relayed session costs four of them |
| `-quota` | bytes one account may move through TURN per month (`10GB`); **unset: no limit** |
| `-quota-backer` | the quota of a Backer, a $2 monthly sponsor (`20GB`); unset: twice `-quota`, and no limit without `-quota` |
| `-push-subject` | the contact of the Web Push claims (`mailto:` or `https:`) |
| `-github-env` | a `KEY=VALUE` file (0640, `root:` the user of the service) with `WEBIDE_GITHUB_CLIENT`, `WEBIDE_GITHUB_SECRET` (the OAuth App), `WEBIDE_GITHUB_TOKEN` (reads the sponsor list) and `WEBIDE_GITHUB_WEBHOOK` (the secret of the Sponsors webhook): without them the relay has no GitHub link and no sponsors, which a self-hosted relay does not need (setup: relay.md *Hosted service and pricing*) |

Without `-domain` and without `-cert/-key`, the relay makes a **self-signed** certificate on first
run: fine on a tailnet, a browser will complain.

The ICE URLs name the domain alone when the relay has one — `turn:relay.example.org:3478` over UDP
and `turns:relay.example.org:443?transport=tcp` — so a client behind a restrictive network reaches
TURN on 443. Each TURN URL costs a relay port on each end of a tunnel (see [load.md](../design/load.md)):
the addresses and TCP 3478 are only named when there is no domain.

### systemd

```ini
# /etc/systemd/system/relay.service
[Unit]
Description=Web IDE relay
After=network-online.target
Wants=network-online.target

[Service]
ExecStart=/usr/local/bin/web-ide-pod -data /var/lib/relay -addr 0.0.0.0:4430 -tls-port 443 \
  -domain relay.example.org -turn-ports 20000-20999 -push-subject mailto:you@example.org relay
User=relay
Group=relay
StateDirectory=relay
ReadWritePaths=/var/lib/relay
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

### Firewall

```
22, 80, 443 tcp          # ssh, the redirect, the page + TURN over TLS + ACME
3478 tcp, 3478 udp       # STUN and TURN control
20000-20999 udp          # the TURN relay sockets (-turn-ports)
```

### Backups

The base holds the accounts, the devices, the pairings, the pods, the sync blobs, the TURN month
and the GitHub links. `sqlite3 relay.db .backup` (or the timer of `--setup`) is enough; the ACME
cache is worth keeping too, so a rebuilt machine does not spend the rate limits again.
`scripts/relay-watch.sh --pull` copies them off the machine and keeps 30 days.

### Pointing your pods at it

Settings > Remote access of a pod shows the relay field, pre-filled with `wss://antennae.space`:
type your relay there and connect, or start the pod with `-relay wss://relay.example.org` (it is
saved in `config.json`). Turning remote access off ends the link without touching anything else.

**Moving to another relay** (including from the hosted one to your own): the pods and browsers keep
their keys, but the *sync* key wraps live in the base of the old relay — a fresh relay logs
`relayclient: sync: no wrap` until an admin device of the account wraps them again (Settings >
Remote access > Sync), or until the base of the old relay is restored.

**Power** (Wake, Reboot, Shut down from the home page, relay.md *Power*): the relay carries the
signed requests between the pods of an account, and asks every online pod of the account to send a
wake. A relay older than milestone 7 drops these messages: the LAN path still works, the page says
the pod does not answer otherwise. Update the relay with the pods.

### VIPs and supporters

The relay keeps a **VIP list**: people you give the VIP level by hand (no TURN quota, the `VIP`
badge in Settings > Remote access). It needs no GitHub app — an account id is enough — and the
commands write `relay.db` directly, next to the running relay, which feels the change within 30 s:

```
web-ide-pod -data /var/lib/relay relay vip add <github-login|account-id> [-note TEXT] [-days N | -until YYYY-MM-DD]
web-ide-pod -data /var/lib/relay relay vip remove <github-login|account-id>
web-ide-pod -data /var/lib/relay relay vip list
web-ide-pod -data /var/lib/relay relay supporters
```

Run them as the user of the service (`sudo -u relay …`), with the `-data` of the service. An account
id is the 43 characters Settings > Remote access > Members shows for the account; anything else is
a GitHub login, resolved once to its id through the GitHub API (the machine needs the network). A
row added again replaces its note and end; `-until` counts the whole day; an ended row no longer
counts and stays in `vip list` until removed. `relay supporters` lists everyone at Backer level or
above — level, source (`monthly $5`, `one-time $10`, `vip`), end, linked accounts, note. The levels
themselves are in relay.md *Hosted service and pricing*.

**Teams**: a monthly sponsorship of $50 or more gives one seat per $5, and the admin of the team
adds its members in Settings › Remote access › Members. The same by hand, where `<owner>` is the
GitHub login of the sponsor (a person, or an organization) and a member is a login or an account id:

```
web-ide-pod -data /var/lib/relay relay team list
web-ide-pod -data /var/lib/relay relay team add <owner> <member>
web-ide-pod -data /var/lib/relay relay team remove <owner> <member>
web-ide-pod -data /var/lib/relay relay team admin <org> <github-login>
```

`team admin` names the admin of an organization's team when the organization refuses the OAuth app
the relay proves its admins with (the panel says so). Teams need the GitHub link: they come from
the sponsor list.

On the hosted relay: `make vip ARGS="add bob -days 90"`, `make team ARGS="add acme bob"`,
`make supporters` (over SSH, `RELAY_HOST`).

### What a self-hosted relay does not do

Nothing is missing: no quota, no GitHub link needed, no sponsor level to buy. The relay never holds a
private key, the phrase, the pod token, the code or the project names in clear — self-hosting
changes who holds the base, not what the relay can see.

## See also

[Account and devices](account-and-devices.md) · [Pods on the local network](lan-pods.md) · [Notifications on the phone](notifications.md) · [Relay design](../design/relay.md) · [Load](../design/load.md)
