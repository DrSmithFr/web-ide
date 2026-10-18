# Desks (draft)

Status: **under discussion**. User desks and the layout modes are planned in tickets #81 (desks in Full mode), #82 (tiles and Grid), #83 (Scroll) and #84 (phone on Scroll), one lineage; the rest has no ticket. This page records the direction chosen on 2026-10-16 and the questions still open. It goes with [plugins.md](plugins.md): both treat the IDE as a window manager.

## Why

- **Several screens.** Every window of a project shows the same desk state (`desks/<project>.json`): two windows show the same layout. The detached views (`/project/:id/editor`, `/project/:id/tool/:toolId`) only half answer it.
- **Agents working in the background.** The follow mode of the Agents tool opens the files of the agent in the window of the user and takes the focus. Consoles, commands, previews and agent processes run behind the windows with no single place to see or end them.

The name: a **desk** (*bureau* in French), as the virtual desktops of GNOME and KDE. *Workspace* is avoided: the folder of the pod is the *projects folder*, and a *session* is a run of an agent.

## Model

```
Project (or worktree)
 └─ Desk ── held by the user, or by an agent session (ACP agent, assistant conversation)
     ├─ layout mode (Full / Scroll / Grid) and its tree of tiles
     ├─ open tabs (views: the documents and their buffers stay shared in the core)
     ├─ consoles and commands     (owner = the desk, in the keeper)
     └─ previews and tunnels opened from it
```

- A resource belongs to the **desk**, not to whoever started it: a console the user opens in the desk of an agent belongs to that desk.
- The **buffers stay shared**: an agent editing a file open on a desk of the user shows live there, as today. Only the tabs belong to a desk.
- The settings, Recent Files and the worktree list stay per project.

## Layout modes

Full and Grid are done in #82, Scroll in #83 (see [spec.md](spec.md#9-desk-state-and-persistence)). Each desk picks its mode. The Full mode keeps the rails and tool zones; the two others need **tiles**: a file, a console, a tool (Git, kanban…), a conversation, a preview, each shown outside a zone. The detached routes prove a tool renders alone; it is the *Tab view / Tool* contribution point of the plugins.

| Mode | Unit | Rails | Like |
|---|---|---|---|
| Full | panes of tabs, tools in zones | yes | JetBrains, the current layout |
| Scroll | columns of tiles (stacked vertically), 1 / 2 / 3 in sight by width (phone / tablet / desktop) | no, or a launcher | niri, Hyprland scrolling, PaperWM |
| Grid | split tree whose leaves are tiles | no | i3, sway |

- Grid is the current `LayoutNode` tree with tools allowed as leaves.
- Scroll is the same tree read as columns: the children of a row split at the root, each a stack of tiles with its own width. Grid ↔ Scroll keeps every tile.
- Scroll at one column is what the phone layout (`state/mobile.ts`) already does; it may replace it.

## User desks

Done in #81 (see [spec.md](spec.md#9-desk-state-and-persistence)):

- Numbered `1 2 3 +` in the project bar; a desk may get a name.
- The URL carries the desk: `/project/:id/desk/2`, `/project/:id` being desk 1. A click switches the window; a middle click opens it in a new tab (a plain `<a href>`). Two windows on the same desk mirror each other, as today. The home page opens the last desk of the device.
- `Ctrl+Alt+1…9` goes to desk N, `Ctrl+Alt+Shift+N` sends the active tab or tile there (Super is not caught by a browser). A tab dragged on a desk number moves there; the tab menu has *Move to desk N*.
- Maybe: *Open on screen 2* through the Window Management API (`getScreenDetails`, Chromium), full screen on the chosen screen.

## Agent desks

- A **development session** (an ACP agent or an assistant conversation working on a ticket) gets its own desk, bound to it and to its worktree: the agent acts in that worktree and that desk only. For the Orchestrator it is open (see the questions).
- Listed apart from the numbered desks: `Claude · #42`, with the dot of the Agents tool (working, waiting, stopped). The user visits one, or opens it in another tab.
- Default layout: Full or Grid, with the conversation, the editor following the edits, and the Git tool (diff of the branch). Development agents rather want an IDE view.
- The **follow mode** acts on the editor of its desk: the window of the user is never moved.
- The user can do anything in the desk of an agent (scroll, open a file, type in its console): the agent does not see the layout, only the effects.
- **End of the session**: the desk stays with the worktree until the ticket is closed, so the user reviews it there. Closing the ticket removes the worktree and ends the desk with the same cascade.

### Tools of the agent

- The MCP server of the IDE (already given to the ACP agents in `session/new`) gets the desk in its URL (`/mcp?desk=…`): `open_file`, `focus`, `console_input`, `read_console`… act on that desk and its worktree. Only an explicit tool (`show_message`, an `ask_user`) reaches the user.
- **Consoles are an option, not a rule**: Claude Code keeps its own Bash. A console of its desk is offered for what lasts or keeps a state between calls (a dev server, a watcher, an SSH session kept open, a REPL); the user sees it in the desk.
- In the long run the agent of a desk has every tool the user has, through the same declarations as the plugins.

### Conversation as a view

- A conversation is a view that any desk may show, as a tab or a tile (a mirror of the one in its own desk): the user shows the conversations they want in their desks or in the Agents tool.
- The Orchestrator opens the child conversations in its desk this way; the Scroll mode suits it: one column per child, a new child pushes a column.

## Activity

A section of the home page, and the same list as a popup in a project:

- Per project, one **card per desk**: holder, state (working, waiting for an answer, idle), age, its **active conversations**, processes (count, CPU, memory), listening ports (link to the preview), unsaved buffers.
- **Kill levels**: *Stop the agent* (its consoles stay, e.g. the dev server it started) or *End the desk* (everything). A confirmation lists what ends: the agent process, N consoles, M commands, previews, tunnels; unsaved buffers are a question apart.
- **Orphans**: processes without a desk (the consoles of today before migration, a console whose desk closed), listed and killable.

### Cascade

- The keeper tags each process with its `owner` (the desk), next to the console id, title and kind it already carries.
- On Linux each desk is a **cgroup** (a `systemd-run --user --scope`, or a cgroup v2 made by the keeper): ending the desk kills the whole cgroup, including what left with `setsid` or `nohup` (the `npm run dev &` of an agent, the shells of its Bash). The same cgroup gives the CPU and memory of the card, and could cap a runaway agent (`MemoryMax`).
- Without systemd and on SSH projects: process groups, best effort.

## Confinement

The binding of an agent to its desk holds for the tools of the IDE only: Claude Code's own Bash, Read and Edit, and those of opencode and pi, reach the whole disk as the user. The keeper confines the agent process when it starts it, and the confinement holds for every descendant.

- **One rule per desk**: the paths it writes (its worktree, a `tmp` and caches of the desk), the paths it reads (toolchains such as `~/sdk/go`, the rest of the repository), nothing else of `~` (`~/.ssh`, `~/.config`…); the TCP ports it may listen on; no signal or ptrace towards processes outside the desk.
- **One confiner per system** translates it:

| System | Confiner | Files | Ports | Signals |
|---|---|---|---|---|
| Linux | Landlock (`go-landlock`, no root, no other user) | yes | bind TCP by port | yes (scoping) |
| macOS | Seatbelt (`sandbox-exec` with a generated profile) | yes | `network-bind` by local port | yes |
| WSL2 | Landlock, if the kernel of WSL has it | | | |
| Others, SSH projects | none | | | |

- **Best effort, shown**: the confiner applies what the kernel offers; the card of the desk says *confined*, *partial* or *not confined*.
- **Internet stays open** (npm, `go mod`, the API of the model).
- **Deferred execution** is the real hole: what the user runs later, outside any confinement. `.git/hooks` and `~/.claude` stay read-only to the agent; Claude Code gets its own `CLAUDE_CONFIG_DIR` per desk (its OAuth credentials refresh: copied in, or an API key). The files of the worktree (a `Makefile`, `package.json`, `.envrc`) are what the review reads.
- **Ports**: an MCP tool `reserve_ports(n)` takes free ports from a range for the desk, released when it ends; the consoles of the desk get `PORT`, `PORT_1`…; the card shows them and the confiner enforces them, so two worktrees no longer collide. Later, maybe: a network namespace per desk (`pasta`, rootless), each desk with its own `localhost:3000`, previews through the keeper.
- Claude Code's own sandbox, where present, adds to it at no cost.
- **Rejected for now**: a dedicated Unix user (needs root to create and sudo to switch, shared group or ACLs for the review, `safe.directory`; a git worktree must write to the `.git` of the main repository, so it would need a clone instead) and containers (a CI to build).

## Order (if decided)

1. **Ownership**: `owner` on the processes of the keeper, a cgroup per owner, the Activity list with its kills. Useful at once, without desks.
2. **User desks** in Full mode: a state per desk, URL, switcher, migration of `sessions/<project>.json` to `desks/` (the current state becomes desk 1).
3. **Agent desks**: one per development session, MCP scoped by desk, follow mode in its desk, conversation as a view, confinement and reserved ports.
4. **Tiles** (tools outside zones), then the **Grid** mode.
5. **Scroll** mode, then the phone on top of it.

## Open questions

1. **The Orchestrator**: its own desk, or the conversation shown on a desk of the user?
2. **Docker for agents**: the socket is root in all but name, so an agent with Docker escapes any confinement. None, or through a proxy of the socket limited to the Compose project of its worktree?
3. **Waiting agents**: a badge on the desk in the switcher and a push notification when an agent desk waits for an answer?
4. **Ports of the user**: the reserved ports answer for the agents; should the consoles of user desks take reserved ports too, and how they meet the [services](relay.md) shared between pods.
