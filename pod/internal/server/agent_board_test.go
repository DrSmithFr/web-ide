package server

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/DrSmithFr/web-ide/pod/internal/agent"
)

func TestBoardDrawChecks(t *testing.T) {
	s, _ := newServer(t)
	r := &agentRun{id: "c", ctx: context.Background(), chat: &agent.Chat{}}
	args := func(js string) toolArgs {
		var a toolArgs
		if err := json.Unmarshal([]byte(js), &a); err != nil {
			t.Fatal(err)
		}
		return a
	}
	for js, want := range map[string]string{
		`{"title":"x","elements":[],"from":2}`:                 "the board has no page yet",
		`{"title":"x","elements":[],"from":1,"size":"square"}`: "size and from cannot go together",
		`{"title":"x","elements":{}}`:                          "must be a list",
		`{"title":"x"}`:                                        "must be a list",
		`{"title":"x","elements":[{"type":"stroke","points":[` + strings.Repeat("[1,2],", 500) + `[1,2]]}]}`: "element 1: 501 points",
		`{"title":"x","elements":[{"type":"rect","x":1,"y":1,"w":5,"h":5}]}`:                                 "needs an IDE window open",
	} {
		if _, err := s.boardDoodle(r, args(js)); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("doodle %.60s: %v", js, err)
		}
	}
	for js, want := range map[string]string{
		`{"title":"x","image":"a.png"}`:                                      `image is an SVG`,
		`{"title":"x","image":"<svg><script>x</script></svg>"}`:              `may not contain "<script"`,
		`{"title":"x","image":"<svg><image href='https://x/a.png'/></svg>"}`: "external reference",
		`{"title":"x","image":"<svg onload=\"x()\"/>"}`:                      "event handler",
		`{"title":"x","image":"<svg width='10' height='10'/>"}`:              "needs an IDE window open",
	} {
		if _, err := s.boardImage(r, args(js)); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("image %.60s: %v", js, err)
		}
	}
	// A copy of an image whose data is not kept (an old attachment) is refused.
	r.chat.Messages = []*agent.Message{{Role: "user", Attachments: json.RawMessage(`[{"name":"old.png","kind":"image","w":4,"h":3}]`)}}
	if _, err := s.boardDoodle(r, args(`{"title":"x","elements":[],"from":1}`)); err == nil || !strings.Contains(err.Error(), "not kept") {
		t.Errorf("copy of a lost image: %v", err)
	}
}

func TestPageImages(t *testing.T) {
	page := &agent.Page{Name: "Flow", PNG: "data:image/png;base64,AA"}
	msgs := []*agent.Message{
		{Role: "user", Content: agent.String("draw")},
		{Role: "assistant"},
		{Role: "tool", ToolCallID: "a", Content: agent.String("Page 1"), Page: page},
		{Role: "tool", ToolCallID: "b", Content: agent.String("ok")},
		{Role: "assistant", Content: agent.String("done")},
	}
	out := apiMessages("sys", msgs, true)
	if len(out) != 7 || out[5].Role != "user" || !strings.Contains(string(out[5].Content), "data:image/png;base64,AA") || out[6].Role != "assistant" {
		t.Fatalf("%+v", out)
	}
	if len(apiMessages("sys", msgs, false)) != 6 {
		t.Fatal("no image for a model without vision")
	}
}
