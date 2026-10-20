# Documentation

The words used everywhere are defined in the [glossary](../GLOSSARY.md).

These pages are also in the IDE, embedded in the pod: *Help › Documentation* (or the **?** next to a setting, the kanban, the Agents tool) opens them read only in a tab, matching the version you run, with no network.

## Install

- [Linux](install/linux.md): the pod as a unit started at boot, optionally on your Tailscale network.
- [macOS](install/macos.md): the release archive, run by hand.
- [Windows with WSL](install/windows-wsl.md): WSL 2, systemd, a task at logon, NAT.
- [From the sources](install/from-sources.md): build, run, install as a unit.

## How-to

- [Account and devices](howto/account-and-devices.md): the recovery phrase, pairing browsers and pods, revocation, settings sync and the vault.
- [Pods on the local network](howto/lan-pods.md): a cluster of pods with no relay, one entry point for all.
- [Self-host a relay](howto/self-host-relay.md): reach your pods from anywhere through your own relay.
- [Notifications on the phone](howto/notifications.md).
- [Add a language](howto/add-translation.md): the interface and the pod's messages.
- [Modify the IDE](howto/modify-the-ide.md): change the installed version with its own assistant.

## Guide

- [User guide](guide.md): projects, models, agents, and a change from an idea to a merged branch.

## Design

How the IDE is built and why, kept up to date with the code.

- [Specification](design/spec.md): what the IDE does, feature by feature.
- [Architecture](design/architecture.md): the system map, the code layout, the protocol, the pitfalls.
- [Kanban](design/kanban.md): tickets, lineages, the git workflow, the MCP tools.
- [Relay](design/relay.md): identity, multi-pod, transport, the hosted service.
- [Load](design/load.md): what a relay costs per session.
- [Doodle](design/doodle.md): drawings attached for the assistant.
- [Plugins](design/plugins.md): the plugin system, phase 1 decided.
- Drafts under discussion: [Desks](design/desks.md).

## Code

A README with a Mermaid map in each area of the code, for humans and AIs.

- [pod](../pod/README.md): the pod, `main.go`, and the map of its five blocks.
- [pod](../pod/packages.md): every Go package, by block (server and protocol, files and processes, AI, kanban and git, network and identity).
- [web](../web/README.md) and [web/src](../web/src/README.md): the front end, one folder per concern.
- [e2e](../e2e/README.md): the browser tests and their suites.
