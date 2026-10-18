# Account and devices

Replace the access token of each pod with an **account** shared by your browsers and pods, and keep their settings in step.

An account is a key: a 24-word recovery phrase, shown once and never stored anywhere. Browsers and pods are its **members**; a device is a browser. Inviting a phone by QR needs a relay ([hosted, or your own](self-host-relay.md)); the rest also works on the local network alone ([pods on the LAN](lan-pods.md)).

## Prerequisites

- A pod installed ([Linux](../install/linux.md), [macOS](../install/macos.md), [WSL](../install/windows-wsl.md)), open in a browser with its access token.
- A recent browser: WebCrypto Ed25519 and the WebAuthn PRF are required, with no fallback.
- For a phone or a remote browser: a relay, and the pod connected to it (Settings > Remote access).

## Steps

### Create the account

1. On the first pod, in a terminal of the machine:

   ```sh
   web-ide-pod pair create
   ```

   It shows the 24 words, once, then asks three of them back. Write them down: nothing before the check is stored, and the phrase itself never is. Only the account id (its public key) and the certificate of this pod are.

2. `web-ide-pod pair status` shows the pod, the account, the certificate and the fingerprints.

The pod keeps its own key in `~/.web-ide/device.key` (mode 0600) and its certificate in `~/.web-ide/identity.json`.

### Pair a browser

1. Open the pod in the browser (on the relay link, the tailnet address or `localhost`). Each origin is a separate device: a passkey is tied to an origin, so `localhost`, the tailnet address and the relay are enrolled one by one.
2. Type the phrase on a computer to enrol this browser. It makes its own key and gets a certificate signed by the account key.
3. To unlock a browser, open the pod on `localhost`: WebAuthn refuses an IP address, and the panel gives the link.

### Invite a phone by QR

1. On an admin device, Settings > Remote access > Devices: show the invite QR.
2. Scan it with the phone. Both sides show a 4-word fingerprint: compare them, then approve on the admin device.

### Pair another pod

1. On the other machine, with the pod installed:

   ```sh
   web-ide-pod pair join            # a code, a QR and the 4-word fingerprint of its key
   ```

   With a relay, an admin device approves it in the Devices panel. With no relay it waits on the LAN, and you approve it in Devices on `localhost` of a pod of the account. The pod must listen on the LAN: see [pods on the LAN](lan-pods.md).
2. Compare the fingerprint read on both sides before approving.
3. When no admin is around: `web-ide-pod pair join --phrase` types the phrase on the new pod, which keeps its own key. `web-ide-pod pair restore` rebuilds an identity from the phrase typed (an admin device, the key that signs certificates).

### Revoke a member

- From a browser: Devices panel, Revoke. A new signed list is stored by the relay and pushed to the online pods, which refuse the revoked member at its next connection. The panel then asks to renew the API keys shared by the vault.
- From a terminal: `web-ide-pod pair revoke <key>`, the phrase typed. With no relay the list spreads from pod to pod; one made offline goes to the relay as soon as a pod reaches it, so a later revocation never undoes it.

### Settings and vault

The pods of an account share their settings through the relay: a theme, a shortcut or a colour changed on one reaches the others. The relay carries ciphertext only: one encrypted blob per account. Each setting merges on its own; you are asked only when the **same** setting changed on two pods (Settings > Remote access shows both values and which device made them).

The layout stays per device (code size, interface size, panel sizes). A device with none of its own uses the layout saved with *Use as default for new devices* (Settings > Fonts).

The **vault** is separate and off by default: the API keys of the model servers and the remembered database passwords. Switching it on sends them to the other pods, switching it off deletes them from the relay.

## Check

- `web-ide-pod pair status` on each pod names the same account.
- The home page of any pod lists the pods of the account, and Settings > Remote access > Devices the browsers.
- Change a theme on one pod: it reaches the others.

## Troubleshooting

| Symptom | Fix |
| --- | --- |
| The browser cannot unlock or enrol | Use a recent browser, and open the pod on `localhost` (not an IP address). |
| A pod joins nothing on the LAN | No relay and no mDNS: see [pods on the LAN](lan-pods.md), *Troubleshooting*. |
| `pair join` times out | It waits 5 minutes for an approval: run it again and approve sooner. |
| A revoked member still connects | The pod has not heard of the list yet: it refuses it at its next connection after the relay or a peer passes it. |
| *waiting for the sync key* on a pod | An admin device wraps the sync key in the Devices panel. |

## See also

[Pods on the local network](lan-pods.md) · [Self-host a relay](self-host-relay.md) · [Notifications on the phone](notifications.md) · [Relay design](../design/relay.md) · [Glossary](../../GLOSSARY.md)
