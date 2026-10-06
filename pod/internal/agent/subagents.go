package agent

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Sub-agents: a conversation delegates a task to a child conversation, with a fresh context
// and the same rights, that runs in the background. They talk only through tools; every
// exchange is a message of one of the two conversations (docs/spec.md, assistant).

const (
	// MaxDepth: a child cannot start children of its own.
	MaxDepth = 1
	// MaxChildren running (not ended) at once per conversation.
	MaxChildren = 5
	// MaxChildQuestions a child may ask its parent.
	MaxChildQuestions = 10
)

// Status of a child conversation.
const (
	AgentRunning       = "running"
	AgentWaitingParent = "waiting_parent"
	AgentDone          = "done"
	AgentBlocked       = "blocked"
	AgentStopped       = "stopped"
	AgentError         = "error"
)

// AgentEnded tells whether a child has finished (it no longer runs nor waits).
func AgentEnded(status string) bool {
	return status == AgentDone || status == AgentBlocked || status == AgentStopped || status == AgentError
}

// SubAgent is what a child conversation knows of its task.
type SubAgent struct {
	Task   string   `json:"task"`
	Files  []string `json:"files,omitempty"`
	Status string   `json:"status"`
	Depth  int      `json:"depth"`
	// Note: the latest note of the child (title, text).
	Note     *AgentNote `json:"note,omitempty"`
	Question string     `json:"question,omitempty"` // open question to the parent
	Asked    int        `json:"asked,omitempty"`    // questions asked so far
	Report   string     `json:"report,omitempty"`
	Changed  []string   `json:"changed,omitempty"` // files changed, from the report
	Error    string     `json:"error,omitempty"`
	// Tokens read and written, and cost (when the provider tells it), over all its requests.
	Tokens int     `json:"tokens,omitempty"`
	Cost   float64 `json:"cost,omitempty"`
	Nudged   bool       `json:"nudged,omitempty"` // told once to end with agent_report
}

type AgentNote struct {
	Title string `json:"title"`
	Text  string `json:"text"`
}

// AgentEvent is a message of a child in its parent (note, question, report, end), or of the
// parent in its child (message).
type AgentEvent struct {
	Child  string   `json:"child"`
	Title  string   `json:"title"` // of the child conversation
	Type   string   `json:"type"`  // note | question | report | message
	Head   string   `json:"head,omitempty"`
	Text   string   `json:"text"`
	Status string   `json:"status,omitempty"` // report: done | blocked | stopped | error
	Files  []string `json:"files,omitempty"`
	// From: the user wrote it (a message to a child from its thread), not the parent.
	From string `json:"from,omitempty"`
}

// EventText is what the model reads of an event.
func EventText(e AgentEvent) string {
	who := fmt.Sprintf("Sub-agent %s (%q)", e.Child, e.Title)
	switch e.Type {
	case "note":
		return fmt.Sprintf("[%s, note] %s\n%s", who, e.Head, e.Text)
	case "question":
		return fmt.Sprintf("[%s asks] %s\nAnswer with agent_reply (child %s); if the answer is the user's, ask them first with ask_user.", who, e.Text, e.Child)
	case "report":
		s := fmt.Sprintf("[%s, report: %s]\n%s", who, e.Status, e.Text)
		if len(e.Files) > 0 {
			s += "\nFiles changed: " + strings.Join(e.Files, ", ")
		}
		return s
	case "message":
		if e.From == "user" {
			return "[Message of the user] " + e.Text
		}
		return "[Message of the parent conversation] " + e.Text
	}
	return e.Text
}

// TaskText is the first message of a child.
func TaskText(task string, files []string) string {
	s := "Task given by the parent conversation:\n\n" + strings.TrimSpace(task)
	if len(files) > 0 {
		s += "\n\nFiles to read first: " + strings.Join(files, ", ")
	}
	return s
}

// SubAgentText ends the system prompt of a child.
const SubAgentText = `# You are a sub-agent
Another conversation (your parent) gave you the task of the first message, with a fresh context: you do not see its conversation. Work on your own with your tools.
- Report progress with agent_note at milestones (a short title, what you did or found); it does not stop you.
- When you are blocked or a choice is not yours, ask your parent with agent_ask: you then wait for its answer. Do not ask the user directly.
- When the task is done (or cannot be done), end with agent_report: a summary of the result, the files you changed, and done or blocked. Your parent reads only this report, so make it complete.`

// ParentText tells a conversation how to delegate.
const ParentText = `Sub-agents: delegate a well-defined, self-contained task (an exploration, a change in a few files, a review) with spawn_agent: a child conversation with a fresh context and your rights runs it in the background while you go on. Give it a precise task and the files to read. Its questions and its report come back to you as messages; answer a question with agent_reply, or ask the user first with ask_user when the answer is theirs. agent_message writes to a running child, agent_stop stops it, agent_status lists them. Relay the reports to the user.`

func agentDefs() (parent, child []Def) {
	parent = []Def{
		fn("spawn_agent", "Delegates a task to a sub-agent: a child conversation with a fresh context and your rights (same mode, same ticket), running in the background. Returns its id at once; its questions and its report come back as messages. At most 5 running at once.",
			obj{
				"title": str("Short title of the task, e.g. \"Find the login code\""),
				"task":  str("The task, complete and precise: the child does not see this conversation"),
				"files": strList("Files the child should read first (paths relative to the root)"),
				"mode":  enum("Mode of the child (default: yours); build changes files, plan only reads", Build, Plan, Briefing),
				"server": str("Server of the child, among the servers for sub-agents listed in your instructions (default: the default one, else yours)"),
				"model":  str("Model of the child on that server (required with server, unless it is the default server)"),
			}, "title", "task"),
		fn("agent_reply", "Answers the open question of a sub-agent; it goes on with the answer.",
			obj{"child": str("Id of the sub-agent"), "answer": str("The answer")}, "child", "answer"),
		fn("agent_message", "Writes to a sub-agent that runs or waits (more information, a change of direction). A sub-agent that ended starts again with it.",
			obj{"child": str("Id of the sub-agent"), "text": str("The message")}, "child", "text"),
		fn("agent_stop", "Stops a sub-agent.", obj{"child": str("Id of the sub-agent")}, "child"),
		fn("agent_status", "Lists your sub-agents: status, latest note, open question, report.", obj{}),
	}
	child = []Def{
		fn("agent_note", "Tells your parent where you are (a milestone, a finding). Returns at once.",
			obj{"title": str("Short title"), "text": str("What you did or found")}, "title", "text"),
		fn("agent_ask", "Asks your parent a question when you are blocked or a choice is not yours; your turn stops until it answers (the answer is the result of this call).",
			obj{"question": str("The question, with what the parent needs to answer it")}, "question"),
		fn("agent_report", "Ends your task with your report to the parent: the result, the files changed, and done or blocked. Nothing runs after it.",
			obj{
				"summary":       str("The result, complete: the parent reads only this"),
				"files_changed": strList("Files you changed"),
				"status":        enum("done, or blocked when the task could not be done", "done", "blocked"),
			}, "summary"),
	}
	return
}

var parentAgentDefs, childAgentDefs = agentDefs()

// ParentTools and ChildTools: the tools of each side, handled by the server.
var (
	ParentTools = names(parentAgentDefs)
	ChildTools  = names(childAgentDefs)
)

// argStrings reads a list of strings of the arguments of a call.
func ArgStrings(raw json.RawMessage) []string {
	var out []string
	_ = json.Unmarshal(raw, &out)
	return out
}
