# `lib`

The Go code the pod ([`pod/`](../pod/README.md)) and the relay ([`relay/`](../relay/README.md)) share. Their descriptions are in [the pod's packages](../pod/packages.md).

- `identity`: the account from a BIP39 phrase, device certificates, revocation lists; the pod identity in the data folder; the JS module served to the browser (`lib/identity/web/identity.js`) and the test vectors shared with it (`vectors.json`, checked by `scripts/identity-vectors.mjs`).
- `tunnel`: message connections with framing and back-pressure, the channel-binding handshake.
- `i18n`: the translation of the messages sent to the page (`i18n.Errorf`, `i18n.T`) and their catalogs; its test checks that every message of `pod/`, `relay/` and `lib/` has a translation.

## Invariants

- `lib/` imports nothing of `pod/` nor `relay/`.

## Tests

`go test ./lib/...` from the root.
