# Install on Windows with WSL

Run the Linux pod inside WSL 2, started at your logon, and reach it from Windows.

The pod is a Linux program: on Windows it runs in a WSL 2 distribution, as the same systemd units as on [Linux](linux.md).

## Prerequisites

- Windows 10 or 11 with WSL 2 and a distribution (Ubuntu, for example): `wsl --install` in a PowerShell.
- Inside the distribution: `curl`, `git`, and the tools your projects need.

## Steps

1. Turn systemd on in the distribution. In `/etc/wsl.conf` (create it as root):

   ```ini
   [boot]
   systemd=true
   ```

   Then, in a PowerShell, restart the WSL virtual machine:

   ```powershell
   wsl --shutdown
   ```

2. Install the pod inside WSL, as on Linux (`systemctl --user` must work, see *Check*):

   ```sh
   curl -fsSL https://raw.githubusercontent.com/DrSmithFr/web-ide/main/scripts/install.sh | sh
   ```

3. Start WSL at your logon, so the units run without you opening a terminal. In the Windows **Task Scheduler**, create a task:

   - *Trigger*: **At log on**, of your user.
   - *Action*: start the program `wsl`, with the arguments `--exec dbus-launch true`.

   Starting any process in the distribution boots the virtual machine and its user units (`web-ide-pod`, `web-ide-keeper`); `dbus-launch` opens the user session bus they need. The same task from a command prompt (not tried by the authors of this page):

   ```bat
   schtasks /Create /TN "WSL at logon" /SC ONLOGON /TR "wsl.exe --exec dbus-launch true"
   ```

4. Open the **access token** address the script printed in a Windows browser: `http://127.0.0.1:4433/?token=…` (also `localhost`). WSL 2 forwards its local ports to Windows.

## Networking

WSL 2 runs with **NAT**, its default: the virtual machine has its own address, hidden behind Windows.

- From Windows, the pod answers on `localhost`.
- From other machines of the LAN, the pod is not reachable directly: use [Tailscale](linux.md) inside WSL, or [a relay](../howto/self-host-relay.md).
- Other pods of your account on the LAN do not find a pod under NAT by mDNS, and cannot reach its LAN port (`:4436`): use the relay instead (an address typed in Settings cannot reach it either, unless the port is forwarded to WSL, which this page does not cover). A pod under NAT still sees the others.
- **Wake-on-LAN** from this pod: the networks of the virtual machine are not the LAN, so the pod reads the IPv4 networks of Windows and sends the magic packet through `powershell.exe` (the WSL interop, `pod/internal/power/wsl.go`). The interop must be on.
- Mirrored networking (`networkingMode=mirrored` in `.wslconfig`) is an option the authors have not tried: the code does not rule it out, but does not count on a broadcast leaving the machine either.

## Check

In the WSL terminal:

```sh
systemctl --user status web-ide-pod web-ide-keeper   # active (running)
```

Then log off and on again in Windows, without opening WSL: the address of step 4 still opens.

## Troubleshooting

| Symptom | Fix |
| --- | --- |
| `systemctl: command not found` or `Failed to connect to bus` in WSL | `systemd=true` is missing or the machine was not restarted: check `/etc/wsl.conf`, then `wsl --shutdown`. |
| The address works after you open a WSL terminal, not before | The logon task does not run: check its trigger (*At log on*), its action (`wsl`, `--exec dbus-launch true`) and its last run result in the Task Scheduler. |
| `warning: loginctl enable-linger failed` | See [Linux](linux.md), *Troubleshooting*. |
| The pod does not wake a machine | `powershell.exe` is not found: is the WSL interop on? The pod looks in the `PATH`, then at `/mnt/c/Windows/System32/WindowsPowerShell/v1.0/powershell.exe`. |
| The pod is not reachable from the phone on the LAN | This is NAT: use Tailscale or the relay. |

## See also

[Linux](linux.md) · [Pods on the local network](../howto/lan-pods.md) · [Self-host a relay](../howto/self-host-relay.md) · [Relay design, *Power*](../design/relay.md)
