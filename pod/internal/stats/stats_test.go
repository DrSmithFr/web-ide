package stats

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/DrSmithFr/web-ide/pod/internal/agent"
)

func answer(at int64, effort string, usage string, thinkMs, elapsedMs int64, reasoning string, calls ...[2]string) *agent.Message {
	m := &agent.Message{Role: "assistant", At: at, Effort: effort, Model: "m", Usage: json.RawMessage(usage), ThinkMs: thinkMs, ElapsedMs: elapsedMs, Reasoning: reasoning}
	for _, c := range calls {
		var tc agent.ToolCall
		tc.Function.Name, tc.Function.Arguments = c[0], c[1]
		m.ToolCalls = append(m.ToolCalls, tc)
	}
	return m
}

func result(name, status, failure string, elapsed, wait int64, content string) *agent.Message {
	return &agent.Message{Role: "tool", Name: name, Status: status, Failure: failure, ElapsedMs: elapsed, WaitMs: wait, Content: agent.String(content)}
}

// A conversation: a message, an answer reading two files at once (one read twice), an edit
// approved after a wait, a command that fails, a compaction, a forgotten parameter, the end.
func fixture() *agent.Chat {
	return &agent.Chat{ID: "c", Messages: []*agent.Message{
		{Role: "user", At: 1000, Content: agent.String(strings.Repeat("u", 70))},
		answer(2000, "xhigh", `{"prompt":1000,"completion":100,"cached":0,"perSecond":50,"promptPerSecond":500}`, 800, 1200, strings.Repeat("r", 30),
			[2]string{"read_file", `{"path":"a"}`}, [2]string{"read_file", `{"path":"b"}`}),
		result("read_file", "ok", "", 10, 0, strings.Repeat("x", 350)),
		result("read_file", "ok", "", 20, 0, strings.Repeat("x", 350)),
		answer(4000, "medium", `{"prompt":1300,"completion":40,"cached":1000,"perSecond":40,"promptPerSecond":300}`, 200, 500, "",
			[2]string{"edit_file", `{"path":"a"}`}),
		result("edit_file", "ok", "", 5000, 4900, ""),
		answer(6000, "low", `{"prompt":1400,"completion":20,"cached":1300,"perSecond":40,"promptPerSecond":100}`, 100, 300, "",
			[2]string{"read_file", `{"path":"a"}`}, [2]string{"bash", `{"command":"make test"}`}),
		result("read_file", "ok", "", 10, 0, ""),
		result("bash", "error", "exit", 3000, 0, "FAIL"),
		{Role: "user", At: 7000, Kind: "summary", Content: agent.String(strings.Repeat("s", 35))},
		answer(9000, "xhigh", `{"prompt":500,"completion":60,"cached":0,"perSecond":60,"promptPerSecond":1000}`, 600, 1000, "",
			[2]string{"write_file", `{}`}),
		result("write_file", "error", "usage", 1, 0, "Error: path is missing"),
		answer(10000, "xhigh", `{"prompt":600,"completion":30,"cached":500,"perSecond":30,"promptPerSecond":1000}`, 300, 1000, strings.Repeat("r", 10)),
	}}
}

func TestCompute(t *testing.T) {
	c := fixture()
	// The messages before the compaction stay for the display, out of the context.
	for _, m := range c.Messages[:9] {
		m.Compacted = true
	}
	a := New(Filter{})
	a.Add(c)
	s := a.Result(c)

	if s.Answers != 5 || s.First != 2000 || s.Last != 10000 || s.Conversations != 1 {
		t.Fatalf("answers: %+v", s)
	}
	// Read: 1000 at 500/s, 300 at 300/s, 100 at 100/s, 500 at 1000/s, 100 at 1000/s = 2000+1000+1000+500+100 ms.
	if s.Read.Tokens != 2000 || s.Read.Ms != 4600 || int(s.Read.PerSecond) != 434 {
		t.Fatalf("read: %+v", s.Read)
	}
	// Write: 100/50 + 40/40 + 20/40 + 60/60 + 30/30 = 2+1+0.5+1+1 s.
	if s.Write.Tokens != 250 || s.Write.Ms != 5500 || int(s.Write.PerSecond) != 45 {
		t.Fatalf("write: %+v", s.Write)
	}
	if s.Prompt != 4800 || s.Cached != 2800 || s.GenerationMs != 4000 || s.ThinkMs != 2000 {
		t.Fatalf("totals: %+v", s)
	}
	// Reasoning: 100 * 30/(30+21+21) = 41 (a call is its name and arguments), then 30 * 10/10 = 30.
	if s.ReasoningTokens != 71 {
		t.Fatalf("reasoning tokens: %d", s.ReasoningTokens)
	}
	wantEfforts := []Effort{{"xhigh", 3, 1700, 600}, {"medium", 1, 200, 200}, {"low", 1, 100, 100}}
	if !reflect.DeepEqual(s.Efforts, wantEfforts) {
		t.Fatalf("efforts: %+v", s.Efforts)
	}
	wantTools := []Tool{
		{Name: "bash", Calls: 1, Ms: 3000, Failures: map[string]int{"exit": 1}},
		{Name: "edit_file", Calls: 1, Ms: 100, WaitMs: 4900},
		{Name: "read_file", Calls: 3, Ms: 40},
		{Name: "write_file", Calls: 1, Ms: 1, Failures: map[string]int{"usage": 1}},
	}
	if !reflect.DeepEqual(s.Tools, wantTools) {
		t.Fatalf("tools: %+v", s.Tools)
	}
	if s.ToolCalls != 6 || s.ToolMs != 3141 || s.WithTools != 4 || s.Multi != 2 || s.Repeats != 1 ||
		!reflect.DeepEqual(s.Failures, map[string]int{"exit": 1, "usage": 1}) {
		t.Fatalf("calls: %+v", s)
	}

	ctx := s.Context
	// The last answer: 600 + 30 generated, all of it reasoning. The messages: summary 35/3.5,
	// result 22/3.5, call 12/3.5; the rest is the system prompt.
	if ctx.Tokens != 600 {
		t.Fatalf("context tokens: %d", ctx.Tokens)
	}
	wantParts := []Part{{"system", "", 581}, {"tool", "write_file", 6}, {"assistant", "", 3}, {"summary", "", 10}}
	sortParts(wantParts)
	if !reflect.DeepEqual(ctx.Parts, wantParts) {
		t.Fatalf("parts: %+v", ctx.Parts)
	}
	wantCurve := []Point{{2000, 1000, false}, {4000, 1300, false}, {6000, 1400, false}, {7000, 0, true}, {9000, 500, false}, {10000, 600, false}}
	if !reflect.DeepEqual(ctx.Curve, wantCurve) {
		t.Fatalf("curve: %+v", ctx.Curve)
	}
	if !reflect.DeepEqual(ctx.Compactions, []Gap{{At: 7000, Ms: 6000, Steps: 3, Peak: 1400, Tokens: 160}}) {
		t.Fatalf("compactions: %+v", ctx.Compactions)
	}
}

func sortParts(p []Part) {
	for i := range p {
		for j := i + 1; j < len(p); j++ {
			if p[j].Tokens > p[i].Tokens {
				p[i], p[j] = p[j], p[i]
			}
		}
	}
}

func TestFilter(t *testing.T) {
	a := New(Filter{Effort: "xhigh", From: 5000})
	a.Add(fixture())
	a.Add(fixture())
	s := a.Result(nil)
	// The two last answers of each conversation, with their tool result.
	if s.Conversations != 2 || s.Answers != 4 || s.ToolCalls != 2 || s.Failures["usage"] != 2 || s.Context != nil {
		t.Fatalf("filtered: %+v", s)
	}
}
