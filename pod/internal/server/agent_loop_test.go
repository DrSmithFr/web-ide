package server

import (
	"testing"

	"github.com/DrSmithFr/web-ide/pod/internal/agent"
	"github.com/DrSmithFr/web-ide/pod/internal/llm"
)

func TestReasoningOfTheTurn(t *testing.T) {
	msgs := []*agent.Message{
		{Role: "user", Content: agent.String("a")},
		{Role: "assistant", Content: agent.String("A"), Reasoning: "old"},
		{Role: "user", Content: agent.String("b")},
		{Role: "assistant", Reasoning: "think", ToolCalls: []agent.ToolCall{{ID: "1"}}},
		{Role: "tool", ToolCallID: "1", Content: agent.String("ok")},
		{Role: "assistant", Content: agent.String(""), Reasoning: "stuck", Error: "Answer ended in its reasoning, without text or action."},
	}
	out := apiMessages("sys", msgs, false)
	if len(out) != 6 || out[2].Reasoning != "" || out[4].Reasoning != "think" {
		t.Fatalf("%+v", out)
	}
}

func TestEmptyAnswer(t *testing.T) {
	for m, want := range map[*agent.Message]bool{
		{Content: agent.String(" \n")}:           true,
		{Reasoning: "x"}:                         true,
		{Content: agent.String("ok")}:            false,
		{ToolCalls: []agent.ToolCall{{ID: "1"}}}: false,
	} {
		if emptyAnswer(m) != want {
			t.Errorf("%+v: %v", m, !want)
		}
	}
}

func TestCompactCut(t *testing.T) {
	step := func() []*agent.Message {
		return []*agent.Message{
			{Role: "assistant", Reasoning: "think", ToolCalls: []agent.ToolCall{{ID: "1"}}},
			{Role: "tool", ToolCallID: "1", Content: agent.String("ok")},
		}
	}
	// A summary, one turn of 10 steps, then an exchange: the last 10 steps stay.
	msgs := []*agent.Message{{Role: "user", Kind: "summary"}, {Role: "user", Content: agent.String("go")}}
	for range 10 {
		msgs = append(msgs, step()...)
	}
	msgs = append(msgs, &agent.Message{Role: "user", Content: agent.String("next")}, &agent.Message{Role: "assistant", Content: agent.String("ok")})
	if cut := compactCut(msgs, 0); cut != 4 || msgs[cut].Role != "assistant" {
		t.Fatalf("last steps kept: cut at %d of %d", cut, len(msgs))
	}
	if cut := compactCut(msgs[:8], 0); cut != 0 {
		t.Fatalf("3 steps: nothing to compact, cut at %d", cut)
	}
}

func TestResumeKeepsTheFailedAnswer(t *testing.T) {
	failed := &agent.Message{Role: "assistant", Content: agent.String("half"), Reasoning: "long", Error: "cut", Usage: []byte(`{"prompt":100,"completion":90000}`)}
	chat := &agent.Chat{Messages: []*agent.Message{
		{Role: "user", Content: agent.String("go")},
		{Role: "assistant", Content: agent.String("ok"), Usage: []byte(`{"prompt":50,"completion":10}`)},
		failed,
	}}
	if from := resumeFrom(chat); from != 2 || len(chat.Messages) != 3 || !failed.Resumed {
		t.Fatalf("kept and set aside: from %d, %d messages, resumed %v", from, len(chat.Messages), failed.Resumed)
	}
	if out := apiMessages("sys", chat.Messages, false); len(out) != 3 {
		t.Fatalf("not sent back: %+v", out)
	}
	if used := contextUsed(chat); used != 60 {
		t.Fatalf("its usage is not the context: %d", used)
	}
}

func TestReasoningLoop(t *testing.T) {
	stops := 0
	a := &answer{onLoop: func() { stops++ }}
	long := "Let me look at the log of pod B once more, more carefully this time.\n"
	for range 7 {
		a.add(llm.Delta{Reasoning: long + "const tools = r.messages.filter((m) => m.role === 'tool').length\n"})
	}
	if a.looped {
		t.Fatal("7 times: a sound reasoning")
	}
	for range 3 {
		a.add(llm.Delta{Reasoning: "short line\n"})
	}
	// A line cut between two pieces counts once whole.
	a.add(llm.Delta{Reasoning: long[:20]})
	a.add(llm.Delta{Reasoning: long[20:]})
	for range 5 {
		a.add(llm.Delta{Reasoning: long})
	}
	if !a.looped || stops != 1 {
		t.Fatalf("the 10th time: a loop, stopped once (%v, %d)", a.looped, stops)
	}
}

func TestStuckNudge(t *testing.T) {
	msgs := []*agent.Message{
		{Role: "user", Content: agent.String("go")},
		{Role: "assistant", Reasoning: "round", Error: "loop", Stuck: true, Resumed: true},
	}
	out := apiMessages("sys", msgs, false)
	if len(out) != 3 || out[2].Role != "user" || agent.ContentText(out[2].Content) != stuckNudge {
		t.Fatalf("asked again with the nudge: %+v", out)
	}
	msgs = append(msgs, &agent.Message{Role: "assistant", Content: agent.String("done")})
	if out := apiMessages("sys", msgs, false); len(out) != 3 || out[2].Role != "assistant" {
		t.Fatalf("the nudge only while it is the last: %+v", out)
	}
}
