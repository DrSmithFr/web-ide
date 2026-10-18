# Install from the sources

```sh
git clone https://github.com/DrSmithFr/web-ide.git && cd web-ide
make build && ./bin/web-ide-pod     # run it in the terminal
make service                         # or install this build as the service
```

Building needs Go 1.27+ and Node.js 20.19+. Options of the pod: `-addr`, `-workspace` (default folder of new projects, `~/Apps`), `-data` (`~/.web-ide`), `-allow-remote` (plain HTTP to other machines, token only: prefer Tailscale), `-version`.


See [CONTRIBUTING.md](../../CONTRIBUTING.md) to work on Web IDE itself.
