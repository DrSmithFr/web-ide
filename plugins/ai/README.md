# AI plugin

The Assistant, the Agents tool, doodles and the board of a conversation. It provides the `assistant` and `agents` capabilities. A built-in plugin: [plugins.md](../../docs/design/plugins.md); `plugin.json` is its manifest.

- **Pod** (`*.go`, package `ai`): The `assistant.*`, `llm.*`, `agents.*` and `models.*` methods: the turn loop and its tools, sub-agents, the Orchestrator, compaction, the board, the Agents tool. The shared parts are lent by the pod: the interface calls of a window `Pod.UICall`, the saved commands, the previews, the tools of the other plugins, the questions, the push notifications. `mcp.go` the MCP tools on the conversations linked to a ticket (`kanban_conversation`, `kanban_reply`, `kanban_answer`, through `Host.MCPTool`; Claude Code's replies); `notify.go` the push notifications of a conversation; `exports.go` what the tests of `pod/server` read; the conversations that ran are taken back on `OnStart`, saved on `OnStop`. The model servers and conversations are `pod/llm`, the agents `pod/acp`, the tool kit shared with the MCP `pod/assistant`.
- **Page** (`web/`, the chunk `plugin-ai`): `llm/` the Assistant tool: state, turn loop, tools, prompt, the editor of the message box shared with the Agents tool `MessageBox.tsx`, attachments, speech recognition, sub-agents `SubAgents.tsx`, doodles `llm/doodle/`, the board of a conversation `llm/board/`, app previews `PreviewCard.tsx`, its interface tools `uiTools.ts`; `agents/` the Agents tool: sessions of the worktrees of the window and their threads, tabs, launcher, permission cards, the follow mode; `plugin.ts` the gateway, whose `on` keeps the events subscribed before the activation.
- **E2e** (`e2e/`, run by `e2e/run.sh`): agent, agents, chat, doodle, llm, orchestrator, plan, subagents, speech.

Its unit tests run on a fake pod (`harness_test.go`); those through the RPC of a whole pod are in `pod/server`.

The page module imports the plugin API only (`@ide/api`, `@ide/types`, `@ide/capabilities`); the Go package imports the core of `pod/`.

AI imports the Kanban plugin (`exports.go`, `TicketTool`, the manager `K.Kanban`) and its package `kanban/`; `pod/server` imports neither.
