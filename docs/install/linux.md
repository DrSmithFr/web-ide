# Install on Linux

Install the pod as a systemd user unit that starts at boot, and open it in your browser.

Web IDE is one program, the **pod**: it serves the interface to your browser and does the work on your machine (files, git, terminals, language servers, model calls).

## Prerequisites

- Linux on x86_64 or arm64, with systemd (`systemctl`) and `curl`.
- `git`, and whatever your projects need (`go`, `node`, language servers): the pod finds the tools that are in your `PATH` when you run the script.
- For other devices: a [Tailscale](https://tailscale.com) account (optional).

On Windows, follow [Windows with WSL](windows-wsl.md); on macOS, [macOS](macos.md).

## Steps

1. Run the installer:

   ```sh
   curl -fsSL https://raw.githubusercontent.com/DrSmithFr/web-ide/main/scripts/install.sh | sh
   ```

   It downloads the latest release into `~/.local/bin/web-ide-pod`, writes two units in `~/.config/systemd/user/` (`web-ide-pod` and `web-ide-keeper`, the keeper runs the terminals), enables *lingering* for your user so they start at boot even before you log in, and starts them. A given version: `… | sh -s -- v1.0.0`.

2. Open the address it prints:

   ```
   Open: http://127.0.0.1:4433/?token=…
   ```

   The **access token** goes into a cookie and pairs this browser. It is also in `~/.web-ide/token`.

3. Optional, to use the pod from your laptop, phone or tablet: serve it on your tailnet.

   ```sh
   curl -fsSL https://raw.githubusercontent.com/DrSmithFr/web-ide/main/scripts/install.sh | sh -s -- --tailscale
   ```

   This runs `tailscale serve`, which gives the pod an HTTPS address with a real certificate, printed at the end (`On the tailnet: https://my-desktop.my-tailnet.ts.net/?token=…`). HTTPS matters: browsers give the microphone (dictation) and the clipboard to secure pages only.

4. Optional: to reach the pod from any network, see [a relay](../howto/self-host-relay.md); to share one account between your browsers and pods, [account and devices](../howto/account-and-devices.md).

## Check

```sh
systemctl --user status web-ide-pod web-ide-keeper    # both active (running)
~/.local/bin/web-ide-pod -version
```

The address of step 2 shows the home page of the IDE.

## Update

Run the installer again (or `make service` from the sources): the pod is replaced and your data in `~/.web-ide` is kept. Your terminals keep running and your conversations with the assistant go on: they live in the keeper, which the pod reconnects to, and the page reconnects by itself. The keeper is replaced only when its protocol changes, and then updates itself in place; only the SSH terminals are closed then.

The units keep the `PATH` of the shell that ran the script: run it again after installing new tools elsewhere.

## Logs and uninstall

```sh
journalctl --user -u web-ide-pod       # or -u web-ide-keeper
```

To remove it, stop and disable the units, then delete what the script wrote (your data stays in `~/.web-ide` until you delete it too):

```sh
systemctl --user disable --now web-ide-pod web-ide-keeper
rm ~/.config/systemd/user/web-ide-pod.service ~/.config/systemd/user/web-ide-keeper.service
rm ~/.local/bin/web-ide-pod && rm -r ~/.local/lib/web-ide
systemctl --user daemon-reload
```

## Troubleshooting

| Symptom | Fix |
| --- | --- |
| `systemctl not found: the unit needs systemd` | The script needs systemd; run `web-ide-pod` by hand from the [release archive](macos.md) or the [sources](from-sources.md). |
| `no release build for <arch>` | Releases exist for x86_64 and arm64 only: [build from the sources](from-sources.md). |
| `warning: loginctl enable-linger failed` | The pod then starts with your session only. Ask root to run `loginctl enable-linger $USER`. |
| `warning: tailscale serve failed` | Allow your user once: `sudo tailscale set --operator=$USER`, then run the script with `--tailscale` again. |
| A tool is not found in the terminals or by the language servers | The unit has the `PATH` of the shell that ran the script: install the tool, then run the script again from a shell that has it. |
| The address does not open | `systemctl --user status web-ide-pod` and `journalctl --user -u web-ide-pod` say why; the default address is `127.0.0.1:4433` (`WEBIDE_ADDR` before the script changes it). |

## See also

[From the sources](from-sources.md) · [Windows with WSL](windows-wsl.md) · [Account and devices](../howto/account-and-devices.md) · [Self-host a relay](../howto/self-host-relay.md)
