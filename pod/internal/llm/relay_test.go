package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/DrSmithFr/web-ide/pod/internal/keeper"
)

func startKeeper(t *testing.T) string {
	t.Helper()
	dir, _ := os.MkdirTemp("", "kr")
	t.Cleanup(func() { os.RemoveAll(dir) })
	path := filepath.Join(dir, "k.sock")
	ln, err := keeper.Listen(path)
	if err != nil {
		t.Fatal(err)
	}
	s := keeper.NewServer("test")
	go s.Serve(ln)
	t.Cleanup(s.Close)
	return path
}

func relayOf(t *testing.T, path string) Relay {
	c, _, err := keeper.Dial(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.Close)
	return KeeperRelay{C: c}
}

// slowModel streams an answer with reasoning and a tool call, one chunk every 15 ms, in the
// OpenAI or the Ollama format; calls counts the completions asked.
func slowModel(t *testing.T, calls *int32) *httptest.Server {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/version":
			http.NotFound(w, r)
		case "/v1/chat/completions":
			atomic.AddInt32(calls, 1)
			w.Header().Set("Content-Type", "text/event-stream")
			for i := 0; i < 20; i++ {
				fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"reasoning_content\":\"r%d \"}}]}\n\n", i)
				fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"content\":\"c%d \"}}],\"timings\":{\"predicted_n\":%d,\"predicted_per_second\":30}}\n\n", i, i+1)
				w.(http.Flusher).Flush()
				time.Sleep(15 * time.Millisecond)
			}
			fmt.Fprint(w, `data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"a","type":"function","function":{"name":"read_file","arguments":"{\"path\":\"x\"}"}}]}}]}`+"\n\n")
			fmt.Fprint(w, `data: {"choices":[{"finish_reason":"tool_calls","delta":{}}],"usage":{"prompt_tokens":10,"completion_tokens":40}}`+"\n\n")
			fmt.Fprint(w, "data: [DONE]\n\n")
		case "/api/chat":
			atomic.AddInt32(calls, 1)
			for i := 0; i < 20; i++ {
				fmt.Fprintf(w, `{"message":{"role":"assistant","content":"c%d ","thinking":"r%d "},"done":false}`+"\n", i, i)
				w.(http.Flusher).Flush()
				time.Sleep(15 * time.Millisecond)
			}
			fmt.Fprint(w, `{"message":{"role":"assistant","content":""},"done":true,"done_reason":"stop","prompt_eval_count":10,"eval_count":40}`+"\n")
		}
	}))
	t.Cleanup(ts.Close)
	return ts
}

// A completion through the relay survives the pod: a new manager resumes it in the middle
// and ends with the answer of an uninterrupted run, the model asked once.
func TestResumeChat(t *testing.T) {
	for _, kind := range []string{"llamacpp", "ollama"} {
		t.Run(kind, func(t *testing.T) {
			path := startKeeper(t)
			var calls int32
			ts := slowModel(t, &calls)
			req := ChatRequest{Server: "s1", Model: "m", Messages: []Message{{Role: "user", Content: json.RawMessage(`"hi"`)}}}

			direct, id := newManager(t, ts.URL, kind)
			req.Server = id
			want, err := direct.Chat(context.Background(), req, nil)
			if err != nil {
				t.Fatal(err)
			}
			atomic.StoreInt32(&calls, 0)

			m1, _ := newManager(t, ts.URL, kind)
			m1.Relay = relayOf(t, path)
			if err := m1.StartChat("st", req); err != nil {
				t.Fatal(err)
			}
			var seen atomic.Value
			seen.Store("")
			ctx, stop := context.WithCancel(context.Background())
			go m1.WaitChat(ctx, "st", nil, func(d Delta) { seen.Store(seen.Load().(string) + d.Content) })
			deadline := time.Now().Add(5 * time.Second)
			for !strings.Contains(seen.Load().(string), "c5 ") && time.Now().Before(deadline) {
				time.Sleep(5 * time.Millisecond)
			}
			stop() // the pod stops: nobody cancels the job

			m2, _ := newManager(t, ts.URL, kind)
			m2.Relay = relayOf(t, path)
			started := time.Now().Add(-time.Minute)
			if err := m2.ResumeChat("st", req, started); err != nil {
				t.Fatal(err)
			}
			var snap Snapshot
			got, err := m2.WaitChat(context.Background(), "st", func(s Snapshot) { snap = s }, nil)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got.Message, want.Message) || got.Finish != want.Finish || !reflect.DeepEqual(got.Usage.Prompt, want.Usage.Prompt) {
				t.Fatalf("resumed:\n%+v\nwant:\n%+v", got, want)
			}
			if n := atomic.LoadInt32(&calls); n != 1 {
				t.Fatalf("the model was asked %d times", n)
			}
			if snap.StartedAt != started.UnixMilli() {
				t.Fatalf("started at %d, want %d", snap.StartedAt, started.UnixMilli())
			}
		})
	}
	// Without relay, nothing to resume.
	m, id := newManager(t, "http://127.0.0.1:1", "llamacpp")
	if err := m.ResumeChat("x", ChatRequest{Server: id, Model: "m"}, time.Now()); err == nil {
		t.Fatal("resumed without relay")
	}
}
