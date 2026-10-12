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

### From another network: the relay

The relay is an optional piece: it only introduces the browser and the pod, the traffic between them is encrypted and the relay never sees your token. Run your own:

```sh
web-ide-pod relay            # signaling + STUN/TURN + bootstrap page, SQLite in ~/.web-ide-relay
```

Its own data folder (`~/.web-ide-relay`, `-data` only when given explicitly), so it does not share the pod's. Options: `-addr` (`127.0.0.1:4430`), `-turn-port` (3478), `-turn-ip` (the IP announced to clients), `-tls-port` (443, shared with HTTPS and TURN over TLS; a port below 1024 needs root, and TURN over TLS is simply off when it cannot be bound), `-push-subject` (the contact given to the push services, `mailto:` or `https:`; the VAPID key of the notifications is made once in `vapid.json`). Open the link the pod prints — `#pod=<id>&t=<token>`, the token stays in the hash and never reaches the relay.

Then start a pod with `-relay http://127.0.0.1:4430` (or `relay` in `config.json`). The pod prints its remote link, also shown in Settings > Remote access:

```
remote : http://127.0.0.1:4430/#pod=<id>&t=<token>
```

Open that link from any browser on another network: a small bootstrap page and a Service Worker come from the relay, the app itself comes from your pod through the tunnel. The pod keeps one outbound WebSocket to its relay (no port to open on the machine) and reconnects by itself; the token is proved inside the DataChannel, bound to the fingerprints of both sides. If the relay is down, `127.0.0.1` keeps working as before. TURN credentials are short-lived and given only to a registered, online pod. See [relay.md](relay.md).

#### Pairing: an account is a key

Once the relay is up, the token can be replaced by an **account**: a 24-word recovery phrase, shown once and never stored anywhere.

```sh
web-ide-pod pair create    # the phrase, then three words asked back
web-ide-pod pair status    # device, account, certificate, fingerprints
web-ide-pod pair join      # a code, a QR and the 4-word fingerprint of its key
```

The pod stores only its own key (`~/.web-ide/device.key`, 0600) and its certificate (`identity.json`). Each browser makes its own key and gets a certificate signed by the account key; the pod pins the account key and checks every peer itself, so a relay cannot impersonate a device. The phrase is typed on a computer, or a phone scans the invite QR shown by an admin device (Settings > Remote access > Devices); the 4-word fingerprint read on both sides is what you compare before approving.

Recent browsers only: WebCrypto Ed25519 and the WebAuthn PRF are required, with no fallback. A passkey is tied to an origin, so `localhost`, the tailnet address and the relay are separate devices, each enrolled once with the phrase — and WebAuthn refuses an IP address, so open the pod on `localhost` to unlock a device (the panel gives the link). Revoking a device from the panel signs a new list: the relay stores it and pushes it to the online pods, which refuse the revoked device at its next connection.

#### Other pods on the same network, with no relay

The pods paired with one account find each other on the local network by themselves, with no relay and no internet: each paired pod opens a LAN port (`:4436`, `-lan` or `lan.addr` in `config.json`, `-lan off` to close it) and announces itself by mDNS. The home page then shows one *Projects* block per pod of the account; a project of another pod opens through a port this pod keeps for it, so its page and its terminals come from the pod that holds it (*← Back to …* leads home). Only the pods of the account are listed: each one proves its certificate, bound to the TLS certificates of the link, and a revoked pod is refused and dropped. An unpaired pod opens no LAN port at all.

When mDNS does not find a pod (another subnet, multicast blocked), type its address (`192.168.1.20:4436`) in Settings > Remote access > Local network; the reason an address fails is shown next to it.

```sh
web-ide-pod pair join            # no relay: waits on the LAN, approve it in Devices on localhost
web-ide-pod pair join --phrase   # no admin around: the phrase typed, the pod keeps its own key
web-ide-pod pair revoke <key>    # the phrase typed: the list spreads from pod to pod
```

With no relay, the Devices panel (on `localhost`) is answered by the pod itself: it lists the devices it knows, shows the pods waiting to join on the LAN with their fingerprint, and passes a revocation on to the other pods. A list made offline goes to the relay as soon as a pod of the account reaches it, so a later revocation never undoes it. Inviting a phone by QR still needs the relay.

#### Notifications on the phone

On the relay origin, the bootstrap page installs as an app (*Install*, or *Add to Home Screen* on iOS, which shows notifications only from the installed app). Settings > Notifications > *Enable on this device* subscribes the browser: the pod then notifies it when the agent finishes a turn or asks for you, when a saved command flagged *test* fails, and when the assistant or Claude Code leaves feedback on a ticket — one switch per kind, *Send a test* to check. Nothing is sent while the device shows a window of the pod. A click opens the conversation or the ticket. The pod encrypts every notification for the browser: the relay forwards it without being able to read it.

### From the sources

```sh
git clone https://github.com/DrSmithFr/web-ide.git && cd web-ide
make build && ./bin/web-ide-pod     # run it in the terminal
make service                         # or install this build as the service
```

Building needs Go 1.27+ and Node.js 20.19+. Options of the pod: `-addr`, `-workspace` (default folder of new projects, `~/Apps`), `-data` (`~/.web-ide`), `-allow-remote` (plain HTTP to other machines, token only: prefer Tailscale), `-version`.

### Updating

Run the installer again (or `make service`). Your terminals keep running and your conversations with the assistant go on: they live in a second service, `web-ide-keeper`, which the pod reconnects to; the page reconnects by itself. When the keeper itself changes, it updates in place and waits for the answers being written; only the SSH terminals are closed then. `systemctl --user status web-ide-pod web-ide-keeper` shows both.

## Projects

![The home page with the project and its icon](images/home.png)

The home page lists your projects and the folders of the workspace not added yet: click one to open it. A project is a local folder or a folder on an SSH host (same features, through your local SSH keys). Each project gets an icon, generated from its name and editable.

A project opens in its own window, and comes back as you left it, in any browser connected to the same pod: tabs, splits, carets, terminals.

## Connect a model

The assistant talks to [llama.cpp](https://github.com/ggml-org/llama.cpp) (`llama-server`, also in router mode) or [Ollama](https://ollama.com), on your machine or on your network: open the assistant (right bar), *Add a model server*, give its address. Pick the model in the composer. Tool calling must be supported by the model and its chat template (`llama-server --jinja`).

A cloud provider (OpenAI, OpenRouter, any OpenAI-compatible API) is a server of type *OpenAI-compatible provider*, with its API key: the key stays on the pod. Type its models by hand when it does not list them. Marked *Available to sub-agents*, with a note ("strong at code, paid"), it lets your local model hand a task to a stronger one: the assistant picks it for a sub-agent, whose card shows its tokens and its cost. The default server of the sub-agents is in the *Sub-agents* tab of the settings.

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

A new conversation starts in **Orchestrator** mode, your start page: ask it *what do we work on today?* (the tickets that can start, in order, with a button to start the first), *what did we do yesterday?* (the tickets that moved), or tell it *I have an idea…* (it opens a Briefing conversation with your idea). It changes no file and starts nothing by itself: you click its buttons. The default mode is set in the settings (*Prompt and instructions*).

### 1. Briefing: from an idea to a ticket

![Briefing: the model asks questions, then writes the ticket](images/briefing.gif)

Open the assistant and switch it to **Briefing** mode (Shift+Tab cycles through Orchestrator, Build, Plan and Briefing). Describe the need in a few words. In this mode the model changes nothing: it reads the code to understand the context, then asks its questions, one at a time, with suggested answers:

![A question of the model, with suggested answers](images/briefing.png)

When the need is clear, it writes the ticket (or several) on the kanban: description, acceptance criteria, linked files. The conversation stays linked to the ticket.

### 2. Plan

![The ticket with its plan and goals](images/plan.png)

On the ticket, *Generate the plan* starts a conversation in Plan mode: the model reads the code concerned and writes the implementation plan and the goals, each with how to check it. The ticket moves to **To do**. Edit the plan or the goals if needed, or *Redo the plan*.

### 3. Development in a worktree

![The model develops the ticket in its worktree](images/develop.gif)

*Start development* creates the branch `ticket/<n>-<slug>` and a git worktree for it in `.ide/worktrees/`, runs the setup commands of the project in it (the Console commands flagged `setup`: `npm install`, copy a `.env`…), and shows it in the same window, where the model starts working:

- it follows the plan and the project instructions (`CLAUDE.md`, `AGENTS.md`, skills);
- it reads, searches, edits files, uses the language servers and runs commands (build, tests);
- it checks the goals one by one and commits on the ticket branch (messages start with `#<n>`);
- when done, it moves the ticket to **To test** with how to test it.

Your main folder is never touched: you can keep working there, or develop several tickets at once. Follow the work live, answer its questions, or stop and redirect it at any time.

![The development window at the end of the session](images/develop.png)

### 4. Test, feedback, merge

![The ticket to test: how to test, goals, changed files and their diff](images/review.png)

In **To test**, the ticket shows how to test, the goals, and the change against its base branch, file by file. Test it in its worktree (the worktree selector of the menu bar; its files and terminals carry the chip `#<n>`). Then:

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

- **Editor**: splits, multiple carets (Alt+J next occurrence, Alt+Shift+drag or middle button for columns), folding, find and replace, Search Everywhere (Shift twice), Recent Files (Ctrl+E), the switcher (Ctrl+Tab), clipboard history (Ctrl+Shift+V). The back and forward buttons of the mouse move in the history of the focused part: positions and tabs of the editor (like Ctrl+Alt+Left/Right), console tabs, conversations of the assistant; they never leave the project. Tabs of files sharing a name show the folders that tell them apart (src/index.ts, lib/index.ts).
- **Language servers** (gopls, typescript-language-server, pyright, intelephense…, from the pod's `PATH`): go to definition, references, completion, rename, formatting, diagnostics.
- **Terminals** in the bottom panel (Ctrl+Shift+`), detachable in their own window.
- **Git**: changes, staging, commits, branches, history graph, side-by-side diffs, other worktrees.
- **Databases** (SQLite, PostgreSQL, Redis), **Docker** (Compose stack, containers, logs) and SSH tunnels.
- When a program (the model, a formatter, another editor) changes an open file, the change is merged into your buffer; a real conflict opens a three-pane dialog.

All shortcuts are in *Settings → Keyboard*, with QWERTY and AZERTY presets.

## Where the data lives

- `~/.web-ide/`: settings (with their history), projects, sessions, model servers, secrets (mode 0600), the pairing token.
- `<project>/.ide/`: conversations (`chats.db`), kanban (`kanban.db`), worktrees of the tickets, database connections, folder marks, icon. The whole folder is ignored by git (its `.gitignore` is `*`); the worktrees use the `.ide` of the main project.
- Audio and private keys never leave your machine.

To work on Web IDE itself, `make dev` runs a separate pod (port 4434, data in `~/.web-ide-dev`) next to the installed one: see [CONTRIBUTING.md](../CONTRIBUTING.md).

## Updating the pictures

```sh
make shots                         # replays the recorded conversations
node e2e/shots/shots.cjs record    # records them again with a real model (LLM_URL, LLM_MODEL)
```

The scenario (`e2e/shots/shots.cjs`) clones this repository at the commit of the recording, drives the whole cycle above in headless Chromium, and writes the PNG and GIF files in `docs/images`. The answers of the model are replayed from `e2e/shots/recording.json.gz`, so the pictures can follow the interface without a model.
