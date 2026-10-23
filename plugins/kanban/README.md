# Kanban plugin

The Kanban tool, the board, ticket, milestone and objective views. It provides `tickets` and requires `terminal` and `worktrees`. A built-in plugin: [plugins.md](../../docs/design/plugins.md); `plugin.json` is its manifest.

- **Pod** (`*.go`, package `kanban`): The `kanban.*` methods: tickets, milestones, objectives, the git workflow of a ticket and its agent session. The services of the core come through `Pod().Services()`; `exports.go` is what the AI calls (the `kanban_*` tools of its assistant, the Orchestrator, its MCP conversation tools through `TicketTool`); `mcp.go` the ticket tools of the MCP (`Host.MCPTool`, listed in `mcpTools` of the manifest); `setup.go` the migration of the old setup setting into a saved command. The manager of the tickets is the plugin's (`K.Kanban`), made in `Register` and closed on `OnStop`. The store is the package `kanban` of `kanban/` (tickets, lineages, milestones, objectives, the git of a ticket), not `internal/` since AI uses its types.
- **Page** (`web/`, the chunk `plugin-kanban`): The views, whose number is their tab state; workflow actions, linked conversations, the New ticket host; its status bar items `status.ts`. It starts the conversations and agent sessions through the `assistant` and `agents` services of AI.
- **E2e** (`e2e/`, run by `e2e/run.sh`): kanban, kanbanagents, kanbanai, kanbangit, milestones.

The design is [kanban.md](../../docs/design/kanban.md).

The page module imports the plugin API only (`@ide/api`, `@ide/types`, `@ide/capabilities`); the Go package imports the core of `pod/`.

AI imports the `exports.go` of Kanban and its package `kanban/` (the types of the tickets); no package of `pod/` imports either.
