package plugins

import (
	"encoding/json"

	"antennae.space/ide/pod/assistant"
)

// Question is a question waiting for the user, as the Questions tool shows it.
type Question struct {
	// ID: <source prefix>:… (acp:<project>:<session>:<index>, chat:<chat>:<index>) or mcp:<n>.
	ID     string `json:"id"`
	Source string `json:"source"` // acp | assistant | mcp
	// Project the question lives in (a worktree has its own), Session the ACP session or the
	// conversation, Index its entry or message.
	Project string `json:"project"`
	Session string `json:"session,omitempty"`
	Index   int    `json:"index"`
	// Name of the asker (an agent, the assistant, an MCP client) and Title of its session.
	Name  string `json:"name"`
	Title string `json:"title"`
	// Kind: permission (Options), form (Text and Schema), ask (Questions of ask_user).
	Kind      string               `json:"kind"`
	Text      string               `json:"text,omitempty"`
	Options   []Option             `json:"options,omitempty"`
	Schema    json.RawMessage      `json:"schema,omitempty"`
	Questions []assistant.Question `json:"questions,omitempty"`
	// Last is what the asker said before the question (its context).
	Last string `json:"last,omitempty"`
	Time int64  `json:"time"`
	// AutoOpen: an override of the setting for this session (nil: the setting).
	AutoOpen *bool `json:"autoOpen,omitempty"`
}

// Option is a choice of a permission question.
type Option struct {
	ID   string `json:"optionId"`
	Name string `json:"name"`
	Kind string `json:"kind"`
}

// Answer is the answer of the user to a question of a source: the option of a permission, or
// the action of a form (accept with Content, decline, cancel).
type Answer struct {
	Option  string
	Action  string
	Content json.RawMessage
}

// Questions is a source of the Questions tool (Host.Questions): List gives the questions
// waiting in the projects of root (itself and its worktrees), Answer answers one of its ids,
// those starting with Prefix and a colon.
type Questions struct {
	Prefix string
	List   func(root string) []Question
	Answer func(root, id string, a Answer) error
}
