package acp

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/DrSmithFr/web-ide/pod/internal/keeper"
)

// The fake agent of the browser tests (a node script).
func fakeAgent(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node is needed by the fake agent")
	}
	p, err := filepath.Abs("../../../e2e/bin/claude-agent-acp")
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// events records what the manager sends.
type recorder struct {
	mu     sync.Mutex
	events []string
}

func (r *recorder) emit(name string, data any) {
	b, _ := json.Marshal(data)
	r.mu.Lock()
	r.events = append(r.events, name+" "+string(b))
	r.mu.Unlock()
}

func newManager(t *testing.T, dir string, k *keeper.Client) (*Manager, *recorder) {
	t.Helper()
	bin := fakeAgent(t)
	rec := &recorder{}
	m := NewManager(Deps{Keeper: k, Owner: "p1", Dir: filepath.Join(dir, "state"), Root: dir,
		Command: func(string) ([]string, error) { return []string{bin}, nil }, Emit: rec.emit, Version: "test"})
	return m, rec
}

func waitFor(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("timeout: %s", what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func waitState(t *testing.T, s *Session, state string) {
	t.Helper()
	waitFor(t, "state "+state, func() bool { return s.Info().State == state })
}

func kinds(entries []Entry) string {
	var out []string
	for _, e := range entries {
		out = append(out, e.Kind)
	}
	return strings.Join(out, ",")
}

func open(t *testing.T, m *Manager) *Session {
	t.Helper()
	info, err := m.Create("claude", "")
	if err != nil {
		t.Fatal(err)
	}
	s, err := m.Get(info.ID)
	if err != nil {
		t.Fatal(err)
	}
	waitState(t, s, Ready)
	return s
}

func TestPromptStreamsTheThread(t *testing.T) {
	dir := t.TempDir()
	m, rec := newManager(t, dir, nil)
	defer m.CloseAll()
	s := open(t, m)
	if info := s.Info(); len(info.Config) != 1 || info.Config[0].CurrentValue != "default" || !strings.Contains(string(info.Commands), "review") {
		t.Fatalf("info after session/new: %+v", info)
	}
	if err := s.Prompt("hi"); err != nil {
		t.Fatal(err)
	}
	if err := s.Prompt("again"); err == nil {
		t.Fatal("a second prompt during a turn is refused")
	}
	waitState(t, s, Ready)
	info, entries := s.Thread()
	if got := kinds(entries); got != "user,thought,plan,agent,tool" {
		t.Fatalf("entries: %s", got)
	}
	if entries[3].Text != "Hello from the **fake agent**." || len(entries[2].Plan) != 2 {
		t.Fatalf("message or plan: %+v", entries)
	}
	tool := entries[4]
	if tool.Status != "completed" || len(tool.Content) != 1 || tool.Content[0].Type != "diff" || *tool.Content[0].OldText != "hi\nthere" {
		t.Fatalf("tool call: %+v", tool)
	}
	if info.Title != "Fake session" || info.Usage == nil || info.Usage.Used != 1234 {
		t.Fatalf("info: %+v", info)
	}
	rec.mu.Lock()
	all := strings.Join(rec.events, "\n")
	rec.mu.Unlock()
	if !strings.Contains(all, `agents.append {"id":"`+info.ID) || !strings.Contains(all, `"text":"from the **fake agent**."`) {
		t.Fatalf("the chunk is sent as an append:\n%s", all)
	}
	// A config option.
	if err := s.SetConfig("mode", "bypass"); err != nil {
		t.Fatal(err)
	}
	if s.Info().Config[0].CurrentValue != "bypass" {
		t.Fatalf("config: %+v", s.Info().Config)
	}
}

func TestPermissionAndCancel(t *testing.T) {
	dir := t.TempDir()
	m, _ := newManager(t, dir, nil)
	defer m.CloseAll()
	s := open(t, m)
	_ = s.Prompt("permission please")
	waitState(t, s, Asking)
	_, entries := s.Thread()
	last := len(entries) - 1
	if entries[last].Kind != "permission" || len(entries[last].Options) != 3 || entries[last].Title != "Edit hello.txt" {
		t.Fatalf("permission: %+v", entries[last])
	}
	if err := s.Answer(last, "allow-once"); err != nil {
		t.Fatal(err)
	}
	waitState(t, s, Ready)
	_, entries = s.Thread()
	if entries[1].Status != "completed" || entries[last].Outcome != "allow-once" || entries[len(entries)-1].Text != "Edited." {
		t.Fatalf("after the answer: %+v", entries)
	}
	if err := s.Answer(last, "reject"); err == nil {
		t.Fatal("a question answered is closed")
	}

	// Stop during a turn.
	_ = s.Prompt("slow")
	waitFor(t, "ticks", func() bool { _, e := s.Thread(); return strings.Contains(e[len(e)-1].Text, "tick 2") })
	if err := s.Cancel(); err != nil {
		t.Fatal(err)
	}
	waitState(t, s, Ready)
	_, entries = s.Thread()
	if e := entries[len(entries)-1]; e.Kind != "notice" || e.Text != "Stopped." {
		t.Fatalf("after stop: %+v", e)
	}
}

func TestCrashRestartsAndLoads(t *testing.T) {
	dir := t.TempDir()
	m, _ := newManager(t, dir, nil)
	defer m.CloseAll()
	s := open(t, m)
	_ = s.Prompt("hi")
	waitState(t, s, Ready)
	_ = s.Prompt("crash")
	waitFor(t, "restart notice", func() bool {
		_, e := s.Thread()
		return strings.Contains(kinds(e), "notice,notice") && s.Info().State == Ready
	})
	_, entries := s.Thread()
	for _, e := range entries {
		if strings.Contains(e.Text, "replayed") {
			t.Fatalf("the replay of session/load is dropped: %+v", entries)
		}
	}
	if err := s.Prompt("hi again"); err != nil {
		t.Fatal(err)
	}
	waitState(t, s, Ready)

	// Crashing again and again stops with the error output.
	_ = s.Prompt("crash loop")
	waitState(t, s, Failed)
	info := s.Info()
	if !info.Crashed || !strings.Contains(info.Error, "crash loop") {
		t.Fatalf("failed: %+v", info)
	}
	os.Remove(filepath.Join(dir, ".fake-acp-crash"))
	_ = s.Restart()
	waitState(t, s, Ready)
}

func TestCloseRemovesTheSession(t *testing.T) {
	dir := t.TempDir()
	m, rec := newManager(t, dir, nil)
	s := open(t, m)
	id := s.Info().ID
	if err := m.Close(id); err != nil {
		t.Fatal(err)
	}
	if len(m.List()) != 0 {
		t.Fatal("closed")
	}
	if _, err := os.Stat(m.file(id)); !os.IsNotExist(err) {
		t.Fatalf("its file is removed: %v", err)
	}
	rec.mu.Lock()
	defer rec.mu.Unlock()
	if !strings.Contains(rec.events[len(rec.events)-1], "agents.closed") {
		t.Fatalf("events: %v", rec.events)
	}
}

// With a keeper, the next pod follows the agent again from where its output was read.
func TestAdoptAfterPodRestart(t *testing.T) {
	dir := t.TempDir()
	sock := filepath.Join(dir, "k.sock")
	ln, err := keeper.Listen(sock)
	if err != nil {
		t.Fatal(err)
	}
	srv := keeper.NewServer("test")
	go srv.Serve(ln)
	defer srv.Close()
	k, _, err := keeper.Dial(sock)
	if err != nil {
		t.Fatal(err)
	}
	defer k.Close()

	m, _ := newManager(t, dir, k)
	s := open(t, m)
	id := s.Info().ID
	_ = s.Prompt("slow")
	waitFor(t, "ticks", func() bool { _, e := s.Thread(); return strings.Contains(e[len(e)-1].Text, "tick 1") })
	m.Release()

	// The next pod.
	m2, _ := newManager(t, dir, k)
	defer m2.CloseAll()
	s2, err := m2.Get(id)
	if err != nil {
		t.Fatal(err)
	}
	if st := s2.Info().State; st != Busy {
		t.Fatalf("the turn goes on: %s", st)
	}
	waitFor(t, "more ticks", func() bool { _, e := s2.Thread(); return strings.Contains(e[len(e)-1].Text, "tick 8 ") })
	_, entries := s2.Thread()
	text := entries[len(entries)-1].Text
	if strings.Count(text, "tick 1 ") != 1 || strings.Contains(text, "tick 1 tick 1") {
		t.Fatalf("no tick lost or repeated: %q", text)
	}
	if err := s2.Cancel(); err != nil {
		t.Fatal(err)
	}
	waitState(t, s2, Ready)
	if err := s2.Prompt("hi"); err != nil {
		t.Fatal(err)
	}
	waitState(t, s2, Ready)
}

func TestCatalog(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "node_modules", ".bin")
	_ = os.MkdirAll(bin, 0o755)
	_ = os.WriteFile(filepath.Join(bin, "pi-acp"), []byte("#!/bin/sh\n"), 0o755)
	t.Setenv("PATH", "/nonexistent")
	c := Catalog{Dir: dir}
	for _, s := range c.List() {
		switch s.ID {
		case "pi":
			if !s.Installed || s.Missing != "pi" {
				t.Fatalf("pi: %+v", s)
			}
		case "claude":
			if s.Installed || s.Package == "" {
				t.Fatalf("claude: %+v", s)
			}
		}
	}
	if argv, err := c.Command("pi"); err != nil || argv[0] != filepath.Join(bin, "pi-acp") {
		t.Fatalf("command: %v %v", argv, err)
	}
	if _, err := c.Command("claude"); err == nil {
		t.Fatal("claude is not installed")
	}
}

func TestLinesAcrossChunks(t *testing.T) {
	var got []string
	var offsets []int64
	l := &lines{offset: 10}
	emit := func(b []byte, next int64) { got, offsets = append(got, string(b)), append(offsets, next) }
	l.feed(10, []byte("ab"), false, emit)
	l.feed(12, []byte("c\nde\nf"), false, emit)
	l.feed(18, []byte("g\n"), false, emit)
	if strings.Join(got, "|") != "abc|de|fg" || offsets[0] != 14 || offsets[1] != 17 || offsets[2] != 20 {
		t.Fatalf("%v %v", got, offsets)
	}
	// A gap drops the line cut there.
	l.feed(30, []byte("x"), true, emit)
	l.feed(31, []byte("y\n"), false, emit)
	if got[len(got)-1] != "xy" || offsets[len(offsets)-1] != 33 {
		t.Fatalf("%v %v", got, offsets)
	}
}
