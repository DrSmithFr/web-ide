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
		`{"title":"x","elements":[],"from":2}`: "the board has no page yet",
		`{"title":"x","elements":{}}`:          "must be a list",
		`{"title":"x","elements":[{"type":"stroke","points":[` + strings.Repeat("[1,2],", 500) + `[1,2]]}]}`: "element 1: 501 points",
		`{"title":"x","elements":[{"type":"rect","x":1,"y":1,"w":5,"h":5}]}`:                                 "needs an IDE window open",
	} {
		if _, err := s.boardDraw(r, nil, args(js)); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%.60s: %v", js, err)
		}
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

func TestAttachmentImage(t *testing.T) {
	msgs := []*agent.Message{{
		Role:        "user",
		Content:     json.RawMessage(`[{"type":"image_url","image_url":{"url":"data:doodle"}},{"type":"image_url","image_url":{"url":"data:shot"}},{"type":"text","text":"look"}]`),
		Attachments: json.RawMessage(`[{"name":"Doodle 1","kind":"doodle","png":"data:doodle"},{"name":"shot.png","kind":"image"}]`),
	}}
	if src, _ := attachmentImage(msgs, "shot.png"); src != "data:shot" {
		t.Fatalf("image: %q", src)
	}
	if src, _ := attachmentImage(msgs, "Doodle 1"); src != "data:doodle" {
		t.Fatalf("doodle: %q", src)
	}
	if src, names := attachmentImage(msgs, "nope"); src != "" || strings.Join(names, ",") != "Doodle 1,shot.png" {
		t.Fatalf("unknown: %q %v", src, names)
	}
}
