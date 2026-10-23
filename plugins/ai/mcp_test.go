package ai

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestMCPConversation(t *testing.T) {
	raw := json.RawMessage(`{"title":"Export","messages":[
		{"role":"user","content":"I need an export","display":"I need an export"},
		{"role":"assistant","content":"","tool_calls":[{"id":"c1","function":{"name":"ask_user","arguments":"{\"questions\":[{\"question\":\"Format?\"}]}"}}]},
		{"role":"tool","tool_call_id":"c1","content":"Format?: CSV"},
		{"role":"assistant","content":[{"type":"text","text":"Noted: CSV."}]},
		{"role":"assistant","content":"","tool_calls":[{"id":"c2","function":{"name":"read_file","arguments":"{}"}}]},
		{"role":"tool","tool_call_id":"c2","content":"secret file content"}]}`)
	text := conversationText(raw, 0, false)
	for _, want := range []string{"# Conversation: Export", "## User\nI need an export", "Format?", "## Answers of the user\nFormat?: CSV", "Noted: CSV."} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q in:\n%s", want, text)
		}
	}
	if strings.Contains(text, "secret file content") || strings.Contains(text, "call read_file") {
		t.Error("other tool results are left out")
	}
	// For a review: the last messages, with the calls, their errors and a failed answer.
	raw = json.RawMessage(`{"title":"Dev","messages":[
		{"role":"user","content":"Develop"},
		{"role":"assistant","content":"","tool_calls":[{"id":"b1","function":{"name":"bash","arguments":"{\"command\":\"pkill -x web-ide-pod\"}"}}]},
		{"role":"tool","tool_call_id":"b1","name":"bash","status":"ok","content":"Exit code 0"},
		{"role":"assistant","content":"","tool_calls":[{"id":"b2","function":{"name":"bash","arguments":"{\"command\":\"make\"}"}}]},
		{"role":"tool","tool_call_id":"b2","name":"bash","status":"ok","content":"Interrupted: the pod stopped before this tool ended."},
		{"role":"assistant","content":"","error":"Interrupted: the pod stopped."}]}`)
	text = conversationText(raw, 5, true)
	for _, want := range []string{"(1 earlier messages left out)", "- call bash {\"command\":\"pkill -x web-ide-pod\"}", "→ ok: Interrupted: the pod stopped before", "## The answer failed\nInterrupted: the pod stopped."} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q in:\n%s", want, text)
		}
	}
	if strings.Contains(text, "## User\nDevelop") || strings.Contains(text, "Exit code 0") {
		t.Errorf("earlier messages or plain results kept:\n%s", text)
	}
}
