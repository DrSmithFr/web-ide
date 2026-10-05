package llm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/DrSmithFr/web-ide/pod/internal/i18n"
)

// Message is an OpenAI chat message. Content is a string or an array of parts
// (text, image_url, input_audio, input_video), kept raw to pass it through.
type Message struct {
	Role       string          `json:"role"`
	Content    json.RawMessage `json:"content,omitempty"`
	Reasoning  string          `json:"reasoning_content,omitempty"`
	ToolCalls  []ToolCall      `json:"tool_calls,omitempty"`
	ToolCallID string          `json:"tool_call_id,omitempty"`
	Name       string          `json:"name,omitempty"`
}

type ToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

type ChatRequest struct {
	Server   string            `json:"server"`
	Model    string            `json:"model"`
	Messages []Message         `json:"messages"`
	Tools    []json.RawMessage `json:"tools,omitempty"`
	// Think: nil leaves the model default.
	Think       *bool    `json:"think,omitempty"`
	Temperature *float64 `json:"temperature,omitempty"`
}

type Usage struct {
	Prompt     int     `json:"prompt"`
	Completion int     `json:"completion"`
	Cached     int     `json:"cached,omitempty"`
	PerSecond  float64 `json:"perSecond,omitempty"`
	DurationMs float64 `json:"durationMs,omitempty"`
	// PromptPerSecond: speed of the prompt reading (tokens not in the cache).
	PromptPerSecond float64 `json:"promptPerSecond,omitempty"`
}

type ChatResult struct {
	Message Message `json:"message"`
	Finish  string  `json:"finish,omitempty"`
	Usage   *Usage  `json:"usage,omitempty"`
}

// Delta is a piece of the answer pushed while it streams. Tool is the name of a tool
// call being written (its arguments are not streamed). The counters are totals so far:
// tokens generated, speed told by the server (0: the page computes it), and the progress
// of the prompt reading (llama.cpp): tokens read, total, tokens found in the cache, speed.
type Delta struct {
	Content     string  `json:"content,omitempty"`
	Reasoning   string  `json:"reasoning,omitempty"`
	Tool        string  `json:"tool,omitempty"`
	Tokens      int     `json:"tokens,omitempty"`
	Speed       float64 `json:"speed,omitempty"`
	PromptDone  int     `json:"promptDone,omitempty"`
	PromptTotal int     `json:"promptTotal,omitempty"`
	PromptCache int     `json:"promptCache,omitempty"`
	PromptSpeed float64 `json:"promptSpeed,omitempty"`
}

// addCounters copies the counters of d (totals: the last value wins) into acc.
func addCounters(acc *Delta, d Delta) {
	if d.Tokens > 0 {
		acc.Tokens = d.Tokens
	}
	if d.Speed > 0 {
		acc.Speed = d.Speed
	}
	if d.PromptTotal > 0 {
		acc.PromptDone, acc.PromptTotal, acc.PromptCache = d.PromptDone, d.PromptTotal, d.PromptCache
	}
	if d.PromptSpeed > 0 {
		acc.PromptSpeed = d.PromptSpeed
	}
}

// Chat runs one completion. onDelta receives the pieces grouped every ~40 ms.
func (m *Manager) Chat(ctx context.Context, req ChatRequest, onDelta func(Delta)) (*ChatResult, error) {
	s, err := m.server(req.Server)
	if err != nil {
		return nil, err
	}
	if req.Model == "" {
		return nil, i18n.New("no model chosen")
	}
	b := newBatcher(onDelta)
	defer b.flush()
	if m.kind(ctx, s) == "ollama" {
		return m.ollamaChat(ctx, s, req, b)
	}
	return m.openaiChat(ctx, s, req, b)
}

// batcher groups the deltas so that a fast model does not send one message per token; a
// timer sends what is waiting after 40 ms, so a pause of the model does not hold it back.
type batcher struct {
	fn    func(Delta)
	mu    sync.Mutex
	cur   Delta
	timer *time.Timer
}

func newBatcher(fn func(Delta)) *batcher { return &batcher{fn: fn} }

func (b *batcher) add(d Delta) {
	if b.fn == nil {
		return
	}
	b.mu.Lock()
	b.cur.Content += d.Content
	b.cur.Reasoning += d.Reasoning
	if d.Tool != "" {
		b.cur.Tool = d.Tool
	}
	addCounters(&b.cur, d)
	if b.timer == nil {
		b.timer = time.AfterFunc(40*time.Millisecond, b.flush)
	}
	b.mu.Unlock()
}

func (b *batcher) flush() {
	if b.fn == nil {
		return
	}
	b.mu.Lock()
	if b.timer != nil {
		b.timer.Stop()
		b.timer = nil
	}
	d := b.cur
	b.cur = Delta{}
	b.mu.Unlock()
	if d != (Delta{}) {
		b.fn(d)
	}
}

// ---------- OpenAI compatible (llama.cpp) ----------

func (m *Manager) openaiChat(ctx context.Context, s Server, req ChatRequest, b *batcher) (*ChatResult, error) {
	body := map[string]any{
		"model":          req.Model,
		"messages":       req.Messages,
		"stream":         true,
		"stream_options": map[string]bool{"include_usage": true},
	}
	if len(req.Tools) > 0 {
		body["tools"] = req.Tools
	}
	if req.Temperature != nil {
		body["temperature"] = *req.Temperature
	}
	if req.Think != nil {
		body["chat_template_kwargs"] = map[string]bool{"enable_thinking": *req.Think}
	}
	// llama.cpp: timings in every chunk and progress of the prompt reading (other servers
	// ignore these fields; tokens are then counted from the chunks).
	body["timings_per_token"] = true
	body["return_progress"] = true
	resp, err := m.do(ctx, s, http.MethodPost, "/v1/chat/completions", body)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var content, reasoning strings.Builder
	var calls []ToolCall
	res := &ChatResult{}
	chunks, serverTokens := 0, 0
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 64*1024), 16<<20)
	for sc.Scan() {
		line := sc.Bytes()
		if !bytes.HasPrefix(line, []byte("data:")) {
			continue
		}
		data := bytes.TrimSpace(line[5:])
		if string(data) == "[DONE]" {
			break
		}
		var chunk struct {
			Choices []struct {
				FinishReason string `json:"finish_reason"`
				Delta        struct {
					Content   string `json:"content"`
					Reasoning string `json:"reasoning_content"`
					ToolCalls []struct {
						Index    int    `json:"index"`
						ID       string `json:"id"`
						Type     string `json:"type"`
						Function struct {
							Name      string `json:"name"`
							Arguments string `json:"arguments"`
						} `json:"function"`
					} `json:"tool_calls"`
				} `json:"delta"`
			} `json:"choices"`
			Usage *struct {
				Prompt     int `json:"prompt_tokens"`
				Completion int `json:"completion_tokens"`
				Details    *struct {
					Cached int `json:"cached_tokens"`
				} `json:"prompt_tokens_details"`
			} `json:"usage"`
			Timings *struct {
				CacheN             int     `json:"cache_n"`
				PromptN            int     `json:"prompt_n"`
				PromptMs           float64 `json:"prompt_ms"`
				PromptPerSecond    float64 `json:"prompt_per_second"`
				PredictedN         int     `json:"predicted_n"`
				PredictedMs        float64 `json:"predicted_ms"`
				PredictedPerSecond float64 `json:"predicted_per_second"`
			} `json:"timings"`
			// processed counts the tokens found in the cache too.
			Progress *struct {
				Total     int     `json:"total"`
				Cache     int     `json:"cache"`
				Processed int     `json:"processed"`
				TimeMs    float64 `json:"time_ms"`
			} `json:"prompt_progress"`
			Error json.RawMessage `json:"error"`
		}
		if json.Unmarshal(data, &chunk) != nil {
			continue
		}
		if len(chunk.Error) > 0 {
			return nil, errors.New(errorMessage(data))
		}
		stats := Delta{}
		if t := chunk.Timings; t != nil && t.PredictedN > 0 {
			serverTokens = t.PredictedN
			stats.Speed = t.PredictedPerSecond
		}
		stats.Tokens = serverTokens
		if p := chunk.Progress; p != nil && p.Total > 0 {
			stats.PromptDone, stats.PromptTotal, stats.PromptCache = p.Processed, p.Total, p.Cache
			if p.TimeMs > 0 && p.Processed > p.Cache {
				stats.PromptSpeed = float64(p.Processed-p.Cache) / (p.TimeMs / 1000)
			}
		}
		if t := chunk.Timings; t != nil && t.PromptN+t.CacheN > 0 {
			total := t.PromptN + t.CacheN
			stats.PromptDone, stats.PromptTotal, stats.PromptCache = total, total, t.CacheN
			stats.PromptSpeed = t.PromptPerSecond
		}
		for _, c := range chunk.Choices {
			d := c.Delta
			content.WriteString(d.Content)
			reasoning.WriteString(d.Reasoning)
			delta := stats
			delta.Content, delta.Reasoning = d.Content, d.Reasoning
			if d.Content != "" || d.Reasoning != "" || len(d.ToolCalls) > 0 {
				chunks++
				if serverTokens == 0 {
					delta.Tokens = chunks // no timings from this server: about one token per chunk
				}
			}
			for _, tc := range d.ToolCalls {
				for len(calls) <= tc.Index {
					calls = append(calls, ToolCall{Type: "function"})
				}
				call := &calls[tc.Index]
				if tc.ID != "" {
					call.ID = tc.ID
				}
				if tc.Function.Name != "" {
					call.Function.Name += tc.Function.Name
					delta.Tool = call.Function.Name
				}
				call.Function.Arguments += tc.Function.Arguments
			}
			b.add(delta)
			if c.FinishReason != "" {
				res.Finish = c.FinishReason
			}
		}
		if len(chunk.Choices) == 0 && stats != (Delta{}) {
			b.add(stats)
		}
		if chunk.Usage != nil {
			u := &Usage{Prompt: chunk.Usage.Prompt, Completion: chunk.Usage.Completion}
			if chunk.Usage.Details != nil {
				u.Cached = chunk.Usage.Details.Cached
			}
			if t := chunk.Timings; t != nil {
				u.PerSecond = t.PredictedPerSecond
				u.DurationMs = t.PredictedMs + t.PromptMs
				u.PromptPerSecond = t.PromptPerSecond
				if u.Cached == 0 {
					u.Cached = t.CacheN
				}
			}
			res.Usage = u
		}
	}
	if err := sc.Err(); err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, err
	}
	res.Message = assistantMessage(content.String(), reasoning.String(), calls)
	return res, nil
}

func assistantMessage(content, reasoning string, calls []ToolCall) Message {
	msg := Message{Role: "assistant", Reasoning: reasoning}
	if content != "" || len(calls) == 0 {
		msg.Content, _ = json.Marshal(content)
	}
	for i := range calls {
		if calls[i].ID == "" {
			calls[i].ID = fmt.Sprintf("call_%d_%d", time.Now().UnixNano(), i)
		}
		if calls[i].Function.Arguments == "" {
			calls[i].Function.Arguments = "{}"
		}
	}
	msg.ToolCalls = calls
	return msg
}

// ---------- Ollama (native API, for num_ctx and think) ----------

type ollamaMessage struct {
	Role      string           `json:"role"`
	Content   string           `json:"content"`
	Thinking  string           `json:"thinking,omitempty"`
	Images    []string         `json:"images,omitempty"`
	ToolCalls []ollamaToolCall `json:"tool_calls,omitempty"`
	ToolName  string           `json:"tool_name,omitempty"`
}

type ollamaToolCall struct {
	Function struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	} `json:"function"`
}

// toOllama converts OpenAI messages: image parts become base64 in images, tool call
// arguments become objects, tool results carry the tool name.
func toOllama(msgs []Message) []ollamaMessage {
	names := map[string]string{}
	out := make([]ollamaMessage, 0, len(msgs))
	for _, msg := range msgs {
		om := ollamaMessage{Role: msg.Role, Thinking: msg.Reasoning}
		var text string
		if json.Unmarshal(msg.Content, &text) == nil {
			om.Content = text
		} else {
			var parts []struct {
				Type     string `json:"type"`
				Text     string `json:"text"`
				ImageURL struct {
					URL string `json:"url"`
				} `json:"image_url"`
			}
			_ = json.Unmarshal(msg.Content, &parts)
			var texts []string
			for _, p := range parts {
				switch p.Type {
				case "text":
					texts = append(texts, p.Text)
				case "image_url":
					if i := strings.Index(p.ImageURL.URL, "base64,"); i >= 0 {
						om.Images = append(om.Images, p.ImageURL.URL[i+7:])
					}
				}
			}
			om.Content = strings.Join(texts, "\n\n")
		}
		for _, tc := range msg.ToolCalls {
			names[tc.ID] = tc.Function.Name
			var call ollamaToolCall
			call.Function.Name = tc.Function.Name
			args := json.RawMessage(tc.Function.Arguments)
			if !json.Valid(args) {
				args = json.RawMessage("{}")
			}
			call.Function.Arguments = args
			om.ToolCalls = append(om.ToolCalls, call)
		}
		if msg.Role == "tool" {
			om.ToolName = names[msg.ToolCallID]
			if om.ToolName == "" {
				om.ToolName = msg.Name
			}
		}
		out = append(out, om)
	}
	return out
}

func (m *Manager) ollamaChat(ctx context.Context, s Server, req ChatRequest, b *batcher) (*ChatResult, error) {
	body := map[string]any{
		"model":    req.Model,
		"messages": toOllama(req.Messages),
		"stream":   true,
	}
	if len(req.Tools) > 0 {
		body["tools"] = req.Tools
	}
	if req.Think != nil {
		body["think"] = *req.Think
	}
	opts := map[string]any{}
	if s.Context > 0 {
		opts["num_ctx"] = s.Context
	}
	if req.Temperature != nil {
		opts["temperature"] = *req.Temperature
	}
	if len(opts) > 0 {
		body["options"] = opts
	}
	resp, err := m.do(ctx, s, http.MethodPost, "/api/chat", body)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var content, reasoning strings.Builder
	var calls []ToolCall
	res := &ChatResult{}
	chunks := 0
	dec := json.NewDecoder(resp.Body)
	for {
		var chunk struct {
			Message    ollamaMessage `json:"message"`
			Done       bool          `json:"done"`
			DoneReason string        `json:"done_reason"`
			PromptN    int           `json:"prompt_eval_count"`
			PromptNs   float64       `json:"prompt_eval_duration"`
			EvalN      int           `json:"eval_count"`
			EvalNs     float64       `json:"eval_duration"`
			TotalNs    float64       `json:"total_duration"`
			Error      string        `json:"error"`
		}
		if err := dec.Decode(&chunk); err != nil {
			if err == io.EOF {
				break
			}
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			return nil, err
		}
		if chunk.Error != "" {
			return nil, errors.New(chunk.Error)
		}
		mm := chunk.Message
		content.WriteString(mm.Content)
		reasoning.WriteString(mm.Thinking)
		delta := Delta{Content: mm.Content, Reasoning: mm.Thinking}
		if mm.Content != "" || mm.Thinking != "" {
			chunks++ // Ollama sends one token per chunk
			delta.Tokens = chunks
		}
		for _, tc := range mm.ToolCalls {
			call := ToolCall{Type: "function"}
			call.Function.Name = tc.Function.Name
			call.Function.Arguments = string(tc.Function.Arguments)
			calls = append(calls, call)
			delta.Tool = call.Function.Name
		}
		b.add(delta)
		if chunk.Done {
			res.Finish = chunk.DoneReason
			if len(calls) > 0 {
				res.Finish = "tool_calls"
			}
			u := &Usage{Prompt: chunk.PromptN, Completion: chunk.EvalN, DurationMs: chunk.TotalNs / 1e6}
			if chunk.EvalNs > 0 {
				u.PerSecond = float64(chunk.EvalN) / (chunk.EvalNs / 1e9)
			}
			if chunk.PromptNs > 0 {
				u.PromptPerSecond = float64(chunk.PromptN) / (chunk.PromptNs / 1e9)
			}
			res.Usage = u
			break
		}
	}
	res.Message = assistantMessage(content.String(), reasoning.String(), calls)
	return res, nil
}
