package agent

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestPages(t *testing.T) {
	msgs := []*Message{
		{Role: "user", Attachments: json.RawMessage(`[{"name":"a.png","kind":"image"},{"name":"Doodle 1","kind":"doodle","doodle":{"v":1},"png":"data:x"}]`)},
		{Role: "assistant"},
		{Role: "tool", Page: &Page{Name: "Flow", Doc: json.RawMessage(`{"v":1}`)}},
		{Role: "user", Attachments: json.RawMessage(`[{"name":"Doodle 1","kind":"doodle","doodle":{"v":1}},{"name":"Doodle 2","kind":"doodle","doodle":{"v":1}}]`)},
	}
	msgs = append(msgs, &Message{Role: "user",
		Content:     json.RawMessage(`[{"type":"image_url","image_url":{"url":"data:d"}},{"type":"image_url","image_url":{"url":"data:shot"}}]`),
		Attachments: json.RawMessage(`[{"name":"Doodle 3","kind":"doodle","doodle":{"v":1},"png":"data:d"},{"name":"shot.png","kind":"image","w":4,"h":3},{"name":"old.png","kind":"image"}]`)})
	got := Pages(msgs)
	if len(got) != 6 || got[5].Kind != "image" || got[5].Name != "shot.png" || !strings.Contains(string(got[5].Doc), `"src":"data:shot"`) || !strings.Contains(string(got[5].Doc), `"w":4`) {
		t.Fatalf("image page: %+v", got)
	}
	if got[0].Name != "Doodle 1" || got[0].PNG != "data:x" || got[1].Kind != "model" || got[1].Number != 2 || got[3].Name != "Doodle 2" || got[3].Number != 4 {
		t.Fatalf("%+v", got)
	}
}
