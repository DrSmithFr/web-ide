package acp

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func fixture(t *testing.T, name string) json.RawMessage {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func near(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func TestPromptUsageClaude(t *testing.T) {
	got := promptUsage(fixture(t, "claude-result.json"), "default")
	// Two models in one turn: Opus and the Haiku of its sub-tasks.
	if len(got) != 2 || got[0].Model != "claude-haiku-5-5" || got[1].Model != "claude-opus-5-5" {
		t.Fatalf("%+v", got)
	}
	if o := got[1]; o.Input != 6 || o.Output != 415 || o.CacheRead != 45973 || o.CacheWrite != 10640 {
		t.Fatalf("opus: %+v", o)
	}
	c, ok := cost(got)
	want := (1193*0.1+21*0.5)/1e6 + (6*4+415*20+45973*0.2+10640*5)/1e6
	if !ok || !near(c, want) {
		t.Fatalf("cost %v %v, want %v", c, ok, want)
	}
}

func TestPromptUsageOpencode(t *testing.T) {
	got := promptUsage(fixture(t, "opencode-result.json"), "llama.cpp/Qwen3.8-Flash-Next")
	if len(got) != 1 || got[0].Model != "llama.cpp/Qwen3.8-Flash-Next" || got[0].Input != 11591 || got[0].Output != 19 {
		t.Fatalf("%+v", got)
	}
	if _, ok := cost(got); ok {
		t.Fatal("a local model has no price")
	}
}

func TestPiUsage(t *testing.T) {
	home := t.TempDir()
	old := piHome
	piHome = func() string { return home }
	defer func() { piHome = old }()
	file, _ := filepath.Abs(filepath.Join("testdata", "pi", "sessions", "s1.jsonl"))
	_ = os.MkdirAll(filepath.Join(home, "pi-acp"), 0o755)
	_ = os.WriteFile(filepath.Join(home, "pi-acp", "session-map.json"), []byte(`{"sessions":{"pi-1":{"sessionFile":"`+file+`"}}}`), 0o644)
	at := func(s string) int64 { tm, _ := time.Parse(time.RFC3339, s); return tm.UnixMilli() }
	got, reported := piUsage("pi-1", at("2026-10-13T09:55:40Z"), at("2026-10-13T09:56:00Z"))
	if len(got) != 1 || got[0].Model != "Qwen3.8-Flash-Next" || got[0].Input != 2450 || got[0].Output != 58 || reported != 0 {
		t.Fatalf("%+v %v", got, reported)
	}
	// Out of the turn: nothing.
	if got, _ := piUsage("pi-1", at("2026-10-13T10:00:00Z"), at("2026-10-13T11:00:00Z")); len(got) != 0 {
		t.Fatalf("%+v", got)
	}
}

func TestPrices(t *testing.T) {
	if p, ok := priceOf("anthropic/claude-sonnet-5-5"); !ok || p.Input != 2 {
		t.Fatalf("provider prefix: %+v %v", p, ok)
	}
	if p, ok := priceOf("claude-haiku-4-5-20251001"); !ok || p.Output != 5 {
		t.Fatalf("dated id: %+v %v", p, ok)
	}
	// The file of the user adds and replaces models, read again when it changes.
	file := filepath.Join(t.TempDir(), "prices.json")
	SetPriceFile(file)
	defer SetPriceFile("")
	_ = os.WriteFile(file, []byte(`{"qwen3.8-flash-next": {"input": 1, "output": 2}, "claude-opus-5-5": {"input": 40, "output": 200}}`), 0o644)
	if c, ok := cost([]Tokens{{Model: "local/Qwen3.8-Flash-Next", Input: 1e6, Output: 1e6}}); !ok || !near(c, 3) {
		t.Fatalf("added model: %v %v", c, ok)
	}
	if p, _ := priceOf("claude-opus-5-5"); p.Input != 40 {
		t.Fatalf("replaced model: %+v", p)
	}
}

func TestTotals(t *testing.T) {
	var tot Totals
	tot.add(Turn{Start: 0, End: 10000, Wait: 4000, Tokens: []Tokens{{Model: "claude-opus-5-5", Input: 1e6}}, Subscription: true})
	tot.add(Turn{Start: 0, End: 1000, Tokens: []Tokens{{Model: "local", Output: 50}}})
	tot.add(Turn{Start: 0, End: 1000, Reported: 0.5})
	if tot.Turns != 3 || tot.Work != 8000 || tot.Wait != 4000 || !near(tot.Cost, 4.5) || tot.Unpriced != 1 || !tot.APIEquivalent || tot.Tokens != 1e6+50 {
		t.Fatalf("%+v", tot)
	}
	if ticketOf("abc-t12") != 12 || ticketOf("abc") != 0 || ticketOf("abc-w1f2") != 0 {
		t.Fatal("ticketOf")
	}
}

// The turns of the fake agent are recorded with their time, waits and tokens, and summed per
// session, ticket and project.
func TestTurnsRecorded(t *testing.T) {
	dir := t.TempDir()
	bin := fakeAgent(t)
	root := filepath.Join(dir, "agents")
	m := NewManager(Deps{Owner: "p1-t7", Dir: filepath.Join(root, "p1-t7"), Root: dir,
		Command: func(string) ([]string, error) { return []string{bin}, nil }})
	defer m.CloseAll()
	s := open(t, m)
	_ = s.Prompt("hi")
	waitState(t, s, Ready)
	_ = s.Prompt("permission please")
	waitState(t, s, Asking)
	time.Sleep(300 * time.Millisecond)
	_, e := s.Thread()
	_ = s.Answer(len(e)-1, "allow-once")
	waitState(t, s, Ready)

	st := s.Info().Stats
	perTurn := (1000*4+200*20)/1e6 + 100*0.5/1e6
	if st == nil || st.Turns != 2 || !near(st.Cost, 2*perTurn) || !st.APIEquivalent || st.Wait < 300 {
		t.Fatalf("session stats: %+v", st)
	}
	data, _ := os.ReadFile(filepath.Join(root, "p1-t7", "turns.jsonl"))
	if lines := strings.Split(strings.TrimSpace(string(data)), "\n"); len(lines) != 2 || !strings.Contains(lines[0], `"ticket":7`) {
		t.Fatalf("turns.jsonl:\n%s", data)
	}
	// Kept once the session is closed; summed per ticket and per project.
	_ = m.Close(s.Info().ID)
	ticket := Sum(root, func(p string) bool { return p == "p1-t7" }, time.Time{})
	if ticket.Turns != 2 || !near(ticket.Cost, 2*perTurn) {
		t.Fatalf("ticket: %+v", ticket)
	}
	if later := Sum(root, func(p string) bool { return true }, time.Now().Add(time.Minute)); later.Turns != 0 {
		t.Fatalf("since: %+v", later)
	}
}
