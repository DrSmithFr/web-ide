# User guide

*[Version française](guide.fr.md)*

How to install Web IDE, connect a model, and take a change from an idea to a merged branch with the assistant and the kanban. The pictures are made on this repository by `make shots` (see [Updating the pictures](#updating-the-pictures)).

- [Install](#install)
- [Projects](#projects)
- [Connect a model](#connect-a-model)
- [The development cycle](#the-development-cycle)
  1. [Briefing: from an idea to a ticket](#1-briefing-from-an-idea-to-a-ticket)
  2. [Plan](#2-plan)
  3. [Development in a worktree](#3-development-in-a-worktree)
  4. [Test, feedback, merge](#4-test-feedback-merge)
- [Working by hand](#working-by-hand)
- [Where the data lives](#where-the-data-lives)
- [Updating the pictures](#updating-the-pictures)

## Install

Web IDE is one program, the **pod**, that serves the interface to your browser and does the work on your machine (files, git, terminals, language servers, model calls).

### As a service (Linux)

```sh
curl -fsSL https://raw.githubusercontent.com/DrSmithFr/web-ide/main/scripts/install.sh | sh
```

The script downloads the latest release into `~/.local/bin/web-ide-pod` and installs a systemd user service that starts at boot, even before you log in (it enables *lingering* for your user). It prints the address to open, with the pairing token:

```
Open: http://127.0.0.1:4433/?token=…
```

Open it once: the token goes into a cookie and pairs this browser. The token is also in `~/.web-ide/token`.

- A given version: `… | sh -s -- v1.0.0`. Running the script again upgrades the pod and keeps your data.
- Status and logs: `systemctl --user status web-ide-pod`, `journalctl --user -u web-ide-pod`.
- The service keeps the `PATH` of the shell that installed it, so the pod finds `git`, `go`, `node` and the language servers you installed. Run the script again after installing new tools elsewhere.

### From other machines: Tailscale

The pod only answers the local machine. To use it from your laptop, phone or tablet, serve it on your [Tailscale](https://tailscale.com) network:

```sh
… | sh -s -- --tailscale
```

This runs `tailscale serve`, which gives the pod an HTTPS address with a real certificate, such as `https://my-desktop.my-tailnet.ts.net/?token=…`, reachable from every device of your tailnet, at home and away. HTTPS matters: browsers only give the microphone (dictation) and the clipboard to secure pages. If `tailscale serve` is refused, allow your user once with `sudo tailscale set --operator=$USER`.

### From the sources

```sh
git clone https://github.com/DrSmithFr/web-ide.git && cd web-ide
make build && ./bin/web-ide-pod     # run it in the terminal
make service                         # or install this build as the service
```

Building needs Go 1.27+ and Node.js 20.19+. Options of the pod: `-addr`, `-workspace` (default folder of new projects, `~/Apps`), `-data` (`~/.web-ide`), `-allow-remote` (plain HTTP to other machines, token only: prefer Tailscale), `-version`.

## Projects

![The home page with the project and its icon](images/home.png)

The home page lists your projects and the folders of the workspace not added yet: click one to open it. A project is a local folder or a folder on an SSH host (same features, through your local SSH keys). Each project gets an icon, generated from its name and editable.

A project opens in its own window, and comes back as you left it, in any browser connected to the same pod: tabs, splits, carets, terminals.

## Connect a model

The assistant talks to [llama.cpp](https://github.com/ggml-org/llama.cpp) (`llama-server`, also in router mode) or [Ollama](https://ollama.com), on your machine or on your network: open the assistant (right bar), *Add a model server*, give its address. Pick the model in the composer. Tool calling must be supported by the model and its chat template (`llama-server --jinja`).

The pictures of this guide are made with Qwen3.8 27B on llama.cpp.

## The development cycle

Each project has a kanban. A ticket goes through four stages, each one with its conversation of the assistant linked to it:

| Stage | What happens | Who |
|---|---|---|
| **New** | The need is clarified and written down | A *Briefing* conversation questions you and writes the ticket |
| **To do** | The implementation plan and its goals | *Generate the plan*: the model reads the code and writes them |
| **In progress** | The code, on its own branch and worktree | *Start development*: the model codes, tests and commits |
| **To test** | You check; feedback goes back to the model | You, then *Fix sessions*; finally merge and close |

You decide every move between stages; the model works inside one.

### 1. Briefing: from an idea to a ticket

![Briefing: the model asks questions, then writes the ticket](images/briefing.gif)

Open the assistant and switch it to **Briefing** mode (Shift+Tab cycles through Build, Plan and Briefing). Describe the need in a few words. In this mode the model changes nothing: it reads the code to understand the context, then asks its questions, one at a time, with suggested answers:

![A question of the model, with suggested answers](images/briefing.png)

When the need is clear, it writes the ticket (or several) on the kanban: description, acceptance criteria, linked files. The conversation stays linked to the ticket.

### 2. Plan

![The ticket with its plan and goals](images/plan.png)

On the ticket, *Generate the plan* starts a conversation in Plan mode: the model reads the code concerned and writes the implementation plan and the goals, each with how to check it. The ticket moves to **To do**. Edit the plan or the goals if needed, or *Redo the plan*.

### 3. Development in a worktree

![The model develops the ticket in its own window](images/develop.gif)

*Start development* creates the branch `ticket/<n>-<slug>` and a git worktree for it in `.ide/worktrees/`, runs the setup command of the kanban settings in it (`npm install`, copy a `.env`…), and opens it in its own window, where the model starts working:

- it follows the plan and the project instructions (`CLAUDE.md`, `AGENTS.md`, skills);
- it reads, searches, edits files, uses the language servers and runs commands (build, tests);
- it checks the goals one by one and commits on the ticket branch (messages start with `#<n>`);
- when done, it moves the ticket to **To test** with how to test it.

Your main folder is never touched: you can keep working there, or develop several tickets at once. Follow the work live, answer its questions, or stop and redirect it at any time.

![The development window at the end of the session](images/develop.png)

### 4. Test, feedback, merge

![The ticket to test: how to test, goals, changed files and their diff](images/review.png)

In **To test**, the ticket shows how to test, the goals, and the change against its base branch, file by file. Test it in the worktree window (its terminals, its run commands). Then:

- **Add feedback** (bug, info or new feature) for what is wrong: *Fix session* sends one feedback or all of them to the model, which fixes them in the worktree and marks them handled.
- **Rebase** on the base branch when it moved; conflicts are listed with *Continue*, *Abort* and a *Resolution session* for the model.
- **Merge** into the local base branch (`merge --no-ff` or squash), or *Create the pull request* (with `gh`).
- **Close** the ticket: the worktree is removed, the branch is kept.

A big change can be split into a **lineage**: the next steps are tickets whose parent is the first one (the briefing proposes it, or *Make it a step of…* in the *Lineage* section of a ticket). Each step is developed in the same worktree once the previous one is validated (*Validate the step* on the first ticket, *Close* on a step), and the first ticket is merged once all its steps are finished. A ticket can also wait for another lineage to be merged; a blocked ticket shows what it waits for, and *Start anyway…* goes past it. The *Roadmap* view of the board shows the lineages as rows of blocks as wide as their size, so you see what can start and how big the work ahead is.

![The kanban after the merge](images/kanban.png)

![The Git panel: the ticket branch merged into main](images/git.png)

## Working by hand

Everything the model does, you can do yourself, in the same windows:

![The editor with completion by gopls, and a terminal](images/editor.png)

- **Editor**: splits, multiple carets (Alt+J next occurrence, Alt+Shift+drag or middle button for columns), folding, find and replace, Search Everywhere (Shift twice), Recent Files (Ctrl+E), the switcher (Ctrl+Tab), clipboard history (Ctrl+Shift+V).
- **Language servers** (gopls, typescript-language-server, pyright, intelephense…, from the pod's `PATH`): go to definition, references, completion, rename, formatting, diagnostics.
- **Terminals** in the bottom panel (Ctrl+Shift+`), detachable in their own window.
- **Git**: changes, staging, commits, branches, history graph, side-by-side diffs, other worktrees.
- **Databases** (SQLite, PostgreSQL, Redis), **Docker** (Compose stack, containers, logs) and SSH tunnels.
- When a program (the model, a formatter, another editor) changes an open file, the change is merged into your buffer; a real conflict opens a three-pane dialog.

All shortcuts are in *Settings → Keyboard*, with QWERTY and AZERTY presets.

## Where the data lives

- `~/.web-ide/`: settings (with their history), projects, sessions, model servers, secrets (mode 0600), the pairing token.
- `<project>/.ide/`: conversations (`chats.db`), kanban (`kanban.db`), worktrees of the tickets, database connections, folder marks, icon. Its own `.gitignore` keeps the bases and the worktrees out of git.
- Audio and private keys never leave your machine.

To work on Web IDE itself, `make dev` runs a separate pod (port 4434, data in `~/.web-ide-dev`) next to the installed one: see [CONTRIBUTING.md](../CONTRIBUTING.md).

## Updating the pictures

```sh
make shots                         # replays the recorded conversations
node e2e/shots/shots.cjs record    # records them again with a real model (LLM_URL, LLM_MODEL)
```

The scenario (`e2e/shots/shots.cjs`) clones this repository at the commit of the recording, drives the whole cycle above in headless Chromium, and writes the PNG and GIF files in `docs/images`. The answers of the model are replayed from `e2e/shots/recording.json.gz`, so the pictures can follow the interface without a model.
