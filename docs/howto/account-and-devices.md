# Account and devices

Replace the access token of each pod with an **account** shared by your browsers and pods. It needs a relay ([hosted, or your own](self-host-relay.md)) to invite a phone; the rest also works on the local network alone ([LAN pods](lan-pods.md)).

## Pairing: an account is a key

Once the relay is up, the token can be replaced by an **account**: a 24-word recovery phrase, shown once and never stored anywhere.

```sh
web-ide-pod pair create    # the phrase, then three words asked back
web-ide-pod pair status    # device, account, certificate, fingerprints
web-ide-pod pair join      # a code, a QR and the 4-word fingerprint of its key
```

The pod stores only its own key (`~/.web-ide/device.key`, 0600) and its certificate (`identity.json`). Each browser makes its own key and gets a certificate signed by the account key; the pod pins the account key and checks every peer itself, so a relay cannot impersonate a device. The phrase is typed on a computer, or a phone scans the invite QR shown by an admin device (Settings > Remote access > Devices); the 4-word fingerprint read on both sides is what you compare before approving.

Recent browsers only: WebCrypto Ed25519 and the WebAuthn PRF are required, with no fallback. A passkey is tied to an origin, so `localhost`, the tailnet address and the relay are separate devices, each enrolled once with the phrase — and WebAuthn refuses an IP address, so open the pod on `localhost` to unlock a device (the panel gives the link). Revoking a device from the panel signs a new list: the relay stores it and pushes it to the online pods, which refuse the revoked device at its next connection.

## The settings follow the account

The pods of one account share their settings through the relay: a theme, a shortcut or a colour changed on one pod reaches the other ones. The relay only carries ciphertext — it stores one encrypted blob per account and never sees a value. Each setting merges on its own, so two devices changing different things do not disturb each other; you are asked only when the **same** setting changed on two pods, and the panel shows both values and which device made them (Settings > Remote access).

The layout stays per device: the code size, the interface size and the panel sizes belong to the browser, so a phone keeps its layout on every pod of the account. A device that has nothing of its own uses the layout saved with *Use as default for new devices* (Settings > Fonts).

The **vault** is separate and off by default: the API keys of the model servers and the remembered database passwords. Switching it on sends them to the other pods, switching it off deletes them from the relay. After a revocation, the Devices panel asks to renew the API keys shared with the other devices.
