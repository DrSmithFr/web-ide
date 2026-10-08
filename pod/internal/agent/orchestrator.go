package agent

// Orchestrator mode: the default of a new conversation. It steers the work instead of doing
// it: it reads the project and the kanban, proposes actions as cards the user clicks, sends
// the user into the right conversation and delegates research to sub-agents (whose children
// may have their own: depth 2). It changes no file.

const Orchestrator = "orchestrator"

// MaxOrchestratedDepth: under an Orchestrator, a child may start children of its own.
const MaxOrchestratedDepth = 2

// ActionKinds are the actions of action_card, run by the page on the user's click.
var ActionKinds = []string{"start_dev", "open_ticket", "generate_plan", "open_conversation"}

const defaultOrchestratorTemplate = `You are the programming assistant built into a web IDE, in **Orchestrator mode**. Open project: "{{project}}", root {{root}}{{host}}. Today: {{date}}.
{{activeFile}}
In Orchestrator mode you steer the work of the day; you do not write code and change no file. You:
- tell what to work on next: kanban_next lists the tickets that can start (in order) and those waiting for the user (to test, feedback to handle); answer briefly why, then propose the action with action_card (start_dev, generate_plan, open_ticket) and maybe one or two alternatives;
- tell what was done: kanban_history lists the tickets that moved in a period (yesterday by default), closed ones first;
- send the user to the right conversation: an idea to clarify goes to a Briefing conversation with open_conversation (mode briefing, the idea as message, send true); a conversation of the project (list_conversations) is opened with open_conversation (chat);
- delegate long research to sub-agents (spawn_agent), which may delegate in turn.
Use the tools rather than guessing. Actions run only when the user clicks their card: never claim that you started or opened something before they did. Keep answers short.
Answer in the language of the user, in Markdown.

{{tools}}`

const orchestratorToolsText = `Reading tools: list_dir, find_files, read_file, search_text, the language servers (lsp_*), bash for reading commands (ls, grep, git log…), which run freely; any other command asks the user first. edit_file and write_file are not available in Orchestrator mode.
Kanban: kanban_list, kanban_get, kanban_next (what can start now, in order, and what waits for the user), kanban_history (what moved in a period). Conversations: list_conversations, open_conversation (moves the user into a new or existing conversation), agent_adopt (follow a conversation that runs on its own, a development started from a card: it becomes your sub-agent, tells you where it is and reports), agent_resume (resume a failed conversation from its last completed step, adopting it on the way). Actions: action_card (a button the user clicks: start the development of a ticket, generate its plan, open it, open a conversation). ask_user asks the user questions when a choice is theirs.
When the conversation gets long, you can summarize it with compact_conversation.
` + batchText

var (
	kanbanNextDef = fn("kanban_next", "Lists the tickets that can start now, in the order to take them (next step of a lineage in progress, priority, smaller size, age), then the tickets waiting for the user (to test, feedback to handle).", obj{})
	kanbanHistDef = fn("kanban_history", "Lists the tickets that moved in a period, with their moves (closed ones first). Default: yesterday 00:00 to now.",
		obj{
			"from": str(`Start, "YYYY-MM-DD" or "YYYY-MM-DD HH:MM" (local time); default yesterday`),
			"to":   str(`End (excluded), same format; default now`),
		})
	listConvsDef = fn("list_conversations", "Lists the conversations of the project (most recent first): id, title, mode, linked ticket, state, sub-agents.",
		obj{"query": str("Text to find in the titles"), "limit": integer("At most this many (default 20)")})
	actionCardDef = fn("action_card", "Shows the user a button for an action; nothing runs until they click it. Kinds: start_dev (start the development of a ticket), generate_plan (have the plan of a ticket written), open_ticket (open the ticket), open_conversation (open a conversation, chat).",
		obj{
			"kind":   enum("The action", ActionKinds...),
			"ticket": integer("Ticket number (start_dev, generate_plan, open_ticket)"),
			"chat":   str("Conversation id (open_conversation)"),
			"label":  str("Text of the button, in the language of the user"),
			"reason": str("One short sentence: why this action"),
		}, "kind", "label")
	openConvDef = fn("open_conversation", "Moves the user into a conversation: an existing one (chat), or a new one in a mode (briefing to clarify an idea, plan, build), maybe linked to a ticket (briefing or plan). message is the first message: sent at once with send, else left in the message box for the user.",
		obj{
			"chat":    str("Id of an existing conversation to open"),
			"mode":    enum("Mode of a new conversation", Briefing, Plan, Build),
			"title":   str("Title of a new conversation"),
			"ticket":  integer("Ticket to link a new briefing or plan conversation to"),
			"message": str("First message, written for the user (their idea, a request)"),
			"send":    boolean("Send the message at once (the conversation starts)"),
		})
	adoptDef = fn("agent_adopt", "Adopts a conversation of the project that runs on its own (a development started from a card…): it becomes one of your sub-agents, keeps working with the user, announces itself with a note and sends you its report when its task is over.",
		obj{"chat": str("Id of the conversation (list_conversations)")}, "chat")
	resumeDef = fn("agent_resume", "Resumes a conversation whose last answer failed (list_conversations marks it failed), from its last completed step, like the Resume button of the user. One that runs on its own is adopted on the way: it becomes your sub-agent.",
		obj{"chat": str("Id of the failed conversation")}, "chat")
	orchestratorDefs = []Def{kanbanNextDef, kanbanHistDef, listConvsDef, actionCardDef, openConvDef, adoptDef, resumeDef}
)

// OrchestratorTools are handled by the server (agent_orchestrator.go).
var OrchestratorTools = names(orchestratorDefs)
