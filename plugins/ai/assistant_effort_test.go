package ai

import (
	"testing"

	"antennae.space/ide/plugins/ai/engine"
	"antennae.space/ide/pod/assistant"
)

func TestEffortFor(t *testing.T) {
	user := &engine.Message{Role: "user"}
	answer := &engine.Message{Role: "assistant"}
	tool := func(name, status string) *engine.Message {
		return &engine.Message{Role: "tool", Name: name, Status: status}
	}
	set := func(level string) *engine.Message {
		return &engine.Message{Role: "tool", Name: "set_effort", Status: "ok", Effort: level}
	}
	dyn, withTool := assistant.Options{}, assistant.Options{Effort: "auto", EffortTool: true}
	cases := []struct {
		name string
		o    assistant.Options
		msgs []*engine.Message
		want string
	}{
		{"a message of the user", dyn, []*engine.Message{user}, "xhigh"},
		{"tool results", dyn, []*engine.Message{user, answer, tool("read_file", "ok"), tool("bash", "ok")}, "medium"},
		{"a failed call", dyn, []*engine.Message{user, answer, tool("read_file", "ok"), tool("bash", "error")}, "xhigh"},
		{"a refused change", dyn, []*engine.Message{user, answer, tool("edit_file", "denied")}, "xhigh"},
		{"mechanical calls", dyn, []*engine.Message{user, answer, tool("edit_file", "ok"), tool("write_file", "ok")}, "low"},
		{"an edit and a command", dyn, []*engine.Message{user, answer, tool("edit_file", "ok"), tool("bash", "ok")}, "medium"},
		{"only the last step counts", dyn, []*engine.Message{user, answer, tool("bash", "error"), answer, tool("read_file", "ok")}, "medium"},
		{"a summary is no message of the user", dyn, []*engine.Message{user, answer, {Role: "user", Kind: "summary"}, answer, tool("edit_file", "ok")}, "low"},
		{"fixed by the user", assistant.Options{Effort: "low"}, []*engine.Message{user}, "low"},
		{"chosen by the model", withTool, []*engine.Message{user, answer, set("low"), answer, tool("bash", "error")}, "low"},
		{"the model's choice ends with the user", withTool, []*engine.Message{set("low"), user}, "xhigh"},
		{"set_effort ignored when turned off", dyn, []*engine.Message{user, answer, set("low"), tool("read_file", "ok")}, "medium"},
	}
	for _, c := range cases {
		if got := effortFor(&engine.Chat{Messages: c.msgs}, c.o); got != c.want {
			t.Errorf("%s: %s, want %s", c.name, got, c.want)
		}
	}
	// A session of a ticket keeps the effort of its complexity, the dynamic one included.
	low := &engine.Chat{Messages: []*engine.Message{user}, Ticket: &engine.TicketLink{ID: 1, Role: "dev", Effort: "low"}}
	if got := effortFor(low, dyn); got != "low" {
		t.Fatalf("effort of a low ticket: %s", got)
	}
	auto := &engine.Chat{Messages: []*engine.Message{user, answer, tool("read_file", "ok")}, Ticket: &engine.TicketLink{ID: 1, Role: "dev", Effort: "auto"}}
	if got := effortFor(auto, assistant.Options{Effort: "low"}); got != "medium" {
		t.Fatalf("dynamic effort of a medium ticket: %s", got)
	}
}
