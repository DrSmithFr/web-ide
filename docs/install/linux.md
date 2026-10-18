# Install on Linux

Web IDE is one program, the **pod**, that serves the interface to your browser and does the work on your machine (files, git, terminals, language servers, model calls).

## As a service (Linux)

```sh
curl -fsSL https://raw.githubusercontent.com/DrSmithFr/web-ide/main/scripts/install.sh | sh
```

The script downloads the latest release into `~/.local/bin/web-ide-pod` and installs a systemd user service that starts at boot, even before you log in (it enables *lingering* for your user). It prints the address to open, with the pairing token:

```
Open: http://127.0.0.1:4433/?token=…
```

Open it once: the token goes into a cookie and pairs this browser. The token is also in `~/.web-ide/token`.

- A given version: `… | sh -s -- v1.0.0`. Running the script again upgrades the pod and keeps your data.
- Status and logs: `systemctl --user status web-ide-pod`, `journalctl --user -u web-ide-pod`.
- The service keeps the `PATH` of the shell that installed it, so the pod finds `git`, `go`, `node` and the language servers you installed. Run the script again after installing new tools elsewhere.

## From other machines: Tailscale

The pod only answers the local machine. To use it from your laptop, phone or tablet, serve it on your [Tailscale](https://tailscale.com) network:

```sh
… | sh -s -- --tailscale
```

This runs `tailscale serve`, which gives the pod an HTTPS address with a real certificate, such as `https://my-desktop.my-tailnet.ts.net/?token=…`, reachable from every device of your tailnet, at home and away. HTTPS matters: browsers only give the microphone (dictation) and the clipboard to secure pages. If `tailscale serve` is refused, allow your user once with `sudo tailscale set --operator=$USER`.

## Updating

Run the installer again (or `make service`). Your terminals keep running and your conversations with the assistant go on: they live in a second service, `web-ide-keeper`, which the pod reconnects to; the page reconnects by itself. When the keeper itself changes, it updates in place and waits for the answers being written; only the SSH terminals are closed then. `systemctl --user status web-ide-pod web-ide-keeper` shows both.

To reach the pod from another network, see [the relay](../howto/self-host-relay.md); to pair your browsers and pods, [account and devices](../howto/account-and-devices.md).
