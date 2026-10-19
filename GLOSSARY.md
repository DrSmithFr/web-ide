# Glossary

The words of Web IDE, one meaning each. Docs, code, UI texts and AI agents use them as defined here; *Avoid* lists the words not to use for the same thing. No implementation details: those live in the READMEs of the code and in [docs/design](docs/).

## Machines, network, identity

**Pod**
The Web IDE program on one machine: it serves the interface to the browser and does the work there (files, git, terminals, models).
_Avoid_: server, host, agent

**Keeper**
A second program next to the pod that holds the terminals and the model answers being written, so the pod restarts without losing them.
_Avoid_: daemon

**Unit**
A pod or keeper installed as a systemd user unit, started at boot.
_Avoid_: service

**Relay**
An optional server that introduces browsers and pods across networks; it carries only signaling, TURN and encrypted data. Hosted at antennae.space, or self-hosted.
_Avoid_: proxy, server

**Level**
What an account gives the project, which sets its TURN quota on the relay: free, Backer, Supporter or VIP, from its GitHub sponsorship and the VIP list. The best live one counts; they do not add up.
_Avoid_: tier (a tier is the price on GitHub Sponsors, a level is what the relay grants), plan, supporter (for the level itself)

**Backer**
The level of a $2 monthly sponsor: the TURN quota is raised (20 GB on the hosted relay).

**Supporter**
The level of a $5 monthly sponsor, or of $5 paid once (for 30 days): no TURN quota (fair use).
_Avoid_: sponsor (for the level: a sponsor is the person on GitHub, at any level)

**VIP**
The level of a $10 monthly sponsor and above, of $10 or $50 paid once (90 or 360 days), of a person on the VIP list, or of a member of a team within its seats: no TURN quota, and the VIP badge.

**Team**
The tier of a monthly sponsorship of $50 or more, by a person or an organization: the sponsor's team, whose admin adds members who become VIPs. The owner takes no seat.
_Avoid_: company, organization (an organization is one kind of owner)

**Seat**
A place in a team: one per $5 of the monthly sponsorship ($50 is 10 seats).

**VIP list**
The people the admin of a relay makes VIPs by hand (`web-ide-pod relay vip`), by GitHub login or by account id, with an optional end and a note only the admin sees.

**Tunnel**
The encrypted WebRTC connection between a browser and a pod, or between two pods, set up through the relay.
_Avoid_: relay link, channel

**Port forward**
A local port that reaches an address seen from the SSH host of a project.
_Avoid_: tunnel, SSH tunnel
The RPC methods and the event of the Port forwards tool are still `tunnels.*`, and `.ide/tunnels.json` is read when `forwards.json` is missing (older pages and projects).

**Service**
An HTTP service of a pod's machine (Ollama, a web UI) shared with the other pods of the account, which reach it on a port of their own machine.
_Avoid_: share, shared port

**Account**
The identity shared by a user's members: a key derived from a recovery phrase.
_Avoid_: user, login

**Recovery phrase**
The 24 words the account key comes from; shown once, never stored.
_Avoid_: seed, mnemonic, password

**Device**
A browser holding its own key, certified by the account.
_Avoid_: client, machine; a pod is not a device

**Member**
A device or a pod of the account.
_Avoid_: device (for a pod), peer, node
On the wire and in storage a member is still called a device (the relay's `devices` message and `device` fields, `device.key`, `identity.json`, the text `device revoked`), so that releases keep talking; each such name says so in a comment.

**Pairing**
Adding a browser or a pod to an account: the recovery phrase typed, or an invite approved by a device already in it.
_Avoid_: enrolment, join (as a noun)

**Access token**
The secret of the first link of a pod (`?token=`) that opens it in a browser, with no account.
_Avoid_: pairing token

**Revocation**
A signed list of the members the account no longer accepts, spread by the relay and from pod to pod.
_Avoid_: ban

**Cluster**
The pods of one account that see each other on the local network.
_Avoid_: site, LAN
On the wire and in storage the cluster is still called `site` (the relay protocol fields and the `site` message, the `/api/pods` field, the `webide.relay.site` key of the bootstrap), so that a relay and pods of different releases keep talking; each such field says so in a comment.

**Gateway**
The pod of a cluster the relay sends browsers to; the role moves when it leaves.
_Avoid_: master, leader

**Vault**
The secrets synced between the pods of the account (API keys, database passwords), off by default and encrypted.
_Avoid_: keychain

**Settings sync**
The pods of one account share their settings through the relay, which stores only ciphertext.

## Projects and the window

**Project**
A folder, local or on an SSH host, opened in the IDE, with its own kanban, conversations and project commands.
_Avoid_: repo, workspace

**Projects folder**
The folder where a pod makes new projects (`~/Apps` by default).
_Avoid_: workspace
The flag `-workspace` and the `workspace` key of `config.json` are still read, and so are the RPCs `workspace.get`, `workspace.set` and `workspace.dirs` (with the `workspace` field of the answer) that pods and pages of different releases call.

**Worktree**
A git worktree of a project, made for a ticket in development; it shares the kanban and the conversations of its project.
_Avoid_: branch, clone

**Desk**
One arrangement of a project's window: its layout mode, tabs, consoles and previews. A project has one or more.
_Avoid_: workspace, session
The pod stores the state of a desk in `desks/` (it was `sessions/`, read and moved on first use); on the wire it is still called `session` (`session.update`, `session.changed`), so that a page and a pod of different releases keep talking.

**Tool**
A part of the window shown from a rail icon: Explorer, Git, Kanban, Console…
_Avoid_: panel, view; for what a model calls, see *Tool call*

**Rail** / **Tool zone**
The icon bars on both sides of the window / the four corners where the tools show.
_Avoid_: sidebar

**Console**
A tab of the Console tool: a **terminal** (an interactive shell) or a **task** (one command running).

**Project command**
A command saved in a project (`serve`, `test`, `setup`…), run as a task.
_Avoid_: script, saved command

**Preview**
A web app of a project shown through the pod, openable on any device.

**Buffer**
The text of an open file, shared live by every window and AI.
_Avoid_: document

**Plugin** ([plugins](docs/design/plugins.md))
A part of the IDE enabled per project, declared by a manifest: tools, tab views, assistant tools, commands, and an optional backend in the pod.
_Avoid_: extension, add-on, module

**Built-in plugin**
A plugin that ships compiled into the IDE, with the same manifest and API as an external one.

**Contribution**
What a plugin adds at one contribution point of the IDE: a tool, a tab view, an assistant tool, a command, a settings section…

**Locked plugin**
A built-in plugin the IDE cannot run without (Files, Code): always enabled, shown greyed in Settings → Plugins.

**Diff source**
What gives the diff view the two sides of a diff tab (Git: the working tree, the index, HEAD, a commit).

**Search provider**
What gives Search Everywhere one kind of results (files, symbols, actions, text…) and its tab: the core or a plugin.

**Capability**
A named feature a plugin or the core provides and others require (`terminal`, `worktrees`), with the service that does it: the consoles of `terminal`.
_Avoid_: service (an HTTP service shared between pods), dependency (a ticket waiting for another)

**Interface tool**
A tool of the MCP or of the assistant that a window of the IDE runs for the pod (`ui.call`): open a file or a diff, focus a panel, show a message; a plugin adds its own (the AI: a page of its board).

## AI

**AI**
Any model at work in a project: the assistant or an agent.

**Model server**
An endpoint that serves models: llama.cpp, Ollama, an OpenAI-compatible cloud.
_Avoid_: backend, provider

**Assistant**
The AI built into the IDE: it runs in the pod against a model server, in the Assistant tool.
_Avoid_: AI assistant, integrated assistant, local assistant, agent

**Agent**
A coding agent program (Claude Code, opencode, pi) the IDE runs through ACP, in the Agents tool.
_Avoid_: CLI agent, external agent; for the assistant

**Conversation**
A thread of the assistant: the messages, its tool calls and its mode.
_Avoid_: chat, session

**Session**
A run of an agent, from its start to its end, with its thread.
_Avoid_: conversation; for the state of a desk

**Mode**
How the assistant works in a conversation: Orchestrator, Build, Plan, Briefing.
_Avoid_: persona

**Tool call**
A function an AI calls (read a file, run a command, an MCP tool of the IDE) and its result.
_Avoid_: tool

**Instructions**
The files a project gives every AI: `AGENTS.md` at the root and in its folders.
_Avoid_: memory, rules

**Skill**
A folder with a `SKILL.md`, loaded by an AI when its description matches the task.

**Sub-agent**
A child run the assistant starts for a side task, with its own context.
_Avoid_: worker

**Compaction**
Replacing the start of a long conversation with a summary to free context.

**Question**
Something an AI asks the user and waits for, gathered in the Questions tool.
_Avoid_: prompt

## Kanban

**Ticket**
A unit of work of a project's kanban, from its description to its merge. Its tile on the board is its **card**.
_Avoid_: issue, task

**Status**
Where a ticket stands: New → To do → In progress → To test (`to_test`) → Done, or Abandoned.
_Avoid_: column, stage

**Briefing**
Turning an idea into tickets: the assistant in Briefing mode questions the user and writes them.

**Plan**
How a ticket will be done, written before its development.
_Avoid_: spec

**Goal**
One checkable result a ticket must reach.
_Avoid_: criterion, acceptance

**Feedback**
A remark made while testing a ticket (info, bug, feature), checked once handled.
_Avoid_: comment, review

**Note**
A decision recorded on a ticket.
_Avoid_: comment, report

**Lineage**
A ticket, its **root**, and its **steps**, developed one after the other in the root's worktree and merged together.
_Avoid_: epic, chain, children

**Dependency**
A ticket of another lineage that must be merged before this one starts.
_Avoid_: blocker, prerequisite

**Complexity**
How hard a ticket's work is (low, medium, high); it picks the model and the effort of its conversations and sessions.
_Avoid_: difficulty

**Size**
How much work a ticket holds, S to XL.
_Avoid_: estimate

**Milestone**
The tickets of one feature, across lineages, with its spec, its user stories and its feedback; always validated by the user.
_Avoid_: epic, release, sprint

**User story**
What a user can do once a milestone is done; checked by the user, and the base of its e2e coverage review.

**Objective**
A named deadline (MVP, a launch…) in an ordered list: the tickets of several milestones and lone ones due together; reached by the user once they are finished. A milestone says what, an objective says when.
_Avoid_: release, target, goal, version

**Review**
A reading of a ticket's change by a model, before its merge.
_Avoid_: test, To test

## Planned

Terms of work not merged yet; each one moves to its section, or goes, when its ticket is merged.

**Referent** (#34)
The conversation a ticket's development asks when the plan does not answer: by default the one that wrote the plan.
_Avoid_: planner, supervisor

**Worktree holder** (#46)
The step of a lineage whose development currently uses the lineage's worktree.

**Agent desk** ([desks](docs/design/desks.md))
A desk bound to an agent's session or a development conversation and its worktree; the AI acts there only.

**Activity** ([desks](docs/design/desks.md))
The list of the desks of every project with what runs in them, and the way to end them.

## Conflicts

None open: every conflict found while writing this glossary was fixed (#96-#100). A naming problem found later goes here, then to a ticket.

