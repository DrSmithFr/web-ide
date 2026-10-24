// Package engine is the engine of the assistant of the AI plugin apart from its loop: the
// conversation as stored, the system prompt, the tools offered in each mode, the Orchestrator,
// the sub-agents and the pages of the board. The loop runs in the plugin (plugins/ai), which
// owns the runtimes it works on; the tool kit it shares with the MCP is pod/assistant.
package engine

import (
	"encoding/json"

	"antennae.space/ide/pod/assistant"
)

// Mode of a conversation: Build (default) acts, Plan explores and proposes, Briefing
// questions the user and writes tickets.
const (
	Build    = "build"
	Plan     = "plan"
	Briefing = "briefing"
)

// Chat is a conversation as the page and the base store it (web/src/llm/state.ts).
type Chat struct {
	ID       string     `json:"id"`
	Title    string     `json:"title"`
	Created  int64      `json:"created"`
	Updated  int64      `json:"updated"`
	Server   string     `json:"server"`
	Model    string     `json:"model"`
	Messages []*Message `json:"messages"`
	ResetAt  int        `json:"resetAt,omitempty"`
	// Nested: the instruction files of subfolders added to the context (nested.go).
	Nested  []NestedFile    `json:"nested,omitempty"`
	Running *Running        `json:"running,omitempty"`
	Queue   []QueuedMessage `json:"queue,omitempty"`
	Mode    string          `json:"mode,omitempty"`
	Ticket  *TicketLink     `json:"ticket,omitempty"`
	// Options of the user for this conversation, sent with each message.
	Options *assistant.Options `json:"options,omitempty"`
	// Approval waiting for the user (a file change, a command).
	Approval *Approval `json:"approval,omitempty"`
	// Parent: the conversation that started this one as a sub-agent (subagents.go), and its
	// task; Children: the sub-agents this one started.
	Parent   string    `json:"parent,omitempty"`
	Agent    *SubAgent `json:"agent,omitempty"`
	Children []string  `json:"children,omitempty"`
	// Draft: a first message prepared for the user (open_conversation), put in the message
	// box of the window that opens the conversation.
	Draft string `json:"draft,omitempty"`
	// Dismissed: the user abandoned it after an error; it is no longer shown as failed (until
	// it runs again).
	Dismissed bool `json:"dismissed,omitempty"`
}

// NestedFile is the AGENTS.md (or CLAUDE.md) of a subfolder, read the first time the
// conversation touched a file there: Path is relative to the root, At the number of messages
// at that time (the system prompt carries it again once a compaction dropped that result).
type NestedFile struct {
	Path string `json:"path"`
	At   int    `json:"at"`
}

// Running is set while the assistant runs, with the stream of the completion awaited: enough
// for a pod that restarts to take the run back (plugins/ai/assistant_resume.go).
type Running struct {
	Stream string `json:"stream,omitempty"`
	// StartedAt: when the completion was asked (Unix ms); Effort: its reasoning effort.
	StartedAt int64  `json:"startedAt,omitempty"`
	Effort    string `json:"effort,omitempty"`
	// The command of the tool call running, in the keeper: a process (bash) or a console
	// (run_command), and its deadline (Unix ms).
	Proc    string `json:"proc,omitempty"`
	Console string `json:"console,omitempty"`
	Until   int64  `json:"until,omitempty"`
}

type TicketLink struct {
	ID       int64  `json:"id"`
	Role     string `json:"role"`
	Feedback int64  `json:"feedback,omitempty"`
	// Project the conversation works in (the worktree of the ticket for a development):
	// it stays there whatever worktree the window shows.
	Project string `json:"project,omitempty"`
	// Effort of the assistant chosen for the session from the complexity (auto,
	// xhigh, low): it replaces the one of the options of the user.
	Effort string `json:"effort,omitempty"`
}

// QueuedMessage is a message written during an answer, sent at the next step.
type QueuedMessage struct {
	ID          string          `json:"id"`
	Text        string          `json:"text"`
	Parts       json.RawMessage `json:"parts,omitempty"`
	Attachments json.RawMessage `json:"attachments,omitempty"`
	Display     string          `json:"display,omitempty"`
	// Event: a message of a sub-agent or of the parent, waiting for the next step.
	Event *AgentEvent `json:"event,omitempty"`
	// Author: "claude" for a message of Claude Code (MCP kanban_reply), else the user.
	Author string `json:"author,omitempty"`
}

// Message of the conversation: the API fields, then the fields of the page.
type Message struct {
	Role       string          `json:"role"`
	Content    json.RawMessage `json:"content,omitempty"`
	Reasoning  string          `json:"reasoning_content,omitempty"`
	ToolCalls  []ToolCall      `json:"tool_calls,omitempty"`
	ToolCallID string          `json:"tool_call_id,omitempty"`
	Name       string          `json:"name,omitempty"`

	Display     string          `json:"display,omitempty"`
	Attachments json.RawMessage `json:"attachments,omitempty"`
	Usage       json.RawMessage `json:"usage,omitempty"`
	Error       string          `json:"error,omitempty"`
	// Status of a tool result: ok, error, denied.
	Status string `json:"status,omitempty"`
	// Summary shown folded: a text, or a Text to translate in the page.
	Summary json.RawMessage      `json:"summary,omitempty"`
	Diff    []assistant.DiffLine `json:"diff,omitempty"`
	Model   string               `json:"model,omitempty"`
	// Finish: why the server ended the answer (stop, length, tool_calls…), for diagnosis.
	Finish    string `json:"finish,omitempty"`
	Compacted bool   `json:"compacted,omitempty"`
	// Stuck: an answer that looped in its reasoning, or ended in it without text nor action.
	Stuck bool `json:"stuck,omitempty"`
	// Resumed: a failed answer a resume went on from; shown, not sent back to the model.
	Resumed    bool   `json:"resumed,omitempty"`
	Kind       string `json:"kind,omitempty"`
	Summarized int    `json:"summarized,omitempty"`
	ThinkMs    int64  `json:"thinkMs,omitempty"`
	Effort     string `json:"effort,omitempty"` // reasoning effort of the answer
	// At: when the message was added (Unix ms; for an answer, the end of its generation).
	At int64 `json:"at,omitempty"`
	// WaitMs: time a tool call waited for the approval of the user (within its ElapsedMs).
	WaitMs int64 `json:"waitMs,omitempty"`
	// Failure of a tool call in error: usage (the call itself is wrong: a parameter missing or
	// invalid, an unknown tool…), exit (a command that ended with a code, or timed out), error.
	Failure string `json:"failure,omitempty"`
	// Term: output of a command with its colors, for the page (the model reads Content).
	// Exit: its exit code.
	Term      string               `json:"term,omitempty"`
	Exit      *int                 `json:"exit,omitempty"`
	ElapsedMs int64                `json:"elapsedMs,omitempty"`
	StartedAt int64                `json:"startedAt,omitempty"` // start of a tool call (Unix ms); its ElapsedMs is set at its end
	Mode      string               `json:"mode,omitempty"`
	Plan      string               `json:"plan,omitempty"`
	PlanState string               `json:"planState,omitempty"`
	Questions []assistant.Question `json:"questions,omitempty"`
	Answers   [][]string           `json:"answers,omitempty"`
	Notes     []string             `json:"notes,omitempty"`
	Path      []int                `json:"path,omitempty"`    // questions asked, in order (a graph)
	OffPath   *int                 `json:"offPath,omitempty"` // question where the user left the path
	AskState  string               `json:"askState,omitempty"`
	// Page drawn on the board by board_draw (tool message), and the state of the capture of
	// the screen it waits for (pending, done, refused, skipped).
	Page    *assistant.Page `json:"page,omitempty"`
	Capture string          `json:"capture,omitempty"`
	// Event of a sub-agent in its parent, or of the parent in its child (user messages of
	// kind agent_event); Wait is "parent" on the result of agent_ask until the parent answers.
	Event *AgentEvent `json:"event,omitempty"`
	Wait  string      `json:"wait,omitempty"`
	// Child: the sub-agent a tool call of the parent started or addressed.
	Child string `json:"child,omitempty"`
	// Card: an action offered by action_card (Orchestrator); Opened: the conversation
	// open_conversation moved the user into.
	Card   *assistant.ActionCard `json:"card,omitempty"`
	Opened string                `json:"opened,omitempty"`
	// Preview offered by share_preview (tool message): the card starts it on a click.
	Preview *assistant.Preview `json:"preview,omitempty"`
	// Author: "claude" for a message or the answers of Claude Code (MCP), else the user.
	Author string `json:"author,omitempty"`
}

type ToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

// Approval is an action waiting for the user: a file change (with its diff) or a command.
type Approval struct {
	ID      string               `json:"id"`
	Call    ToolCall             `json:"call"`
	Kind    string               `json:"kind"` // edit | command
	Path    string               `json:"path,omitempty"`
	Diff    []assistant.DiffLine `json:"diff,omitempty"`
	Created bool                 `json:"created,omitempty"`
	Command string               `json:"command,omitempty"`
}
