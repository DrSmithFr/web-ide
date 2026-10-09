package console

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/DrSmithFr/web-ide/pod/internal/execx"
	"github.com/DrSmithFr/web-ide/pod/internal/keeper"
)

type events struct {
	mu   sync.Mutex
	out  map[string]*strings.Builder
	next map[string]int64
	t    *testing.T
}

func newEvents(t *testing.T) (*events, Events) {
	e := &events{out: map[string]*strings.Builder{}, next: map[string]int64{}, t: t}
	return e, Events{
		Output: func(id string, data []byte, offset int64) {
			e.mu.Lock()
			defer e.mu.Unlock()
			if e.out[id] == nil {
				e.out[id] = &strings.Builder{}
			} else if offset != e.next[id] {
				t.Errorf("console %s: output at %d, expected %d", id, offset, e.next[id])
			}
			e.out[id].Write(data)
			e.next[id] = offset + int64(len(data))
		},
		Exit: func(string, int) {},
	}
}

func (e *events) text(id string) string {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.out[id] == nil {
		return ""
	}
	return e.out[id].String()
}

func waitFor(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatal("timeout: " + what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// ticks checks that text holds "tick n" lines from first, consecutive; it returns the last.
func ticks(t *testing.T, text string, first int) int {
	t.Helper()
	n := first
	for _, l := range strings.Split(strings.ReplaceAll(text, "\r", ""), "\n") {
		if !strings.HasPrefix(l, "tick ") {
			continue
		}
		if v, _ := strconv.Atoi(strings.TrimPrefix(l, "tick ")); v != n {
			t.Fatalf("tick %d after %d: %q", v, n-1, text)
		}
		n++
	}
	return n - 1
}

// A console of the keeper survives the restart of the pod: the new manager adopts it with its
// title and scrollback, and its output goes on without gap or duplicate.
func TestKeeperConsole(t *testing.T) {
	dir, _ := os.MkdirTemp("", "kc")
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "k.sock")
	ln, err := keeper.Listen(path)
	if err != nil {
		t.Fatal(err)
	}
	ks := keeper.NewServer("test")
	go ks.Serve(ln)
	defer ks.Close()
	c1, _, err := keeper.Dial(path)
	if err != nil {
		t.Fatal(err)
	}

	e1, ev1 := newEvents(t)
	m1 := NewManager(execx.Local{}, dir, ev1)
	m1.UseKeeper(c1, "p1")
	info, err := m1.Create("task", "Counter", []string{"sh", "-c", "i=0; while :; do echo tick $i; i=$((i+1)); sleep 0.02; done"}, "", 80, 20)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m1.Create("terminal", "", nil, "", 80, 20); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "ticks", func() bool { return strings.Contains(e1.text(info.ID), "tick 5\r\n") })
	if err := m1.Rename(info.ID, "Renamed"); err != nil {
		t.Fatal(err)
	}

	// The pod stops: its consoles stay in the keeper.
	m1.Release()
	c1.Close()
	time.Sleep(100 * time.Millisecond)

	c2, _, err := keeper.Dial(path)
	if err != nil {
		t.Fatal(err)
	}
	defer c2.Close()
	e2, ev2 := newEvents(t)
	m2 := NewManager(execx.Local{}, dir, ev2)
	m2.UseKeeper(c2, "p1")
	list := m2.List()
	if len(list) != 2 || list[0].ID != info.ID || list[0].Title != "Renamed" || list[0].Kind != "task" || list[1].Kind != "terminal" {
		t.Fatalf("adopted: %+v", list)
	}
	_, scroll, end, _ := m2.Snapshot(info.ID)
	last := ticks(t, string(scroll), 0)
	if last < 5 {
		t.Fatalf("scrollback: %q", scroll)
	}
	waitFor(t, "ticks after the restart", func() bool { return strings.Contains(e2.text(info.ID), "tick "+strconv.Itoa(last+5)) })
	e2.mu.Lock()
	first := e2.next[info.ID] - int64(e2.out[info.ID].Len())
	e2.mu.Unlock()
	_, all, _, _ := m2.Snapshot(info.ID)
	ticks(t, string(all), 0)
	if first > end {
		t.Fatalf("output from %d after a scrollback ending at %d", first, end)
	}

	// Input goes to the adopted terminal; closing a console ends its process.
	term := list[1].ID
	if err := m2.Input(term, []byte("echo back$((1+1))\n")); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "echo", func() bool { return strings.Contains(e2.text(term), "back2") })
	m2.CloseAll()
	waitFor(t, "processes gone", func() bool { p, _ := c2.List("p1"); return len(p) == 0 })
}
