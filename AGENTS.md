# Notes for AI agents

Web IDE: a Go program, the pod (`pod/`), serving a SolidJS front end (`web/`) over a WebSocket, with its built-in plugins in `plugins/<id>/`; the same binary runs the relay (`relay/`), and both share `lib/`. Read these first:

- [GLOSSARY.md](GLOSSARY.md): the words of the project, one meaning each. Use them in code, docs and UI texts; its *Conflicts* list the names still to fix.
- [docs/README.md](docs/README.md): every page of the documentation (install, how-to, guide, design).
- [docs/design/spec.md](docs/design/spec.md): what the IDE does and why.
- [docs/design/architecture.md](docs/design/architecture.md): the system map, code layout, protocol, build and test commands, pitfalls.
- The code: [pod/README.md](pod/README.md), [pod/packages.md](pod/packages.md), [relay/README.md](relay/README.md), [lib/README.md](lib/README.md), [plugins/README.md](plugins/README.md) (and the README of each plugin), [web/src/README.md](web/src/README.md), [e2e/README.md](e2e/README.md): a Mermaid map, the components, the pitfalls and the tests of each area.
- [docs/design/kanban.md](docs/design/kanban.md): the kanban and its git workflow.
- [docs/design/relay.md](docs/design/relay.md): remote access through a relay, and the hosted one at `antennae.space`.
- [docs/howto/self-host-relay.md](docs/howto/self-host-relay.md): running your own relay (domain, flags, firewall, backups).
- [docs/design/plugins.md](docs/design/plugins.md): the plugin system (phase 1 decided: core, Docker, database).
- [docs/design/desks.md](docs/design/desks.md): draft under discussion (desks and agent desks).

## Commands

```
make build   # front end, then the pod binary with the front end embedded
make test    # gofmt, go vet, go test, tsc, links and paths of the docs
make e2e     # browser tests (or ./e2e/run.sh <suite>)
```

Go is in the `PATH` of the pod: the assistant of the IDE calls `go` directly (a `$GO` variable makes every command ask). Other shells may not have it: the Makefile falls back to `~/sdk/go/bin/go`.

## Conventions

- Code, comments, docs and commit messages in English.
- Every text shown to the user goes through `t()` (`web/src/i18n`): the English text is the key, each other language of `languages.json` has a catalog (`fr.json`, `es.json`). Messages sent by the pod use `i18n.Errorf` / `i18n.T` (`lib/i18n`) and its catalogs. A text of a built-in plugin only (its page, its manifest, its Go messages) goes to its own catalogs, `plugins/<id>/i18n/<lang>.json`; a text shared with the core stays in the core's.
- One tested commit per feature: `make test` and the e2e suites touching the change must pass; add e2e assertions for visible features.
- Match the surrounding code: comment density, naming, small focused files.

## Docs

The docs are written for humans and AIs alike, and kept true with the code:

- Before changing a folder, read its `README.md` when it has one: its map, its invariants and its pitfalls.
- A change that contradicts `docs/design/` updates it in the same commit; a visible change updates the guide or the how-to that shows it.
- The plan of a ticket lists the docs it touches.
- A new term, or a term whose meaning changes, goes to `GLOSSARY.md` in the same commit.
- Relative links only between docs; `make test` checks them (`scripts/check-links.mjs`) and the paths of the repository written as inline code (`scripts/check-paths.mjs`).
- `AGENTS.md` holds the instructions (read by Claude Code through `CLAUDE.md`, by opencode, pi and the assistant directly).
