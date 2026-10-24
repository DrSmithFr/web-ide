# `lib`

The Go code the pod ([`pod/`](../pod/README.md)) and the relay ([`relay/`](../relay/README.md)) share. Their descriptions are in [the pod's packages](../pod/packages.md).

- `identity`: the account from a BIP39 phrase, device certificates, revocation lists; the pod identity in the data folder; the JS module served to the browser (`lib/identity/web/identity.js`) and the test vectors shared with it (`vectors.json`, checked by `scripts/identity-vectors.mjs`); `WithPeer` / `PeerOf`, the certificate of the device a request comes from, set by the transport that checked it.
- `tunnel`: message connections with framing and back-pressure, the channel-binding handshake.
- `i18n`: the translation of the messages sent to the page (`i18n.Errorf`, `i18n.T`) and the catalogs of the core; `i18n.Add` merges a built-in plugin's (`plugins/<id>/i18n`, from its `init`). Its tests check that every message of `pod/`, `relay/`, `lib/` and `plugins/` has a translation, in its plugin's catalog or the core's, and that no key is in both.

## Invariants

- `lib/` imports nothing of `pod/` nor `relay/`.

## Tests

`go test ./lib/...` from the root.
