package server

import (
	"testing"

	"github.com/DrSmithFr/web-ide/pod/internal/agent"
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
