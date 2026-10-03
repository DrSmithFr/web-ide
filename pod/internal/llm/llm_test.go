package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"webide/pod/internal/store"
)

func newManager(t *testing.T, url, kind string) (*Manager, string) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	m := New(st)
	if err := m.SaveServer(Server{ID: "s1", Kind: kind, URL: url, APIKey: "k", Context: 8192}, false); err != nil {
		t.Fatal(err)
	}
	return m, "s1"
}

func TestNormalizeURL(t *testing.T) {
	for in, want := range map[string]string{
		"127.0.0.1:8080": "http://127.0.0.1:8080",
		"http://h:1/v1/": "http://h:1",
		" https://h/ ":   "https://h",
		"":               "",
	} {
		if got := NormalizeURL(in); got != want {
			t.Errorf("%q: %q, want %q", in, got, want)
		}
	}
}

func TestViewHidesKey(t *testing.T) {
	m, _ := newManager(t, "h:1", "llamacpp")
	data, _ := json.Marshal(m.View())
	if strings.Contains(string(data), `"k"`) || !strings.Contains(string(data), `"hasKey":true`) {
		t.Fatalf("view: %s", data)
	}
	// Saving without key keeps the stored one.
	if err := m.SaveServer(Server{ID: "s1", URL: "h:2"}, true); err != nil {
		t.Fatal(err)
	}
	if s, _ := m.server("s1"); s.APIKey != "k" || s.URL != "http://h:2" {
		t.Fatalf("server: %+v", s)
	}
}

func sse(w http.ResponseWriter, chunks ...string) {
	w.Header().Set("Content-Type", "text/event-stream")
	for _, c := range chunks {
		fmt.Fprintf(w, "data: %s\n\n", c)
		w.(http.Flusher).Flush()
	}
	fmt.Fprint(w, "data: [DONE]\n\n")
}

func TestOpenAIChatStream(t *testing.T) {
	var got map[string]any
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/version":
			http.NotFound(w, r)
		case "/v1/chat/completions":
			if r.Header.Get("Authorization") != "Bearer k" {
				t.Errorf("auth header: %q", r.Header.Get("Authorization"))
			}
			_ = json.NewDecoder(r.Body).Decode(&got)
			sse(w,
				`{"choices":[{"delta":{"reasoning_content":"Je "}}]}`,
				`{"choices":[{"delta":{"reasoning_content":"réfléchis"}}]}`,
				`{"choices":[{"delta":{"content":"Voici"}}]}`,
				`{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"a","type":"function","function":{"name":"read_file","arguments":"{\"pa"}}]}}]}`,
				`{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"th\":\"x\"}"}}]}}]}`,
				`{"choices":[{"delta":{"tool_calls":[{"index":1,"id":"b","type":"function","function":{"name":"list_dir","arguments":""}}]}}]}`,
				`{"choices":[{"finish_reason":"tool_calls","delta":{}}]}`,
				`{"choices":[],"usage":{"prompt_tokens":10,"completion_tokens":5,"prompt_tokens_details":{"cached_tokens":3}},"timings":{"predicted_ms":100,"prompt_ms":50,"predicted_per_second":50}}`)
		}
	}))
	defer ts.Close()
	m, id := newManager(t, ts.URL, "auto")
	var deltas []Delta
	think := false
	res, err := m.Chat(context.Background(), ChatRequest{Server: id, Model: "m", Think: &think,
		Messages: []Message{{Role: "user", Content: json.RawMessage(`"salut"`)}}}, func(d Delta) { deltas = append(deltas, d) })
	if err != nil {
		t.Fatal(err)
	}
	if got["stream"] != true || got["model"] != "m" || got["chat_template_kwargs"] == nil {
		t.Fatalf("request: %v", got)
	}
	msg := res.Message
	if string(msg.Content) != `"Voici"` || msg.Reasoning != "Je réfléchis" || res.Finish != "tool_calls" {
		t.Fatalf("message: %+v", res)
	}
	if len(msg.ToolCalls) != 2 || msg.ToolCalls[0].Function.Arguments != `{"path":"x"}` || msg.ToolCalls[1].Function.Arguments != "{}" || msg.ToolCalls[1].ID != "b" {
		t.Fatalf("tool calls: %+v", msg.ToolCalls)
	}
	if res.Usage == nil || res.Usage.Prompt != 10 || res.Usage.Cached != 3 || res.Usage.PerSecond != 50 {
		t.Fatalf("usage: %+v", res.Usage)
	}
	var all Delta
	for _, d := range deltas {
		all.Content += d.Content
		all.Reasoning += d.Reasoning
	}
	if all.Content != "Voici" || all.Reasoning != "Je réfléchis" {
		t.Fatalf("deltas: %+v", deltas)
	}
}

func TestOpenAIChatError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(400)
		io.WriteString(w, `{"error":{"message":"contexte dépassé"}}`)
	}))
	defer ts.Close()
	m, id := newManager(t, ts.URL, "llamacpp")
	_, err := m.Chat(context.Background(), ChatRequest{Server: id, Model: "m"}, nil)
	if err == nil || !strings.Contains(err.Error(), "contexte dépassé") {
		t.Fatalf("err: %v", err)
	}
}

func TestOllamaChat(t *testing.T) {
	var got struct {
		Messages []ollamaMessage `json:"messages"`
		Options  map[string]any  `json:"options"`
		Think    *bool           `json:"think"`
	}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/version":
			io.WriteString(w, `{"version":"0.35.0"}`)
		case "/api/chat":
			_ = json.NewDecoder(r.Body).Decode(&got)
			for _, l := range []string{
				`{"message":{"role":"assistant","content":"","thinking":"hmm"},"done":false}`,
				`{"message":{"role":"assistant","content":"Bon"},"done":false}`,
				`{"message":{"role":"assistant","content":"","tool_calls":[{"function":{"name":"list_dir","arguments":{"path":"."}}}]},"done":false}`,
				`{"message":{"role":"assistant","content":""},"done":true,"done_reason":"stop","prompt_eval_count":7,"eval_count":4,"eval_duration":2000000000}`,
			} {
				io.WriteString(w, l+"\n")
			}
		}
	}))
	defer ts.Close()
	m, id := newManager(t, ts.URL, "auto")
	think := true
	res, err := m.Chat(context.Background(), ChatRequest{Server: id, Model: "q", Think: &think, Messages: []Message{
		{Role: "user", Content: json.RawMessage(`[{"type":"text","text":"vois"},{"type":"image_url","image_url":{"url":"data:image/png;base64,QUJD"}}]`)},
		{Role: "assistant", ToolCalls: []ToolCall{{ID: "t1", Function: struct {
			Name      string `json:"name"`
			Arguments string `json:"arguments"`
		}{"read_file", `{"path":"a"}`}}}},
		{Role: "tool", ToolCallID: "t1", Content: json.RawMessage(`"contenu"`)},
	}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got.Options["num_ctx"] != float64(8192) || got.Think == nil || !*got.Think {
		t.Fatalf("request: %+v", got)
	}
	if got.Messages[0].Content != "vois" || len(got.Messages[0].Images) != 1 || got.Messages[0].Images[0] != "QUJD" {
		t.Fatalf("user message: %+v", got.Messages[0])
	}
	if string(got.Messages[1].ToolCalls[0].Function.Arguments) != `{"path":"a"}` || got.Messages[2].ToolName != "read_file" {
		t.Fatalf("tool messages: %+v", got.Messages[1:])
	}
	if string(res.Message.Content) != `"Bon"` || res.Message.Reasoning != "hmm" || res.Finish != "tool_calls" ||
		len(res.Message.ToolCalls) != 1 || res.Message.ToolCalls[0].Function.Arguments != `{"path":"."}` || res.Message.ToolCalls[0].ID == "" {
		t.Fatalf("result: %+v", res)
	}
	if res.Usage.PerSecond != 2 || res.Usage.Prompt != 7 {
		t.Fatalf("usage: %+v", res.Usage)
	}
}

func TestModels(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path + "?" + r.URL.RawQuery {
		case "/api/version?":
			http.NotFound(w, r)
		case "/v1/models?":
			io.WriteString(w, `{"data":[{"id":"b","status":{"value":"unloaded","args":["--mmproj","x"]}},{"id":"a","status":{"value":"loaded"}}]}`)
		case "/props?":
			io.WriteString(w, `{"role":"router"}`)
		case "/props?model=a":
			io.WriteString(w, `{"modalities":{"vision":true,"video":true},"chat_template_caps":{"supports_tools":false},"default_generation_settings":{"n_ctx":4096}}`)
		default:
			t.Errorf("unexpected %s", r.URL)
		}
	}))
	defer ts.Close()
	m, id := newManager(t, ts.URL, "auto")
	list, err := m.Models(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	a, b := list.Models[0], list.Models[1]
	if list.Kind != "llamacpp" || a.ID != "a" || !a.Caps.Video || a.Caps.Tools || !a.Caps.Known || a.Context != 4096 {
		t.Fatalf("a: %+v", a)
	}
	if b.State != "unloaded" || !b.Caps.Vision || b.Caps.Known {
		t.Fatalf("b: %+v", b)
	}
}

func TestChats(t *testing.T) {
	m, _ := newManager(t, "h:1", "llamacpp")
	if err := m.SaveChat("p1", json.RawMessage(`{"id":"c1","title":"Un","updated":1,"messages":[]}`)); err != nil {
		t.Fatal(err)
	}
	_ = m.SaveChat("p1", json.RawMessage(`{"id":"c2","title":"Deux","updated":2}`))
	if err := m.SaveChat("p1", json.RawMessage(`{"id":"../x"}`)); err == nil {
		t.Fatal("bad id accepted")
	}
	list, _ := m.ListChats("p1")
	if len(list) != 2 || list[0].ID != "c2" {
		t.Fatalf("list: %+v", list)
	}
	data, err := m.GetChat("p1", "c1")
	if err != nil || !strings.Contains(string(data), `"messages"`) {
		t.Fatalf("get: %s %v", data, err)
	}
	_ = m.DeleteChat("p1", "c1")
	if list, _ := m.ListChats("p1"); len(list) != 1 {
		t.Fatalf("after delete: %+v", list)
	}
	if list, err := m.ListChats("p2"); err != nil || len(list) != 0 {
		t.Fatalf("empty project: %v %v", list, err)
	}
}
