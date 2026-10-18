# Modify the IDE

Change Web IDE itself from the sources of your installed version, with its own assistant, and install the result.

## Prerequisites

- The pod installed as a unit ([Linux](../install/linux.md), or [WSL](../install/windows-wsl.md)).
- Go 1.27+ and Node.js 20.19+ ([from the sources](../install/from-sources.md)), and an assistant: a model server, or an agent (Claude Code, opencode, pi) in the Agents tool.

## Steps

1. Find your version and clone the sources at that tag, so your changes start from what you run:

   ```sh
   web-ide-pod -version
   git clone https://github.com/DrSmithFr/web-ide.git ~/Apps/web-ide && cd ~/Apps/web-ide
   git checkout <version>
   ```

   The version printed is a release tag (`v1.18.3`) when the pod came from the installer; a build of a tree reports a development version.
2. Open the folder as a project of the IDE. Create a branch for the change (or a ticket on its kanban, which makes the branch and a worktree).
3. Ask the assistant or an agent to make the change. They read [AGENTS.md](../../AGENTS.md) (conventions, docs rules) and the [glossary](../../GLOSSARY.md); the architecture is in [docs/design/architecture.md](../design/architecture.md). Texts go through `t()`: see [Add a language](add-translation.md).
4. Check it:

   ```sh
   make test      # gofmt, go vet, Go tests, TypeScript, links of the docs
   make e2e       # browser tests (or ./e2e/run.sh <suite>)
   ```

5. Try it next to the installed pod: `make dev` runs a second pod (port 4434, data in `~/.web-ide-dev`) and the Vite dev server with hot reload (`http://<host>:5173/?token=…`). It never touches the installed one.
6. Install it: `make service` builds and runs `scripts/install.sh --binary bin/web-ide-pod`, replacing the pod of the unit.

Your terminals and the conversations (the assistant's and the agents') go on while the pod is replaced: they live in the keeper, which the pod reconnects to, and the page reconnects by itself. Only when the keeper's protocol changes does it update itself in place (SSH terminals close then).

## Check

- `make test` passes.
- `web-ide-pod -version` shows your build after `make service`, and `systemctl --user status web-ide-pod web-ide-keeper` shows both active.
- The change is visible in the page, and the conversation you were in is still there.

## Troubleshooting

| Symptom | Fix |
| --- | --- |
| `make dev` cannot bind a port | Another dev pod runs: it uses 4434 and 5173. Stop it, or set `DEV_ADDR`. |
| `make service` fails | `make build` first, to see the error; the unit needs systemd ([Linux](../install/linux.md), *Troubleshooting*). |
| A text is missing in French | `npm run check` in `web/` names it; add it to `web/src/i18n/fr.json`. |
| The new pod is broken | Install the release again with the script of [Linux](../install/linux.md): data in `~/.web-ide` is kept. |

## See also

[From the sources](../install/from-sources.md) · [Add a language](add-translation.md) · [CONTRIBUTING.md](../../CONTRIBUTING.md) · [Architecture](../design/architecture.md)
