package agent

import (
	"encoding/json"
	"testing"
)

func TestPages(t *testing.T) {
	msgs := []*Message{
		{Role: "user", Attachments: json.RawMessage(`[{"name":"a.png","kind":"image"},{"name":"Doodle 1","kind":"doodle","doodle":{"v":1},"png":"data:x"}]`)},
		{Role: "assistant"},
		{Role: "tool", Page: &Page{Name: "Flow", Doc: json.RawMessage(`{"v":1}`)}},
		{Role: "user", Attachments: json.RawMessage(`[{"name":"Doodle 1","kind":"doodle","doodle":{"v":1}},{"name":"Doodle 2","kind":"doodle","doodle":{"v":1}}]`)},
	}
	got := Pages(msgs)
	if len(got) != 4 || got[0].Name != "Doodle 1" || got[0].PNG != "data:x" || got[1].From != "model" || got[1].Number != 2 || got[3].Name != "Doodle 2" || got[3].Number != 4 {
		t.Fatalf("%+v", got)
	}
}
