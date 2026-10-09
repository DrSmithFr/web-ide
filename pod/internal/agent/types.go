// Package agent holds what the agent of the assistant needs that does not depend on the
// server: the conversation as stored, the system prompt, the tool definitions, the rules of
// the Plan mode for commands, the diff of a file change and the questions of ask_user. The
// loop itself runs in the server (server/agent*.go), which owns the runtimes and the kanban.
package agent

import "encoding/json"

// Mode of a conversation: Build (default) acts, Plan explores and proposes, Briefing
// questions the user and writes tickets.
const (
	Build    = "build"
	Plan     = "plan"
	Briefing = "briefing"
)

// Chat is a conversation as the page and the base store it (web/src/llm/state.ts).
type Chat struct {
	ID       string          `json:"id"`
	Title    string          `json:"title"`
	Created  int64           `json:"created"`
	Updated  int64           `json:"updated"`
	Server   string          `json:"server"`
	Model    string          `json:"model"`
	Messages []*Message      `json:"messages"`
	ResetAt  int             `json:"resetAt,omitempty"`
	Running  *Running        `json:"running,omitempty"`
	Queue    []QueuedMessage `json:"queue,omitempty"`
	Mode     string          `json:"mode,omitempty"`
	Ticket   *TicketLink     `json:"ticket,omitempty"`
	// Options of the user for this conversation, sent with each message.
	Options *Options `json:"options,omitempty"`
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

// ActionCard is an action the page runs when the user clicks it; State and Result once done.
type ActionCard struct {
	Kind   string `json:"kind"`
	Ticket int64  `json:"ticket,omitempty"`
	Chat   string `json:"chat,omitempty"`
	Label  string `json:"label"`
	Reason string `json:"reason,omitempty"`
	State  string `json:"state,omitempty"` // done | failed
	Result string `json:"result,omitempty"`
}

// Running is set while the agent runs, with the stream of the completion awaited.
type Running struct {
	Stream string `json:"stream,omitempty"`
}

type TicketLink struct {
	ID       int64  `json:"id"`
	Role     string `json:"role"`
	Feedback int64  `json:"feedback,omitempty"`
	// Project the conversation works in (the worktree of the ticket for a development):
	// it stays there whatever worktree the window shows.
	Project string `json:"project,omitempty"`
}

// Options are the preferences of the page that change how the agent runs.
type Options struct {
	AutoApply bool  `json:"autoApply,omitempty"`
	Think     *bool `json:"think,omitempty"`
	// Effort: the reasoning effort, xhigh, medium or low; "" or "auto" lets the agent choose
	// at each step. EffortTool offers set_effort to the model in that dynamic mode.
	Effort      string `json:"effort,omitempty"`
	EffortTool  bool   `json:"effortTool,omitempty"`
	Tools       *bool  `json:"tools,omitempty"`
	AutoCompact *bool  `json:"autoCompact,omitempty"`
	CompactAt   int    `json:"compactAt,omitempty"`
	CompactSrv  string `json:"compactServer,omitempty"`
	CompactMdl  string `json:"compactModel,omitempty"`
	PlanServer  string `json:"planServer,omitempty"`
	PlanModel   string `json:"planModel,omitempty"`
	// ActiveFile: the file shown in the editor of the window that sent the message.
	ActiveFile string `json:"activeFile,omitempty"`
	// DockerProfiles: the Compose profiles active in the Docker tool of the window.
	DockerProfiles []string `json:"dockerProfiles,omitempty"`
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
	Summary    json.RawMessage `json:"summary,omitempty"`
	Diff       []DiffLine      `json:"diff,omitempty"`
	Model      string          `json:"model,omitempty"`
	Compacted  bool            `json:"compacted,omitempty"`
	Kind       string          `json:"kind,omitempty"`
	Summarized int             `json:"summarized,omitempty"`
	ThinkMs    int64           `json:"thinkMs,omitempty"`
	Effort     string          `json:"effort,omitempty"` // reasoning effort of the answer
	ElapsedMs  int64           `json:"elapsedMs,omitempty"`
	StartedAt  int64           `json:"startedAt,omitempty"` // start of a tool call (Unix ms); its ElapsedMs is set at its end
	Mode       string          `json:"mode,omitempty"`
	Plan       string          `json:"plan,omitempty"`
	PlanState  string          `json:"planState,omitempty"`
	Questions  []Question      `json:"questions,omitempty"`
	Answers    [][]string      `json:"answers,omitempty"`
	Notes      []string        `json:"notes,omitempty"`
	Path       []int           `json:"path,omitempty"`    // questions asked, in order (a graph)
	OffPath    *int            `json:"offPath,omitempty"` // question where the user left the path
	AskState   string          `json:"askState,omitempty"`
	// Page drawn on the board by board_draw (tool message), and the state of the capture of
	// the screen it waits for (pending, done, refused, skipped).
	Page    *Page  `json:"page,omitempty"`
	Capture string `json:"capture,omitempty"`
	// Event of a sub-agent in its parent, or of the parent in its child (user messages of
	// kind agent_event); Wait is "parent" on the result of agent_ask until the parent answers.
	Event *AgentEvent `json:"event,omitempty"`
	Wait  string      `json:"wait,omitempty"`
	// Child: the sub-agent a tool call of the parent started or addressed.
	Child string `json:"child,omitempty"`
	// Card: an action offered by action_card (Orchestrator); Opened: the conversation
	// open_conversation moved the user into.
	Card   *ActionCard `json:"card,omitempty"`
	Opened string      `json:"opened,omitempty"`
	// Preview offered by share_preview (tool message): the card starts it on a click.
	Preview *Preview `json:"preview,omitempty"`
	// Author: "claude" for a message or the answers of Claude Code (MCP), else the user.
	Author string `json:"author,omitempty"`
}

// Preview is an app the user can start and open from the conversation (see the preview
// package); Project is the runtime of the run (the worktree of a development).
type Preview struct {
	Project string `json:"project"`
	Title   string `json:"title"`
	Command string `json:"command"`
	Cwd     string `json:"cwd,omitempty"`
	Port    int    `json:"port"`
}

// Page drawn by the model on the board of the conversation: its document (a doodle, see
// web/src/llm/doodle/model.ts), the description and the images made by the window that
// drew it.
type Page struct {
	Name        string          `json:"name"`
	Doc         json.RawMessage `json:"doc"`
	Description string          `json:"description"`
	Thumb       string          `json:"thumb,omitempty"`
	PNG         string          `json:"png,omitempty"`
}

type ToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

// Text is a text of the interface written by the pod and translated by the page:
// t(Key, Params), or tn(N, Key, Other, Params) when Other is set.
type Text struct {
	Key    string         `json:"key"`
	Other  string         `json:"other,omitempty"`
	N      int            `json:"n,omitempty"`
	Params map[string]any `json:"params,omitempty"`
	// Prefix and Suffix are shown as they are around the translated text (a path…).
	Prefix string `json:"prefix,omitempty"`
	Suffix string `json:"suffix,omitempty"`
}

// With puts a plain text before and after the translated one.
func (t Text) With(prefix, suffix string) Text {
	t.Prefix, t.Suffix = prefix, suffix
	return t
}

// T is a text to translate; Tn its plural form (Key for one, Other for several).
func T(key string, params map[string]any) Text { return Text{Key: key, Params: params} }
func Tn(n int, key, other string, params map[string]any) Text {
	return Text{Key: key, Other: other, N: n, Params: params}
}

// Raw is the JSON of a summary: a Text, or a plain string (paths, commands…).
func (t Text) Raw() json.RawMessage {
	data, _ := json.Marshal(t)
	return data
}

// Plain is a summary that needs no translation.
func Plain(s string) json.RawMessage {
	data, _ := json.Marshal(s)
	return data
}

// DiffLine of a file change: ' ' context, '+' added, '-' removed, '…' a gap (Text is then
// a key to translate with N: "line {n}").
type DiffLine struct {
	T    string `json:"t"`
	Text string `json:"text"`
	N    int    `json:"n,omitempty"`
}

// Approval is an action waiting for the user: a file change (with its diff) or a command.
type Approval struct {
	ID      string     `json:"id"`
	Call    ToolCall   `json:"call"`
	Kind    string     `json:"kind"` // edit | command
	Path    string     `json:"path,omitempty"`
	Diff    []DiffLine `json:"diff,omitempty"`
	Created bool       `json:"created,omitempty"`
	Command string     `json:"command,omitempty"`
}

// Question asked with ask_user. Its type picks how the page shows it; an id and the next
// of the options make a small graph of questions (ask.go).
type Question struct {
	Question  string   `json:"question"`
	ID        string   `json:"id,omitempty"`
	Header    string   `json:"header,omitempty"`
	Type      string   `json:"type,omitempty"`      // choice (default) | idea | compare | rank | scenario
	Situation string   `json:"situation,omitempty"` // scenario: the concrete case
	Top       int      `json:"top,omitempty"`       // rank: only the top N is picked
	Options   []Option `json:"options"`
	Multiple  bool     `json:"multiple,omitempty"`
	NextYes   string   `json:"nextYes,omitempty"` // idea: asked after Yes or Exactly
	NextNo    string   `json:"nextNo,omitempty"`  // idea: asked after No
}

type Option struct {
	Label       string   `json:"label"`
	Description string   `json:"description,omitempty"`
	Pros        []string `json:"pros,omitempty"`
	Cons        []string `json:"cons,omitempty"`
	Next        string   `json:"next,omitempty"` // id of the question asked when it is chosen
}

// ContentText is the text of a content: the string, or its text parts joined.
func ContentText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	_ = json.Unmarshal(raw, &parts)
	out := ""
	for _, p := range parts {
		if p.Type == "text" {
			if out != "" {
				out += "\n"
			}
			out += p.Text
		}
	}
	return out
}

// String is the JSON of a string content.
func String(s string) json.RawMessage { return Plain(s) }
