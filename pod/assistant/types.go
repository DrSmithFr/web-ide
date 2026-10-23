// Package assistant is the tool kit of the assistant (the AI plugin) and of the MCP endpoint
// (the server): the definition of a tool, its arguments and result, the texts translated by the
// page, the console and command tools, the rules of the Plan mode for commands, the diff of a
// file change and the questions of ask_user. The conversation, the prompts and the tools of the
// assistant are the engine of the AI plugin (plugins/ai/engine).
package assistant

import "encoding/json"

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

// Options are the preferences of the page that change how the assistant runs.
type Options struct {
	AutoApply bool  `json:"autoApply,omitempty"`
	Think     *bool `json:"think,omitempty"`
	// Effort: the reasoning effort, xhigh, medium or low; "" or "auto" lets the assistant choose
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
	// DockerProfiles: the Compose profiles active in the Docker tool of the window, as pages
	// sent them before the Docker plugin; read as the plugin's options when Plugins has none.
	DockerProfiles []string `json:"dockerProfiles,omitempty"`
	// Plugins: the options of each plugin for the conversation, given by its page module.
	Plugins map[string]json.RawMessage `json:"plugins,omitempty"`
}

// Preview is an app the user can start and open from the conversation (see the preview
// package); Project is the runtime of the run (the worktree of a development).
type Preview struct {
	Project string `json:"project"`
	Title   string `json:"title"`
	Command string `json:"command"`
	Cwd     string `json:"cwd,omitempty"`
	Port    int    `json:"port"`
	// CommandID: the saved command of the project it starts (Console ▶ menu).
	CommandID string `json:"commandId,omitempty"`
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

// AuthorClaude marks the messages and answers written by Claude Code (through the MCP).
const AuthorClaude = "claude"
