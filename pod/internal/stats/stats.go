// Package stats measures how the assistant worked in conversations: speeds, time spent
// generating, thinking and in each tool, failures of the tools, the context and its
// compactions. Token counts by part (reasoning, context by tool) are estimates: servers give
// the totals only.
package stats

import (
	"encoding/json"
	"sort"

	"github.com/DrSmithFr/web-ide/pod/internal/agent"
	"github.com/DrSmithFr/web-ide/pod/internal/llm"
)

// CharsPerToken estimates the tokens of a text from its length (code and English).
const CharsPerToken = 3.5

// Filter keeps the answers of a model, of an effort, in a period ("" and 0: all).
type Filter struct {
	Model  string `json:"model,omitempty"`
	Effort string `json:"effort,omitempty"`
	From   int64  `json:"from,omitempty"` // Unix ms
	To     int64  `json:"to,omitempty"`
}

type Speed struct {
	Tokens    int     `json:"tokens"`
	Ms        float64 `json:"ms"`
	PerSecond float64 `json:"perSecond"`
}

type Effort struct {
	Effort   string `json:"effort"` // "" before the effort was recorded
	Answers  int    `json:"answers"`
	ThinkMs  int64  `json:"thinkMs"`
	MedianMs int64  `json:"medianMs"`
}

type Tool struct {
	Name  string `json:"name"`
	Calls int    `json:"calls"`
	// Ms: time of the calls without the approval waits (WaitMs).
	Ms     int64 `json:"ms"`
	WaitMs int64 `json:"waitMs"`
	// Failures by kind: usage, exit, error, denied.
	Failures map[string]int `json:"failures,omitempty"`
}

type Part struct {
	Kind   string `json:"kind"` // system, user, summary, assistant, tool
	Name   string `json:"name,omitempty"`
	Tokens int    `json:"tokens"`
}

// Point of the context curve: the prompt of an answer, or a compaction.
type Point struct {
	At         int64 `json:"at,omitempty"`
	Tokens     int   `json:"tokens"`
	Compaction bool  `json:"compaction,omitempty"`
}

// Gap: the work between a compaction and the one before (or the start).
type Gap struct {
	At     int64 `json:"at,omitempty"`
	Ms     int64 `json:"ms,omitempty"`
	Steps  int   `json:"steps"`
	Peak   int   `json:"peak"` // largest prompt before it
	Tokens int   `json:"tokens"`
}

type Context struct {
	Tokens      int     `json:"tokens"` // last prompt and the answer kept of it
	Parts       []Part  `json:"parts"`
	Curve       []Point `json:"curve"`
	Compactions []Gap   `json:"compactions"`
}

type Stats struct {
	Conversations int   `json:"conversations"`
	Answers       int   `json:"answers"`
	First         int64 `json:"first,omitempty"`
	Last          int64 `json:"last,omitempty"`
	Read          Speed `json:"read"`
	Write         Speed `json:"write"`
	Prompt        int   `json:"prompt"` // tokens of all the prompts, and those read from the cache
	Cached        int   `json:"cached"`
	GenerationMs  int64 `json:"generationMs"`
	ThinkMs       int64 `json:"thinkMs"`
	// ReasoningTokens: estimated share of Write.Tokens spent thinking.
	ReasoningTokens int            `json:"reasoningTokens"`
	Efforts         []Effort       `json:"efforts"`
	Tools           []Tool         `json:"tools"`
	ToolCalls       int            `json:"toolCalls"`
	ToolMs          int64          `json:"toolMs"`
	Failures        map[string]int `json:"failures,omitempty"`
	// WithTools: answers that call tools; Multi: those that call several at once.
	WithTools int `json:"withTools"`
	Multi     int `json:"multi"`
	// Repeats: calls identical (tool and arguments) to one made since the last message of the user.
	Repeats int      `json:"repeats"`
	Context *Context `json:"context,omitempty"`
}

// Acc gathers the statistics of conversations.
type Acc struct {
	f     Filter
	s     Stats
	think map[string][]int64
	tools map[string]*Tool
}

func New(f Filter) *Acc {
	return &Acc{f: f, s: Stats{Failures: map[string]int{}}, think: map[string][]int64{}, tools: map[string]*Tool{}}
}

func (f Filter) keeps(m *agent.Message) bool {
	return (f.Model == "" || m.Model == f.Model) && (f.Effort == "" || m.Effort == f.Effort) &&
		(f.From == 0 || m.At >= f.From) && (f.To == 0 || m.At > 0 && m.At < f.To)
}

func usageOf(m *agent.Message) llm.Usage {
	var u llm.Usage
	_ = json.Unmarshal(m.Usage, &u)
	return u
}

// Add counts the messages of a conversation.
func (a *Acc) Add(c *agent.Chat) {
	a.s.Conversations++
	kept := false
	seen := map[string]bool{}
	for _, m := range c.Messages {
		switch m.Role {
		case "user":
			if m.Kind != "summary" {
				seen = map[string]bool{}
			}
		case "assistant":
			kept = a.f.keeps(m)
			if kept {
				a.answer(m, seen)
			}
		case "tool":
			if kept {
				a.tool(m)
			}
		}
	}
}

func (a *Acc) answer(m *agent.Message, seen map[string]bool) {
	s := &a.s
	s.Answers++
	if m.At > 0 {
		if s.First == 0 || m.At < s.First {
			s.First = m.At
		}
		s.Last = max(s.Last, m.At)
	}
	u := usageOf(m)
	s.Prompt += u.Prompt
	s.Cached += u.Cached
	if read := u.Prompt - u.Cached; read > 0 && u.PromptPerSecond > 0 {
		s.Read.Tokens += read
		s.Read.Ms += float64(read) / u.PromptPerSecond * 1000
	}
	if u.Completion > 0 && u.PerSecond > 0 {
		s.Write.Tokens += u.Completion
		s.Write.Ms += float64(u.Completion) / u.PerSecond * 1000
		s.ReasoningTokens += reasoningTokens(m, u.Completion)
	}
	s.GenerationMs += m.ElapsedMs
	s.ThinkMs += m.ThinkMs
	a.think[m.Effort] = append(a.think[m.Effort], m.ThinkMs)
	if n := len(m.ToolCalls); n > 0 {
		s.WithTools++
		if n > 1 {
			s.Multi++
		}
	}
	for _, c := range m.ToolCalls {
		key := c.Function.Name + "\x00" + c.Function.Arguments
		if seen[key] {
			s.Repeats++
		}
		seen[key] = true
	}
}

// reasoningTokens: the share of the generated tokens spent thinking, by the length of the texts.
func reasoningTokens(m *agent.Message, completion int) int {
	r := len(m.Reasoning)
	rest := len(agent.ContentText(m.Content))
	for _, c := range m.ToolCalls {
		rest += len(c.Function.Name) + len(c.Function.Arguments)
	}
	if r+rest == 0 {
		return 0
	}
	return completion * r / (r + rest)
}

func (a *Acc) tool(m *agent.Message) {
	t := a.tools[m.Name]
	if t == nil {
		t = &Tool{Name: m.Name, Failures: map[string]int{}}
		a.tools[m.Name] = t
	}
	t.Calls++
	t.Ms += max(0, m.ElapsedMs-m.WaitMs)
	t.WaitMs += m.WaitMs
	a.s.ToolCalls++
	a.s.ToolMs += max(0, m.ElapsedMs-m.WaitMs)
	kind := ""
	switch {
	case m.Status == "denied":
		kind = "denied"
	case m.Status == "error":
		kind = m.Failure
		if kind == "" {
			kind = "error"
		}
	}
	if kind != "" {
		t.Failures[kind]++
		a.s.Failures[kind]++
	}
}

// Result: the statistics gathered, with the context of c (nil: none, for several conversations).
func (a *Acc) Result(c *agent.Chat) Stats {
	s := a.s
	s.Read.PerSecond = perSecond(s.Read)
	s.Write.PerSecond = perSecond(s.Write)
	for e, ms := range a.think {
		sort.Slice(ms, func(i, j int) bool { return ms[i] < ms[j] })
		var sum int64
		for _, v := range ms {
			sum += v
		}
		s.Efforts = append(s.Efforts, Effort{Effort: e, Answers: len(ms), ThinkMs: sum, MedianMs: ms[len(ms)/2]})
	}
	sort.Slice(s.Efforts, func(i, j int) bool { return effortRank(s.Efforts[i].Effort) < effortRank(s.Efforts[j].Effort) })
	for _, t := range a.tools {
		if len(t.Failures) == 0 {
			t.Failures = nil
		}
		s.Tools = append(s.Tools, *t)
	}
	sort.Slice(s.Tools, func(i, j int) bool {
		return s.Tools[i].Ms > s.Tools[j].Ms || s.Tools[i].Ms == s.Tools[j].Ms && s.Tools[i].Name < s.Tools[j].Name
	})
	if len(s.Failures) == 0 {
		s.Failures = nil
	}
	if c != nil {
		s.Context = contextOf(c)
	}
	return s
}

func perSecond(sp Speed) float64 {
	if sp.Ms <= 0 {
		return 0
	}
	return float64(sp.Tokens) / sp.Ms * 1000
}

func effortRank(e string) int {
	switch e {
	case "xhigh":
		return 0
	case "medium":
		return 1
	case "low":
		return 2
	}
	return 3
}

// contextOf: the context of the conversation now, estimated by part, and its history.
func contextOf(c *agent.Chat) *Context {
	ctx := &Context{Curve: []Point{}, Compactions: []Gap{}}
	var last *agent.Message
	start := int64(0)
	steps, peak, tokens := 0, 0, 0
	for _, m := range c.Messages {
		if start == 0 && m.At > 0 {
			start = m.At
		}
		switch {
		case m.Role == "assistant" && len(m.Usage) > 0:
			u := usageOf(m)
			if u.Prompt == 0 {
				continue
			}
			last = m
			steps++
			peak = max(peak, u.Prompt)
			tokens += u.Completion
			ctx.Curve = append(ctx.Curve, Point{At: m.At, Tokens: u.Prompt})
		case m.Role == "user" && m.Kind == "summary":
			g := Gap{At: m.At, Steps: steps, Peak: peak, Tokens: tokens}
			if m.At > 0 && start > 0 {
				g.Ms = m.At - start
			}
			ctx.Compactions = append(ctx.Compactions, g)
			ctx.Curve = append(ctx.Curve, Point{At: m.At, Compaction: true})
			start, steps, peak, tokens = m.At, 0, 0, 0
		}
	}
	if last == nil {
		return ctx
	}
	u := usageOf(last)
	// The reasoning is not sent back to the model: the answer keeps its text and its calls.
	ctx.Tokens = u.Prompt + u.Completion - reasoningTokens(last, u.Completion)
	parts := map[[2]string]int{}
	total := 0
	for _, m := range c.Messages {
		if m.Compacted {
			continue
		}
		key, n := [2]string{m.Role, ""}, len(agent.ContentText(m.Content))
		switch m.Role {
		case "user":
			if m.Kind == "summary" {
				key[0] = "summary"
			}
		case "assistant":
			for _, call := range m.ToolCalls {
				n += len(call.Function.Name) + len(call.Function.Arguments)
			}
		case "tool":
			key[1] = m.Name
		}
		t := int(float64(n) / CharsPerToken)
		parts[key] += t
		total += t
	}
	// Scaled to the real total when the estimate goes beyond it; the rest is the system prompt
	// and the definitions of the tools.
	scale := 1.0
	if total > ctx.Tokens && total > 0 {
		scale = float64(ctx.Tokens) / float64(total)
	}
	sum := 0
	for k, t := range parts {
		t = int(float64(t) * scale)
		if t == 0 {
			continue
		}
		ctx.Parts = append(ctx.Parts, Part{Kind: k[0], Name: k[1], Tokens: t})
		sum += t
	}
	if rest := ctx.Tokens - sum; rest > 0 {
		ctx.Parts = append(ctx.Parts, Part{Kind: "system", Tokens: rest})
	}
	sort.Slice(ctx.Parts, func(i, j int) bool {
		return ctx.Parts[i].Tokens > ctx.Parts[j].Tokens || ctx.Parts[i].Tokens == ctx.Parts[j].Tokens && ctx.Parts[i].Name < ctx.Parts[j].Name
	})
	return ctx
}
