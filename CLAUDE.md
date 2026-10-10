# Notes for AI agents

Web IDE: a Go agent (`pod/`) serving a SolidJS front end (`web/`) over a WebSocket. Read these first:

- [docs/spec.md](docs/spec.md): what the IDE does and why.
- [docs/architecture.md](docs/architecture.md): code layout, protocol, build and test commands, pitfalls.
- [docs/kanban.md](docs/kanban.md): the kanban and its git workflow.
- [docs/relay.md](docs/relay.md): remote access through a relay (planned).

## Commands

```
make build   # front end, then the pod binary with the front end embedded
make test    # go vet, go test, tsc
make e2e     # browser tests (or ./e2e/run.sh <suite>)
```

Go is in the `PATH` of the pod: the assistant of the IDE calls `go` directly (a `$GO` variable makes every command ask). Other shells may not have it: the Makefile falls back to `~/sdk/go/bin/go`.

## Conventions

- Code, comments, docs and commit messages in English.
- Every text shown to the user goes through `t()` (`web/src/i18n`): the English text is the key, `fr.json` holds the French translation. Messages sent by the pod use `i18n.Errorf` / `i18n.T` (`pod/internal/i18n`) and its French catalog.
- One tested commit per feature: `make test` and the e2e suites touching the change must pass; add e2e assertions for visible features.
- Match the surrounding code: comment density, naming, small focused files.
