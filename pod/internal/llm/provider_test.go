package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/DrSmithFr/web-ide/pod/internal/i18n"
	"github.com/DrSmithFr/web-ide/pod/internal/store"
)

// A cloud provider: the key on every request, no llama.cpp fields, reasoning and cost in
// the stream, typed models merged with /models, errors explained, rate limits retried.
func TestOpenAIProvider(t *testing.T) {
	var limited atomic.Int32
	var body map[string]any
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer sk-test" {
			w.WriteHeader(401)
			fmt.Fprint(w, `{"error":{"message":"Incorrect API key"}}`)
			return
		}
		switch r.URL.Path {
		case "/api/v1/models":
			fmt.Fprint(w, `{"data":[{"id":"big/coder","context_length":200000,"architecture":{"input_modalities":["text","image"]},"supported_parameters":["tools","reasoning"]},{"id":"small"}]}`)
		case "/api/v1/chat/completions":
			_ = json.NewDecoder(r.Body).Decode(&body)
			switch body["model"] {
			case "busy":
				if limited.Add(1) < 3 {
					w.Header().Set("Retry-After", "0")
					w.WriteHeader(429)
					fmt.Fprint(w, `{"error":{"message":"slow down"}}`)
					return
				}
			case "broke":
				w.WriteHeader(402)
				fmt.Fprint(w, `{"error":{"message":"Insufficient credits"}}`)
				return
			}
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"reasoning\":\"hm\"}}]}\n\n")
			fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"Hi\"},\"finish_reason\":\"stop\"}]}\n\n")
			fmt.Fprint(w, "data: {\"choices\":[],\"usage\":{\"prompt_tokens\":12,\"completion_tokens\":3,\"cost\":0.0042,\"prompt_tokens_details\":{\"cached_tokens\":4}}}\n\n")
			fmt.Fprint(w, "data: [DONE]\n\n")
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()
	st, _ := store.Open(t.TempDir())
	m := New(st)
	srv := Server{ID: "or", Name: "OpenRouter", Kind: "openai", URL: ts.URL + "/api/v1", APIKey: "sk-test", Note: "strong, paid", Children: true,
		Models: []ModelConf{{ID: "small", Context: 32000, Tools: true}, {ID: "typed-only", Tools: true}}}
	if err := m.SaveServer(srv, false); err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(m.View())
	if strings.Contains(string(data), "sk-test") || !strings.Contains(string(data), `"note":"strong, paid"`) {
		t.Fatalf("view: %s", data)
	}
	ctx := context.Background()
	list, err := m.Models(ctx, "or")
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]Model{}
	for _, md := range list.Models {
		got[md.ID] = md
	}
	if b := got["big/coder"]; list.Kind != "openai" || b.Context != 200000 || !b.Caps.Vision || !b.Caps.Tools || !b.Caps.Thinking {
		t.Fatalf("listed model: %+v", b)
	}
	if s := got["small"]; s.Context != 32000 || !s.Caps.Tools || got["typed-only"].ID == "" || len(list.Models) != 3 {
		t.Fatalf("typed models: %+v", list.Models)
	}

	res, err := m.Chat(ctx, ChatRequest{Server: "or", Model: "big/coder", Messages: []Message{{Role: "user", Content: json.RawMessage(`"hi"`)}}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Message.Reasoning != "hm" || string(res.Message.Content) != `"Hi"` || res.Usage.Cost != 0.0042 || res.Usage.Cached != 4 || res.Usage.Prompt != 12 {
		t.Fatalf("result: %+v / %+v", res.Message, res.Usage)
	}
	for _, k := range []string{"timings_per_token", "return_progress", "chat_template_kwargs"} {
		if _, ok := body[k]; ok {
			t.Fatalf("llama.cpp field %s sent to a provider", k)
		}
	}

	// A rate limit is waited out; no credit is said.
	RetryDelay = time.Millisecond
	if _, err := m.Chat(ctx, ChatRequest{Server: "or", Model: "busy"}, nil); err != nil || limited.Load() != 3 {
		t.Fatalf("retry: %v after %d", err, limited.Load())
	}
	_, err = m.Chat(ctx, ChatRequest{Server: "or", Model: "broke"}, nil)
	if err == nil || i18n.Translate("fr", err) != "OpenRouter : plus de crédit : Insufficient credits" {
		t.Fatalf("402: %v", err)
	}
	srv.APIKey = "wrong"
	_ = m.SaveServer(srv, false)
	if _, err := m.Chat(ctx, ChatRequest{Server: "or", Model: "big/coder"}, nil); err == nil || err.Error() != "OpenRouter refused the API key: Incorrect API key" {
		t.Fatalf("401: %v", err)
	}

	// The default server of the sub-agents.
	if err := m.SetChildDefault("or", "small"); err != nil {
		t.Fatal(err)
	}
	if s, md := m.ChildDefault(); s != "or" || md != "small" || len(m.ForChildren()) != 1 {
		t.Fatalf("child default: %s %s", s, md)
	}
	_ = m.DeleteServer("or")
	if s, _ := m.ChildDefault(); s != "" {
		t.Fatal("default kept after the server was deleted")
	}
}
