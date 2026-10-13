package acp

import (
	"encoding/json"
	"time"
)

// Entry is an item of the thread of a session.
type Entry struct {
	// Kind: user, agent, thought, tool, plan, permission, notice.
	Kind string `json:"kind"`
	Text string `json:"text,omitempty"`
	Time int64  `json:"time"`

	// A tool call.
	ToolID    string        `json:"toolId,omitempty"`
	Title     string        `json:"title,omitempty"`
	ToolKind  string        `json:"toolKind,omitempty"`
	Status    string        `json:"status,omitempty"`
	Content   []ToolContent `json:"content,omitempty"`
	Locations []string      `json:"locations,omitempty"`

	// The plan.
	Plan []PlanItem `json:"plan,omitempty"`

	// A permission asked: the JSON-RPC id of the request, its options, the option chosen
	// ("cancelled" when the turn stopped).
	Request json.RawMessage `json:"request,omitempty"`
	Options []Option        `json:"options,omitempty"`
	Outcome string          `json:"outcome,omitempty"`
}

type ToolContent struct {
	// Type: content (text), diff, terminal.
	Type    string  `json:"type"`
	Text    string  `json:"text,omitempty"`
	Path    string  `json:"path,omitempty"`
	OldText *string `json:"oldText,omitempty"`
	NewText string  `json:"newText,omitempty"`
}

type PlanItem struct {
	Content  string `json:"content"`
	Priority string `json:"priority,omitempty"`
	Status   string `json:"status,omitempty"`
}

type Option struct {
	ID   string `json:"optionId"`
	Name string `json:"name"`
	Kind string `json:"kind"`
}

// ---------- the wire ----------

type contentBlock struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	Resource *struct {
		URI  string `json:"uri"`
		Text string `json:"text"`
	} `json:"resource,omitempty"`
}

func (b contentBlock) text() string {
	switch {
	case b.Type == "text":
		return b.Text
	case b.Resource != nil:
		return b.Resource.Text
	}
	return ""
}

type wireToolContent struct {
	Type    string        `json:"type"`
	Content *contentBlock `json:"content,omitempty"`
	Path    string        `json:"path,omitempty"`
	OldText *string       `json:"oldText,omitempty"`
	NewText string        `json:"newText,omitempty"`
	// TerminalID of a terminal content: shown as its id (the IDE offers no terminal yet).
	TerminalID string `json:"terminalId,omitempty"`
}

func toolContent(in []wireToolContent) []ToolContent {
	out := make([]ToolContent, 0, len(in))
	for _, c := range in {
		switch c.Type {
		case "diff":
			out = append(out, ToolContent{Type: "diff", Path: c.Path, OldText: c.OldText, NewText: c.NewText})
		case "terminal":
			out = append(out, ToolContent{Type: "terminal", Text: c.TerminalID})
		default:
			if c.Content != nil {
				out = append(out, ToolContent{Type: "content", Text: c.Content.text()})
			}
		}
	}
	return out
}

type wireToolCall struct {
	ToolCallID string             `json:"toolCallId"`
	Title      *string            `json:"title"`
	Kind       *string            `json:"kind"`
	Status     *string            `json:"status"`
	Content    *[]wireToolContent `json:"content"`
	Locations  *[]struct {
		Path string `json:"path"`
	} `json:"locations"`
}

// merge sets the fields the update carries.
func (w wireToolCall) merge(e *Entry) {
	if w.Title != nil && *w.Title != "" {
		e.Title = *w.Title
	}
	if w.Kind != nil {
		e.ToolKind = *w.Kind
	}
	if w.Status != nil {
		e.Status = *w.Status
	}
	if w.Content != nil {
		e.Content = toolContent(*w.Content)
	}
	if w.Locations != nil {
		e.Locations = e.Locations[:0]
		for _, l := range *w.Locations {
			e.Locations = append(e.Locations, l.Path)
		}
	}
}

type update struct {
	SessionUpdate string `json:"sessionUpdate"`
	// chunks
	Content json.RawMessage `json:"content"`
	// tool calls (decoded apart: their title and content are other fields)
	ToolCallID string `json:"toolCallId"`
	// plan
	Entries []PlanItem `json:"entries"`
	// commands
	AvailableCommands json.RawMessage `json:"availableCommands"`
	// mode, config options
	CurrentModeID string          `json:"currentModeId"`
	ConfigOptions json.RawMessage `json:"configOptions"`
	// usage
	Used *int64 `json:"used"`
	Size *int64 `json:"size"`
	// info
	Title *string `json:"title"`
}

// thread is the transcript of a session; change reports each entry changed (its index) or
// appended to (with the text added).
type thread struct {
	Entries []Entry `json:"entries"`
	// tools maps a tool call id to its entry.
	tools map[string]int
	// plan is the entry of the plan of the turn (-1: none yet).
	plan int
}

func (t *thread) index() {
	t.tools, t.plan = map[string]int{}, -1
	for i, e := range t.Entries {
		switch e.Kind {
		case "tool":
			t.tools[e.ToolID] = i
		case "plan":
			t.plan = i
		case "user":
			t.plan = -1
		}
	}
}

func (t *thread) add(e Entry) int {
	if e.Time == 0 {
		e.Time = time.Now().UnixMilli()
	}
	t.Entries = append(t.Entries, e)
	i := len(t.Entries) - 1
	switch e.Kind {
	case "tool":
		t.tools[e.ToolID] = i
	case "user":
		t.plan = -1
	}
	return i
}

// chunk appends text to the last entry when it is of this kind, else starts one. It returns
// the index and whether the entry is new.
func (t *thread) chunk(kind, text string) (int, bool) {
	if n := len(t.Entries); n > 0 && t.Entries[n-1].Kind == kind {
		t.Entries[n-1].Text += text
		return n - 1, false
	}
	return t.add(Entry{Kind: kind, Text: text}), true
}

// applyTool records a tool call or its update; the index of its entry.
func (t *thread) applyTool(w wireToolCall) int {
	i, ok := t.tools[w.ToolCallID]
	if !ok {
		i = t.add(Entry{Kind: "tool", ToolID: w.ToolCallID, Status: "pending"})
	}
	w.merge(&t.Entries[i])
	return i
}

func (t *thread) setPlan(items []PlanItem) int {
	if t.plan < 0 {
		t.plan = t.add(Entry{Kind: "plan"})
	}
	t.Entries[t.plan].Plan = items
	return t.plan
}
