# Install from the sources

Build the pod yourself, to run it, to install it as a unit, or to change it.

## Prerequisites

- `git`, `make`.
- Go 1.27 or later (`go.mod`); the Makefile also looks in `~/sdk/go/bin/go`.
- Node.js 20.19 or later, with `npm`, for the front end.

## Steps

1. Clone and build (front end first, then the pod with the front end embedded):

   ```sh
   git clone https://github.com/DrSmithFr/web-ide.git && cd web-ide
   make build
   ```

2. Run it in the terminal:

   ```sh
   ./bin/web-ide-pod
   ```

   Open the address it prints, with the **access token** (also in `~/.web-ide/token`).

3. Or install this build as the units of [Linux](linux.md), which start at boot:

   ```sh
   make service
   ```

   It runs `scripts/install.sh --binary bin/web-ide-pod`: your terminals and conversations survive the update.

4. To work on the IDE, `make dev` runs a separate pod (port 4434, data in `~/.web-ide-dev`, a copy of `~/.web-ide`) with the Vite dev server and hot reload at `http://<host>:5173/?token=…`, next to the installed one. See [modify the IDE](../howto/modify-the-ide.md).

## Options of the pod

`-addr` (listen address, default `127.0.0.1:4433`), `-projects` (the projects folder, where new projects are made, `~/Apps`; `-workspace` still works), `-data` (`~/.web-ide`), `-allow-remote` (plain HTTP to other machines, protected by the token only: prefer Tailscale), `-relay`, `-lan`, `-name`, `-version`. `web-ide-pod -h` lists them all.

## Check

`./bin/web-ide-pod -version` prints the version; `make test` (gofmt, `go vet`, Go tests, TypeScript, links of the docs) passes.

## Troubleshooting

| Symptom | Fix |
| --- | --- |
| `go: command not found` | Put Go in your `PATH`, or install it in `~/sdk/go/bin`; or `make GO=/path/to/go build`. |
| `go.mod requires go >= 1.27` | Update Go. |
| `npm run build` fails on the engine version | Update Node.js to 20.19 or later. |
| `make service` does not start the unit | Needs systemd: see [Linux](linux.md), *Troubleshooting*. |

## See also

[CONTRIBUTING.md](../../CONTRIBUTING.md) · [Architecture](../design/architecture.md) · [Linux](linux.md)
