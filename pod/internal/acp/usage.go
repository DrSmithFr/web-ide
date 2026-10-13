package acp

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// Tokens of a turn for one model.
type Tokens struct {
	Model      string `json:"model"`
	Input      int64  `json:"in,omitempty"`
	Output     int64  `json:"out,omitempty"`
	CacheRead  int64  `json:"cacheRead,omitempty"`
	CacheWrite int64  `json:"cacheWrite,omitempty"`
}

func (t Tokens) total() int64 { return t.Input + t.Output + t.CacheRead + t.CacheWrite }

// promptUsage reads the tokens of a turn from the answer of its session/prompt.
//   - claude-agent-acp: _meta.quota.model_usage, per model (its sub-agents included);
//   - opencode: usage, for the model of the session (model).
//
// pi reports nothing there: see piUsage.
func promptUsage(result json.RawMessage, model string) []Tokens {
	var r struct {
		Usage *struct {
			InputTokens       int64 `json:"inputTokens"`
			OutputTokens      int64 `json:"outputTokens"`
			CachedReadTokens  int64 `json:"cachedReadTokens"`
			CachedWriteTokens int64 `json:"cachedWriteTokens"`
		} `json:"usage"`
		Meta struct {
			Quota struct {
				ModelUsage []struct {
					Model      string `json:"model"`
					TokenCount struct {
						InputTokens       int64 `json:"inputTokens"`
						OutputTokens      int64 `json:"outputTokens"`
						CachedInputTokens int64 `json:"cachedInputTokens"`
						CachedWriteTokens int64 `json:"cachedWriteTokens"`
					} `json:"token_count"`
				} `json:"model_usage"`
			} `json:"quota"`
		} `json:"_meta"`
	}
	if json.Unmarshal(result, &r) != nil {
		return nil
	}
	var out []Tokens
	for _, m := range r.Meta.Quota.ModelUsage {
		c := m.TokenCount
		out = append(out, Tokens{Model: m.Model, Input: c.InputTokens, Output: c.OutputTokens, CacheRead: c.CachedInputTokens, CacheWrite: c.CachedWriteTokens})
	}
	if len(out) == 0 && r.Usage != nil {
		u := r.Usage
		out = append(out, Tokens{Model: model, Input: u.InputTokens, Output: u.OutputTokens, CacheRead: u.CachedReadTokens, CacheWrite: u.CachedWriteTokens})
	}
	return out
}

// piHome holds the files of pi (a variable for the tests).
var piHome = func() string { h, _ := os.UserHomeDir(); return filepath.Join(h, ".pi") }

// piUsage reads the tokens and the cost of the answers of a pi session between from and to
// (ms) in its session file: pi-acp maps the ACP session to it in pi-acp/session-map.json.
func piUsage(session string, from, to int64) ([]Tokens, float64) {
	var sm struct {
		Sessions map[string]struct {
			SessionFile string `json:"sessionFile"`
		} `json:"sessions"`
	}
	data, err := os.ReadFile(filepath.Join(piHome(), "pi-acp", "session-map.json"))
	if err != nil || json.Unmarshal(data, &sm) != nil {
		return nil, 0
	}
	f, err := os.Open(sm.Sessions[session].SessionFile)
	if err != nil {
		return nil, 0
	}
	defer f.Close()
	byModel := map[string]*Tokens{}
	var cost float64
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 16<<20)
	for sc.Scan() {
		var l struct {
			Type      string `json:"type"`
			Timestamp string `json:"timestamp"`
			Message   struct {
				Role  string `json:"role"`
				Model string `json:"model"`
				Usage *struct {
					Input      int64 `json:"input"`
					Output     int64 `json:"output"`
					CacheRead  int64 `json:"cacheRead"`
					CacheWrite int64 `json:"cacheWrite"`
					Cost       struct {
						Total float64 `json:"total"`
					} `json:"cost"`
				} `json:"usage"`
			} `json:"message"`
		}
		if json.Unmarshal(sc.Bytes(), &l) != nil || l.Type != "message" || l.Message.Role != "assistant" || l.Message.Usage == nil {
			continue
		}
		at, err := time.Parse(time.RFC3339Nano, l.Timestamp)
		if err != nil || at.UnixMilli() < from || at.UnixMilli() > to {
			continue
		}
		u := l.Message.Usage
		t := byModel[l.Message.Model]
		if t == nil {
			t = &Tokens{Model: l.Message.Model}
			byModel[l.Message.Model] = t
		}
		t.Input += u.Input
		t.Output += u.Output
		t.CacheRead += u.CacheRead
		t.CacheWrite += u.CacheWrite
		cost += u.Cost.Total
	}
	out := make([]Tokens, 0, len(byModel))
	for _, t := range byModel {
		out = append(out, *t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Model < out[j].Model })
	return out, cost
}
