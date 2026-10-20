# Kanban plugin

The Kanban tool, the board, ticket, milestone and objective views. It provides `tickets` and requires `terminal` and `worktrees`. A built-in plugin: [plugins.md](../../docs/design/plugins.md); `plugin.json` is its manifest.

- **Pod** (`*.go`, package `kanban`): The `kanban.*` methods: tickets, milestones, objectives, the git workflow of a ticket and its agent session. The services of the core come through `Pod().Services()`; `exports.go` is what the assistant, the Orchestrator and the MCP call (their `kanban_*` tools are the AI's and the MCP's, offered only with `tickets` on). The store is `pod/kanban`.
- **Page** (`web/`, the chunk `plugin-kanban`): The views, whose number is their tab state; workflow actions, linked conversations, the New ticket host; its status bar items `status.ts`. It starts the conversations and agent sessions through the `assistant` and `agents` services of AI.
- **E2e** (`e2e/`, run by `e2e/run.sh`): kanban, kanbanagents, kanbanai, kanbangit, milestones.

The design is [kanban.md](../../docs/design/kanban.md).

The page module imports the plugin API only (`@ide/api`, `@ide/types`, `@ide/capabilities`); the Go package imports the core of `pod/`.

Still coupled until #135: AI imports the `exports.go` of Kanban, and `pod/server` imports both (the MCP tools, the replies of Claude Code).
