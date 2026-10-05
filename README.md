# Web IDE

*[Version française](README.fr.md)*

A self-hosted IDE in your browser, with an AI agent that runs on **your** models (llama.cpp, Ollama) and a kanban that takes each change from an idea to a merged branch.

![The model develops a ticket in its own git worktree: it reads the code, edits, runs the tests and commits](docs/images/develop.gif)

- **One binary, local first.** A small Go program, the *pod*, serves the interface and does the work on your machine: files, git, terminals, language servers, databases, model calls. No Electron, no cloud, no account.
- **The same session everywhere.** Open it from another browser or device (over Tailscale) and find your tabs, splits and terminals as you left them.
- **A development cycle with the agent.** Briefing → plan → development in a worktree → test → merge, each step with its conversation, each decision yours.

## From an idea to a merged branch

| | |
|---|---|
| **1. Briefing**: describe the need; the model reads the code, asks its questions, then writes the ticket. <br><br> ![Briefing](docs/images/briefing.gif) | **2. Plan**: the model writes the implementation plan and goals you can check. <br><br> ![Plan](docs/images/plan.png) |
| **3. Development**: a branch and a worktree per ticket, in its own window; the model codes, tests, commits. <br><br> ![Development](docs/images/develop.png) | **4. Test and merge**: how to test, the goals, the diff; feedback goes back to the model; merge when it is right. <br><br> ![Review](docs/images/review.png) |

The full walk-through is in the **[user guide](docs/guide.md)**.

## And a real editor

![The editor with completion from gopls, and a terminal](docs/images/editor.png)

- **Editor**: fast on 100,000-line files, splits, multiple carets, folding, Search Everywhere, Recent Files, clipboard history, themes (this is *High contrast*), QWERTY and AZERTY shortcuts, full keyboard navigation.
- **Language servers**: definition, references, completion, rename, formatting, diagnostics (gopls, typescript-language-server, pyright, intelephense…).
- **Git**: changes, staging, commits, branches, history graph, side-by-side diffs, worktrees.
- **Terminals**, **databases** (SQLite, PostgreSQL, Redis), **Docker** (Compose, containers, logs) and SSH tunnels.
- **Local and SSH projects**, with your local keys.
- **Safe with other tools**: changes made to open files by another program are merged into your buffer.
- **Assistant**: Build, Plan and Briefing modes, `CLAUDE.md` / `AGENTS.md` and skills, Mermaid diagrams, image / PDF / audio attachments, local dictation (Whisper in the browser), doodles.
- Interface in **English and French**.

| | |
|---|---|
| ![The kanban](docs/images/kanban.png) | ![The Git panel](docs/images/git.png) |

## Install

Linux, as a service started at boot ([details](docs/guide.md#install)):

```sh
curl -fsSL https://raw.githubusercontent.com/DrSmithFr/web-ide/main/scripts/install.sh | sh
# from your other devices, over HTTPS on your Tailscale network:
curl -fsSL https://raw.githubusercontent.com/DrSmithFr/web-ide/main/scripts/install.sh | sh -s -- --tailscale
```

It prints the address to open once, with the pairing token. On macOS, download the archive of the [latest release](https://github.com/DrSmithFr/web-ide/releases/latest) and run `./web-ide-pod`.

From the sources (Go 1.27+, Node.js 20.19+):

```sh
git clone https://github.com/DrSmithFr/web-ide.git && cd web-ide
make build && ./bin/web-ide-pod
```

Optional: `git`, language servers in the `PATH`, a [llama.cpp](https://github.com/ggml-org/llama.cpp) or [Ollama](https://ollama.com) server for the assistant.

## Security

The pod has the rights of your user: it reads and writes files and runs commands for the page. It only answers the local machine, every request needs the pairing token, and the agent should only be connected to model servers you trust. See [SECURITY.md](SECURITY.md).

## Contributing

[CONTRIBUTING.md](CONTRIBUTING.md) to get started, [docs/architecture.md](docs/architecture.md) for the code, [docs/spec.md](docs/spec.md) for the intended behaviour, [CHANGELOG.md](CHANGELOG.md) for the releases.

## License

[MIT](LICENSE)
