# Documentation

The words used everywhere are defined in the [glossary](../GLOSSARY.md).

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
- Drafts under discussion: [Desks](design/desks.md), [Plugins](design/plugins.md).
