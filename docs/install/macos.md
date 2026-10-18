# Install on macOS

Run the pod from the release archive: there is no unit that starts it, you launch it yourself.

## Prerequisites

- macOS on Intel (`darwin/amd64`) or Apple silicon (`darwin/arm64`).
- `git`, and the tools your projects need, in your `PATH`.

## Steps

1. Download the archive for your Mac from the [latest release](https://github.com/DrSmithFr/web-ide/releases/latest): `web-ide-pod-darwin-arm64.tar.gz` (Apple silicon) or `web-ide-pod-darwin-amd64.tar.gz` (Intel).
2. Extract it and run the pod:

   ```sh
   mkdir web-ide && tar -xzf web-ide-pod-darwin-arm64.tar.gz -C web-ide && cd web-ide
   ./web-ide-pod
   ```

3. Open the address it prints, with the **access token**: the token goes into a cookie that opens the pod in this browser. It is also in `~/.web-ide/token`.

The installer script, the keeper and the start at boot are Linux only (`scripts/install.sh` refuses to run on macOS). The pod stops when you close its terminal; to keep it, run it in a terminal multiplexer or write your own launchd job.

## Check

`./web-ide-pod -version` prints the release, and the address shows the home page of the IDE.

## Update

Stop the pod, download the new archive, replace the binary. Your data in `~/.web-ide` is kept.

## Troubleshooting

| Symptom | Fix |
| --- | --- |
| macOS refuses to open the binary (unidentified developer) | The binary is not signed: remove the quarantine flag with `xattr -d com.apple.quarantine web-ide-pod`, or allow it in System Settings > Privacy & Security. |
| The terminals end with the pod | Without the keeper, the pod runs them itself: they live as long as it does. |
| A tool is not found | The pod has the `PATH` of the shell that started it: start it from a shell that has the tool. |

## See also

[Linux](linux.md) · [From the sources](from-sources.md) · [Account and devices](../howto/account-and-devices.md)
