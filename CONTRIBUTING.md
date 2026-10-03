# Contributing

Thanks for your interest! Bug reports, ideas and pull requests are welcome.

## Before you start

- For anything larger than a small fix, open an issue first to discuss the change.
- Read [docs/architecture.md](docs/architecture.md): it describes the code layout, the protocol between the page and the pod, and pitfalls already met.

## Setting up

```sh
make build   # installs the npm dependencies, builds the front end, then the pod
make dev     # pod + Vite dev server with hot reload (http://<host>:5173/?token=…)
```

## Checks

A pull request must keep these green:

```sh
make test    # go vet, Go tests, TypeScript check
make e2e     # browser tests; ./e2e/run.sh <suite> runs one suite
```

Add or update e2e assertions for any visible change. The `lsp` suite needs `gopls` and the `speech` suite downloads a small Whisper model once.

## Conventions

- **Language**: code, comments, documentation and commit messages in English.
- **Interface texts** go through `t()` (`web/src/i18n`): write the English text as the key and add its French translation to `web/src/i18n/fr.json` (`npm run check` reports missing ones). Messages sent by the pod use `i18n.Errorf` / `i18n.T` with the French catalog in `pod/internal/i18n/fr.json` (checked by its tests).
- **Style**: `gofmt` for Go; for TypeScript, follow the surrounding code (2 spaces, no semicolons, single quotes). Keep comments for the *why*, not the *what*.
- **Commits**: one logical change per commit, with a short imperative subject line (`Kanban: freeze the change when merging`) and a body explaining why when it is not obvious.
- **Dependencies**: keep them few. The pod is a single static binary and the front end has no runtime CDN.

## Reporting a security issue

Please do not open a public issue; see [SECURITY.md](SECURITY.md).
