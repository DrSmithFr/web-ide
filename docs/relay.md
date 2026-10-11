# Relay

Reach every pod of the user from any browser, a phone included, with nothing to install, no domain to buy and no network to configure: open `https://antennae.space`, pair once, see the projects of all the machines that are on. The relay only introduces the browser and the pods to each other; the traffic between them is end-to-end encrypted.

The IDE stays free and works without the relay (`127.0.0.1`, LAN, Tailscale). The relay is open source (MIT, like the rest) and self-hostable with every feature; the hosted one at `antennae.space` sells comfort only, close to a recurring donation with perks.

## Goals and non-goals

- **Simpler** than a VPN: no client on the phone (a PWA at most), no account form, no domain, no port to open.
- **Private**: the relay never sees code, terminals, settings or project names in clear; a compromised relay cannot impersonate a device.
- **Optional**: if the relay is down, local, LAN and Tailscale access keep working.
- Not a VPN: a tunnel for the IDE only (its protocol and HTTP previews), not a network between machines.
- Not hosted machines: the user brings the machines.

## Pieces

```
                     ┌──────── relay (web-ide-pod relay) ────────┐
                     │ signaling · STUN/TURN · encrypted blobs   │
                     │ bootstrap page + Service Worker · push    │
                     └──▲──────────────────▲──────────────────▲──┘
            signaling   │                  │ outbound WS      │ outbound WS
                        │                  │                  │
 browser (phone) ═══ WebRTC DataChannels ═══ gateway pod ─── LAN ─── pod B
                        (DTLS, end to end)        ╚═══ WebRTC ═══ pod C (other site)
```

- **Relay**: a subcommand of the pod binary, `web-ide-pod relay`, with its SQLite base. Signaling (WebSocket), STUN and TURN (Pion TURN, embedded), storage of encrypted blobs, the bootstrap page, Web Push. Hosted at `antennae.space`; anyone can run their own and point their pods to it.
- **Pod**: keeps one outbound WebSocket to its relay (no port to open), answers WebRTC offers with Pion, and serves its usual protocol over DataChannels.
- **Browser**: loads a small bootstrap page and a Service Worker from the relay; everything else comes from the user's own pods through the tunnel.

## Transport

- WebRTC with ICE: direct paths first (host, then STUN hole punching), TURN relay when they fail (symmetric NAT, mobile CGNAT, UDP blocked). TURN is also offered over TCP/TLS 443 for corporate networks.
- The DTLS fingerprints of both sides are bound to the proofs signed inside the DataChannel (see *Identity*): a relay that swaps the offer changes a fingerprint, so a signature fails and the connection is refused. No extra signaling format is needed for that.
- TURN credentials are short-lived (HMAC with an expiry) and given by the relay to authenticated devices only, with a quota per account: never an open relay.
- **DataChannels**: one reliable, ordered channel carries the current JSON protocol unchanged (requests, responses, events); each terminal and each file transfer gets a channel of its own, so a big upload never delays a key stroke.
- The protocol is versioned: every channel opens with a `hello { protocol, version }`; a pod too old or too new for its gateway is listed with *Update this pod* instead of its projects.

## Where the code of the page comes from

- On a machine with a pod, the user keeps opening `http://127.0.0.1:4433`: the page comes from the local pod, which acts as the gateway to the others.
- Elsewhere, `antennae.space` serves only a **bootstrap**: a small, auditable page and a Service Worker. Once the tunnel is up, the Service Worker fetches the application from the pod over the DataChannel. When a project opens, its front end comes **from the pod that holds the project**, so a front end always speaks to its own pod, whatever the versions of the others (switching to a project of another machine reloads the page).
- The WebSocket client of the front end goes through a transport interface: a WebSocket on `127.0.0.1`, the DataChannel through the relay.
- App previews (`localhost:3000` of a pod) go through the Service Worker and a DataChannel as well.
- Later: a signed, versioned bootstrap so that even the hosted relay cannot change it silently.

## Identity

No e-mail, no password. An account is a key. **Done** (`pod/internal/identity`, `web-ide-pod pair`, Settings > Remote access > Devices): see *Milestone 2* below.

- **Recovery phrase**: 24 BIP39 words, made on the first device and shown **once**, with a check that the user wrote it down (like a hardware wallet). It is never stored by the IDE, on disk or in the relay: storing it would turn the theft of one machine into the theft of the account. The user may keep it in a password manager.
- From the phrase (HKDF): the **account key** (Ed25519, its public half is the account id) and the **sync key seed**.
- **Device keys**: each browser and each pod makes its own key pair and gets a **certificate** signed by the account key: device public key, name, kind (browser, pod), date, `scope` (always `all` for now: the field exists so that read-only or per-project devices need no new format). A pod stores its key in `~/.web-ide/device.key` (0600).
- **Admin devices** hold the account key (encrypted at rest; in a browser, unlocked by a passkey through the WebAuthn PRF extension) and can add and revoke devices. The others hold only their own key.
- **Passkeys** unlock the keys of the current browser day to day. A passkey is tied to a domain: each origin (`antennae.space`, a self-hosted relay, `127.0.0.1`) has its own.
- Every pod pins the account public key when it is paired and checks the certificate of every peer itself: it never trusts the relay to say who is who.
- **Recent browsers only** (decided): WebCrypto Ed25519 and the WebAuthn PRF extension are required, there is no fallback for older ones — the bootstrap page names what is missing. Chrome, Edge, Firefox and Safari of 2025 and later.
- **One origin per relay** (`antennae.space`, or the self-hosted one): accounts are isolated by keys, not by origins. Per-account subdomains would need wildcard DNS and TLS and hurt self-hosting, and a passkey is tied to an origin, so one passkey per origin per account (the user handle is the account id). The app runs on the relay origin, served only by pods paired with the account (trusted code); several accounts in one browser profile share that origin.
- **Passkeys and IndexedDB are per origin**: the tailnet address, `localhost` and the relay are separate devices, each enrolled once with the phrase. WebAuthn refuses an IP address as a relying-party id, so no passkey can be made on `127.0.0.1`: the Devices panel lives on the relay origin and on `localhost`, and gives a one-click link to `localhost` when opened on `127.0.0.1`.

### Pairing

- **First pod**: `web-ide-pod pair create` makes the account (phrase shown once, three words asked back) or `pair restore` takes the phrase typed; `pair status` shows the device, the account and the fingerprints.
- **Another pod**: `web-ide-pod pair join` prints a code, a terminal QR and the 4-word fingerprint of its key, and waits for an admin; the phrase is typed instead when no admin is around.
- **A phone or a tablet**: scan the QR shown by an admin device (like WhatsApp Web) — the invite link `#join=<code>` opens the bootstrap page, which shows its own fingerprint and waits for the approval.
- **A computer**: the QR, or the phrase, whichever is easier.
- The code is 8 base32 characters, single use, 10 minutes; the 4-word fingerprint read on both sides is the guard against a relay that swaps the key. A code that already holds a key refuses another one.

### Revocation

An admin device signs a revocation list, stored by the relay and checked by every pod. Revoking a device also rotates the sync key (next epoch, data encrypted again, new key wrapped for each remaining device) and suggests renewing the API keys of the vault, since a key that leaked stays leaked.

The relay stores the list only when its signature verifies against the account it names and its `seq` is higher than the stored one, and pushes it to the online devices of the account; a relay that replays an older list changes nothing. The panel (Settings > Remote access > Devices) lists the devices of the account with their kind, date and fingerprint, approves a request by its code after comparing the fingerprint, shows the invite QR, and revokes.

### Formats

- **Phrase**: 24 BIP39 words (256-bit entropy, embedded English wordlist, checksum). Seed = BIP39 PBKDF2 seed; HKDF-SHA256 (32-byte zero salt) gives the account Ed25519 seed (`info "web-ide account v1"`), the sync seed (`"web-ide sync v1"`) and the device seed (`"web-ide device v1"`). The account id is the base64url of the account public key.
- **Certificate**: canonical JSON `{v:1, account, key, name, kind: browser|pod, created, scope: "all"}` + the Ed25519 signature of the account key, base64url. Fixed field order, no spaces: the browser and the pod build the same bytes, checked by the shared test vectors (`pod/internal/identity/vectors.json`, run by `make test` in Go and in JS).
- **Revocation list**: `{account, seq, epoch, revoked: [keys], created}` signed by the account key; the revoked keys are sorted so both sides build the same bytes; a higher `seq` wins.
- **Fingerprint**: the 4-word reading of a key id (44 bits of its SHA-256, 11 bits per word), shown on both sides of a pairing.

### Handshake v2 (cert mode)

The same shape as the M1 token handshake, with Ed25519 signatures instead of the HMAC of the token, each side signing `role|fpBrowser|fpPod|nonceBrowser|noncePod` with its device key:

```
browser → $hello { protocol, version: "2", nonceB, cert }
pod     → $hello { …, nonceP, cert, sig = Ed25519(podKey, "pod"|fpB|fpP|nB|nP) }
browser → $auth  { sig = Ed25519(browserKey, "browser"|fpB|fpP|nB|nP) }
pod     → $ready
```

Each side checks the other's certificate against the **pinned** account key, the scope `all` and the revocation list. A swapped offer changes a fingerprint, so a signature fails: the connection is refused. The errors are `unpaired`, `cert` (a paired pod refuses the token mode), `foreign`, `revoked`, `proof`; failed proofs are still rate-limited and the handshake keeps its 10 s deadline.

The **token mode** of M1 (`#t=`) stays for unpaired pods, and `127.0.0.1` is unchanged (token cookie). A paired pod refuses the token mode through the relay.

## Multi-pod

- **Unified home page**: one *Projects* block per pod, online or not.
- Each pod publishes its project list (names, paths, icons, branches) **encrypted** with the sync key; the relay caches the blobs, so the home page shows at once, offline pods included (greyed). The connection to a pod is made when one of its projects opens.
- **Sites**: pods that share a public address and see each other on the LAN form a site. The relay elects one **gateway** per site (the pod online for the longest; a pod may be marked *preferred*) and moves the role to another pod when it goes offline.
- The browser opens one tunnel to a gateway; the gateway reaches the pods of its site directly over the LAN and the pods of other sites pod to pod over WebRTC. Every hop checks the certificates: a pod not paired with the account is invisible, even on the same LAN.
- A pod may set its LAN address by hand when discovery gets it wrong.
- **Updates**: the home page shows outdated pods; the gateway asks a pod to update itself (with the keeper, #19, an update keeps terminals and streams).
- **Wake-on-LAN**: a pod records the MAC addresses of its machine; an online pod of the same site sends the magic packet (a machine cannot be woken from the internet without a device awake on its LAN). **Shutdown** is off by default; `sudo web-ide-pod allow-shutdown` installs the rule that lets the pod power the machine off.

## Settings sync

- Today the settings live in each pod (`settings.json`), shared by its browsers. With an account, the pods sync them through the relay, encrypted with the sync key.
- Merge field by field; the user is asked only when the same setting changed on two devices.
- **Per device**: the layout and the sizes (fonts, panels). A device without its own uses a *fallback layout* the user saves for new devices.
- **Vault** (opt-in, separate from the rest): API keys of the model providers and remembered passwords (`secrets.json`).

## Push notifications

Web Push from the relay (an installed PWA on iOS): the agent finished, tests failed, a feedback arrived. The pod encrypts the payload for the subscription of the browser (RFC 8291), so the relay forwards ciphertext.

## Share links

A public HTTPS link to a port of a pod, to show a work in progress to a friend: `https://<random>.share.antennae.space`.

- The relay ends TLS with its own Let's Encrypt wildcard certificate and forwards to the pod, which calls the local server in HTTP, or in HTTPS accepting a self-signed certificate. Unlike the tunnel, the relay sees this traffic in clear: the user is told.
- Short-lived (24 h by default, 7 days at most), a secret in the URL, a password unless the visitor's address is allowed, a bandwidth quota, revoked from the IDE at any time.
- Before opening them on the hosted relay: legal notice, terms, privacy policy, an abuse contact and a way to cut a link at once (the relay is a host under the LCEN and the DSA). A self-hosted relay leaves this to whoever runs it; the documentation says so.

## Shared sessions (later)

Code and review together:

- A link with an ephemeral guest key; the guest picks a nickname, no account. The host accepts or refuses, chooses read-only or editing, and can switch it during the session. The terminal is a separate permission, asked each time.
- Several carets in the editor; the pod is the authority: browsers send their changes with the version they saw, the pod rebases and broadcasts them (the model of `@codemirror/collab`, simpler than a CRDT).
- Voice over WebRTC, on the connection that already exists.
- Guests run on another origin than the application, so code served to a guest can never read the keys of the account.

## Hosted service and pricing

- **Free**: relay, pairing, multi-pod, settings sync, TURN within a monthly quota.
- **Supporter** (about 4 € a month): TURN without quota (fair use), push notifications, share links, vault sync. Later **Team**: shared sessions and voice.
- Paid through GitHub Sponsors: the account links a GitHub login (OAuth) and the relay checks the sponsorship. No e-mail is ever asked by the IDE. Paddle may come later for people without GitHub.
- A self-hosted relay has every feature without limits.
- One VPS in the EU to start; the relay stays a single binary with SQLite until load says otherwise.
- **Counting**: the relay keeps anonymous counters only (accounts seen in 30 days, pods online); no telemetry in the pod or the page.

## Milestones

1. **Done** (`web-ide-pod relay`, `-relay`): the relay (signaling, STUN, TURN) and the pod and the browser over WebRTC, the pod token as access control. See *Milestone 1* below.
2. **Done** (`web-ide-pod pair`, `pod/internal/identity`, Settings > Remote access > Devices): phrase, device certificates, pairing, QR, passkeys, revocation. See *Milestone 2* below.
3. Multi-pod: gateway, sites, pod to pod, encrypted project lists, versions and updates.
4. Settings sync, per-device layout, vault.
5. Hosted relay at `antennae.space`, GitHub Sponsors, quotas, counters.
6. Push notifications.
7. Wake-on-LAN and shutdown.
8. Share links.
9. Shared sessions and voice.

The full IDE on a phone goes on alongside, from the phone layout (#22).

## Milestone 1 (done)

Three pieces, one binary:

- **`web-ide-pod relay`** (`pod/internal/relay`): signaling over WebSocket, STUN and TURN (Pion TURN, UDP and TCP, and TURN over TLS on 443 shared with HTTPS through an ALPN demux, `stun.turn`), the bootstrap page and the Service Worker, a SQLite base (`pods`, `usage`, `PRAGMA user_version`). Short-lived HMAC-SHA1 TURN credentials (12 h), given only to a registered, online pod.
- **The pod** (`pod/internal/relayclient`): one outbound WebSocket to its relay (`-relay` flag or `relay` in `config.json`), pod id and secret in `relay.json` (0600) of the data folder, backoff reconnect, one PeerConnection per offer, trickle ICE with the ICE servers announced by the relay. The remote link is printed at startup and shown in Settings > Remote access with its state.
- **The browser**: the bootstrap page and the Service Worker come from the relay; the app, its assets and the previews come from the pod through the tunnel. The front end talks to a `Transport` (`web/src/pod/transport.ts`): a WebSocket on `127.0.0.1`, or a MessagePort to the bootstrap page. Each project window gets its own connection.

**Channels** (`pod/internal/tunnel`): the JSON protocol is unchanged on the `rpc` channel; `term:<id>` carries the input and output of one terminal, `xfer:<n>` one big message, `http:<n>` one HTTP request of the Service Worker. Every message is framed into 16 KB fragments (1-byte header: 1 = more, 0 = last) so a 2 MB transfer stays under the DataChannel cap, with back-pressure.

**Access control in this step** is the pod token, and the relay never sees it. The token is proved inside the DataChannel and bound to both DTLS fingerprints (channel binding), so a relay that swaps the SDP cannot MITM:

```
browser → $hello { protocol, version, nonceB }
pod     → $hello { protocol, version, nonceP, proof = HMAC(token, "pod"|fpB|fpP|nonceB|nonceP) }
browser → $auth  { proof = HMAC(token, "browser"|fpB|fpP|nonceB|nonceP) }
pod     → $ready
```

The fingerprints are read from the `a=fingerprint` line of each SDP; failed proofs are rate-limited per pod, and the handshake has a 10 s deadline. A protocol mismatch answers `$hello { error: "version" }` and the bootstrap page shows *Update this pod*. Identity (phrase, device certificates, pairing) came with milestone 2: the token mode stays for unpaired pods, and a paired pod refuses it.

What is not in this step: multi-pod and gateways, encrypted blob storage, push, quotas. A pod with no relay, or whose relay is down, keeps working on `127.0.0.1` (the e2e `relay` suite checks both, with TURN forced and the pair asserted as a relay one).

## Milestone 2 (done)

The identity of *Identity* above, in three places:

- **`pod/internal/identity`**: BIP39 (generate, validate, checksum), the derivation, the certificates, the revocation lists, the verification of a peer certificate. The pod identity in the data folder: `device.key` (0600) and `identity.json` (its own certificate, the pinned account key, the last revocation list). The wordlist and the test vectors are embedded; `scripts/identity-vectors.mjs` runs the same vectors in JS, so a signature made in the browser verifies in the pod.
- **`pod/internal/identity/web/identity.js`**: the same formats in JS, imported at runtime by the bootstrap page and by the app (served under `/_relay/identity.js` by the relay and by the pod — one copy, so the two sides cannot disagree on the bytes a signature covers). The device key is made with WebCrypto Ed25519 and stored wrapped by an AES-GCM key derived from the passkey (WebAuthn PRF), unwrapped as a non-extractable key for the session.
- **`web-ide-pod pair`**: `create`, `restore`, `join`, `status` (see *Pairing*). The relay keeps the identity tables (`accounts`, `devices`, `pairings`) to list the devices and route pairing; it never holds a private key or the phrase, so it can check a signature against an account id but never make one — only a device holding the account key can approve a pairing or push a revocation list.
- **Settings > Remote access > Devices**: the admin panel, on the relay origin and on `localhost` (see *Identity*). The pod only carries the frames of the pairing link.

The identity works **without a relay**: `pair create`, `restore` and `status` run offline, `identity.json` and the certificate checks do not depend on `relayclient`, and the handshake v2 proof takes the two peer fingerprints as plain inputs — so LAN discovery (#50) can feed it the TLS certificate fingerprints of a direct WebSocket instead of the DTLS ones.

The e2e `identity` suite pairs a pod with the CLI and two browsers (a virtual authenticator answering the WebAuthn PRF), and checks that an unpaired browser and a revoked one are refused; the swapped-offer refusal is a Go integration test with Pion peers and a relay that rewrites the SDP.

## Open points

- The name of the product, which `antennae.space` may become.
- ~~One origin for the application (`antennae.space`) or one per account~~ — decided with milestone 2: **one origin per relay**, accounts isolated by keys (see *Identity*).
- The TURN quota of the free tier, once real usage is measured.
- Wrapping the sync key for each device and re-encrypting the data on a revocation: nothing is synced yet, this waits for milestone 4.
