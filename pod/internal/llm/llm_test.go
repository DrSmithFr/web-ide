package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"webide/pod/internal/fsx"
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
				`{"choices":[{"delta":{"content":null}}],"prompt_progress":{"total":40,"processed":20}}`,
				`{"choices":[{"delta":{"reasoning_content":"Je "}}]}`,
				`{"choices":[{"delta":{"reasoning_content":"réfléchis"}}]}`,
				`{"choices":[{"delta":{"content":"Voici"}}],"timings":{"predicted_n":7,"predicted_per_second":31.5}}`,
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
	var tokens, total int
	var speed float64
	for _, d := range deltas {
		tokens, speed, total = max(tokens, d.Tokens), max(speed, d.Speed), max(total, d.PromptTotal)
	}
	if tokens < 7 || speed != 31.5 || total != 40 {
		t.Fatalf("stats: tokens=%d speed=%v prompt=%d %+v", tokens, speed, total, deltas)
	}
	if got["timings_per_token"] != true || got["return_progress"] != true {
		t.Fatalf("llama.cpp options missing: %v", got)
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
	// A conversation of the JSON era is imported.
	_ = m.st.WriteFile("chats/p1/old.json", []byte(`{"id":"old","title":"Ancienne","updated":0,"messages":[{"role":"user","content":"hé"}]}`))
	root := t.TempDir()
	loc := ChatLocation{Project: "p1", IdeDir: filepath.Join(root, ".ide")}
	if err := m.SaveChat(loc, json.RawMessage(`{"id":"c1","title":"Un","created":1,"updated":1,"model":"m","pinned":true,"messages":[{"role":"user","content":"a"},{"role":"assistant","content":"b","usage":{"prompt":3}}]}`)); err != nil {
		t.Fatal(err)
	}
	_ = m.SaveChat(loc, json.RawMessage(`{"id":"c2","title":"Deux","updated":2,"messages":[]}`))
	if err := m.SaveChat(loc, json.RawMessage(`{"id":"../x"}`)); err == nil {
		t.Fatal("bad id accepted")
	}
	list, _ := m.ListChats(loc)
	if len(list) != 3 || list[0].ID != "c2" || list[2].ID != "old" {
		t.Fatalf("list: %+v", list)
	}
	if _, err := os.Stat(m.st.Path("chats/p1/old.json")); !os.IsNotExist(err) {
		t.Fatal("JSON file not removed after import")
	}
	data, err := m.GetChat(loc, "c1")
	var c struct {
		Model    string
		Pinned   bool
		Messages []map[string]any
	}
	if err != nil || json.Unmarshal(data, &c) != nil || c.Model != "m" || !c.Pinned || len(c.Messages) != 2 || c.Messages[1]["usage"] == nil {
		t.Fatalf("get: %s %v", data, err)
	}
	// Saving again replaces the messages.
	_ = m.SaveChat(loc, json.RawMessage(`{"id":"c1","title":"Un","updated":3,"messages":[{"role":"user","content":"a"}]}`))
	data, _ = m.GetChat(loc, "c1")
	_ = json.Unmarshal(data, &c)
	if len(c.Messages) != 1 {
		t.Fatalf("resave: %s", data)
	}
	if _, err := os.Stat(filepath.Join(root, ".ide/chats.db")); err != nil {
		t.Fatal("base not in .ide:", err)
	}
	gi, _ := os.ReadFile(filepath.Join(root, ".ide/.gitignore"))
	if !strings.Contains(string(gi), "chats.db") {
		t.Fatalf(".gitignore: %q", gi)
	}
	if err := m.RenameChat(loc, "c2", " Renommée "); err != nil {
		t.Fatal(err)
	}
	if list, _ := m.ListChats(loc); list[1].Title != "Renommée" {
		t.Fatalf("rename: %+v", list)
	}
	_ = m.DeleteChat(loc, "c1")
	if list, _ := m.ListChats(loc); len(list) != 2 {
		t.Fatalf("after delete: %+v", list)
	}
	// Remote project: base in the pod data.
	if list, err := m.ListChats(ChatLocation{Project: "p2"}); err != nil || len(list) != 0 {
		t.Fatalf("remote project: %v %v", list, err)
	}
	if _, err := os.Stat(m.st.Path("chats/p2.db")); err != nil {
		t.Fatal(err)
	}
	m.Close()
}

func TestContext(t *testing.T) {
	m, _ := newManager(t, "h:1", "llamacpp")
	home := t.TempDir()
	t.Setenv("HOME", home)
	root := t.TempDir()
	write := func(p, s string) {
		_ = os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(home, ".claude/CLAUDE.md"), "Toujours en français.")
	write(filepath.Join(home, ".claude/skills/pdf/SKILL.md"), "---\nname: pdf\ndescription: >\n  Lire et créer\n  des PDF\n---\n# PDF\nUtiliser pdftotext.")
	write(filepath.Join(home, ".claude/skills/pdf/scripts/x.py"), "print(1)")
	write(filepath.Join(root, "CLAUDE.md"), "Projet Go. Voir @docs/style.md et `@ignored`.\n```\n@docs/none.md\n```")
	write(filepath.Join(root, "docs/style.md"), "Tabulations.")
	write(filepath.Join(root, "AGENTS.md"), "Tests avec make test.")
	write(filepath.Join(root, ".claude/skills/deploy/SKILL.md"), "---\nname: deploy\ndescription: \"Déployer le projet\"\n---\nmake deploy")
	write(filepath.Join(root, ".agents/skills/pdf/SKILL.md"), "---\nname: pdf\ndescription: PDF du projet\n---\nlocal")
	write(filepath.Join(root, ".ide/system-prompt.md"), "Prompt du projet {{project}}")
	_ = m.SaveGlobalPrompt("Prompt global")
	write(m.st.Path("AGENTS.md"), "Règles de l'IDE.")
	write(m.st.Path("skills/notes/SKILL.md"), "---\nname: notes\ndescription: Notes de l'IDE\n---\nnoter")
	write(m.st.Path("skills/pdf/SKILL.md"), "---\nname: pdf\ndescription: PDF de l'IDE\n---\nide")

	p := Project{Root: root, FS: fsx.Local{}}
	c := m.LoadContext(p)
	if c.GlobalPrompt == nil || *c.GlobalPrompt != "Prompt global" || c.ProjectPrompt == nil || !strings.Contains(*c.ProjectPrompt, "{{project}}") {
		t.Fatalf("prompts: %+v", c)
	}
	var paths []string
	for _, f := range c.Files {
		paths = append(paths, f.Scope+":"+filepath.Base(f.Path))
	}
	if strings.Join(paths, ",") != "global:CLAUDE.md,global:AGENTS.md,project:CLAUDE.md,project:style.md,project:AGENTS.md" {
		t.Fatalf("files: %v", paths)
	}
	if len(c.Skills) != 3 || c.Skills[0].Name != "deploy" || c.Skills[0].Description != "Déployer le projet" || c.Skills[1].Name != "notes" || c.Skills[2].Scope != "project" || c.Skills[2].Description != "PDF du projet" {
		t.Fatalf("skills: %+v", c.Skills)
	}
	sk, err := m.ReadSkill(p, "deploy")
	if err != nil || sk["content"] != "make deploy" {
		t.Fatalf("read skill: %v %v", sk, err)
	}
	// Global skill once the project one is gone.
	_ = os.RemoveAll(filepath.Join(root, ".agents"))
	// The IDE folder wins over ~/.claude.
	if sk, err = m.ReadSkill(p, "pdf"); err != nil || sk["content"] != "ide" {
		t.Fatalf("IDE skill: %v %v", sk, err)
	}
	_ = os.RemoveAll(m.st.Path("skills/pdf"))
	sk, err = m.ReadSkill(p, "pdf")
	if err != nil || sk["content"] != "# PDF\nUtiliser pdftotext." || len(sk["files"].([]string)) != 1 {
		t.Fatalf("global skill: %v %v", sk, err)
	}
	if desc := m.LoadContext(p).Skills[2].Description; desc != "Lire et créer des PDF" {
		t.Fatalf("folded description: %q", desc)
	}
	if txt, err := m.ReadSkillFile(p, "pdf", "scripts/x.py"); err != nil || txt != "print(1)" {
		t.Fatalf("skill file: %q %v", txt, err)
	}
	if _, err := m.ReadSkillFile(p, "pdf", "../../CLAUDE.md"); err == nil {
		t.Fatal("escape accepted")
	}
}

func TestJobSurvivesDetach(t *testing.T) {
	release := make(chan struct{})
	var hits atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"début \"}}]}\n\n")
		w.(http.Flusher).Flush()
		if hits.Add(1) > 1 {
			<-r.Context().Done() // second job: never ends by itself
			return
		}
		select {
		case <-release:
		case <-r.Context().Done():
			return
		}
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"fin\"}}]}\n\ndata: [DONE]\n\n")
	}))
	defer ts.Close()
	m, id := newManager(t, ts.URL, "llamacpp")
	req := ChatRequest{Server: id, Model: "m", Messages: []Message{{Role: "user", Content: json.RawMessage(`"x"`)}}}
	if err := m.StartChat("s1", req); err != nil {
		t.Fatal(err)
	}
	// First page: leaves (its context ends) while the answer is being written.
	ctx, cancel := context.WithCancel(context.Background())
	got := make(chan struct{}, 1)
	go func() {
		_, _ = m.WaitChat(ctx, "s1", nil, func(d Delta) {
			if d.Content != "" {
				select {
				case got <- struct{}{}:
				default:
				}
			}
		})
	}()
	<-got
	cancel()
	// Second page: gets what was written, then the end.
	time.Sleep(50 * time.Millisecond)
	var snap Snapshot
	go func() {
		time.Sleep(50 * time.Millisecond)
		close(release)
	}()
	res, err := m.WaitChat(context.Background(), "s1", func(s Snapshot) { snap = s }, func(Delta) {})
	if err != nil || snap.Content != "début " || string(res.Message.Content) != `"début fin"` || snap.StartedAt == 0 {
		t.Fatalf("attach: snap=%+v res=%+v err=%v", snap, res, err)
	}
	if _, err := m.WaitChat(context.Background(), "inconnu", nil, nil); err == nil {
		t.Fatal("unknown stream accepted")
	}
	// Stop cancels the job.
	if err := m.StartChat("s2", req); err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond)
	m.CancelChat("s2")
	if _, err := m.WaitChat(context.Background(), "s2", nil, func(Delta) {}); err == nil {
		t.Fatal("cancelled job ended without error")
	}
}
