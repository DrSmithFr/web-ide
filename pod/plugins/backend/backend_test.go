package backend

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

// The test binary is the backend too (BACKEND_FAKE): it answers call hello, asks the core,
// emits, hangs on slow, dies on die.
func TestMain(m *testing.M) {
	if os.Getenv("BACKEND_FAKE") != "" {
		fake()
		return
	}
	os.Exit(m.Run())
}

func fake() {
	in := bufio.NewScanner(os.Stdin)
	out := json.NewEncoder(os.Stdout)
	var mu sync.Mutex
	write := func(v any) {
		mu.Lock()
		defer mu.Unlock()
		out.Encode(v)
	}
	replies := map[string]chan json.RawMessage{}
	for in.Scan() {
		var m struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
			Result json.RawMessage `json:"result"`
			Error  *struct{ Message string }
		}
		json.Unmarshal(in.Bytes(), &m)
		if m.Method == "" { // an answer of the pod to a core call
			if ch := replies[string(m.ID)]; ch != nil {
				if m.Error != nil {
					ch <- json.RawMessage(`"error: ` + m.Error.Message + `"`)
				} else {
					ch <- m.Result
				}
			}
			continue
		}
		var c struct{ Method string }
		json.Unmarshal(m.Params, &c)
		switch {
		case m.Method == "initialize" || m.Method == "shutdown":
			write(map[string]any{"jsonrpc": "2.0", "id": m.ID, "result": map[string]any{}})
			if m.Method == "shutdown" {
				return
			}
		case c.Method == "greet.hello":
			write(map[string]any{"jsonrpc": "2.0", "method": "emit", "params": map[string]any{"event": "greet.said", "data": "hi"}})
			write(map[string]any{"jsonrpc": "2.0", "id": m.ID, "result": "hello"})
		case c.Method == "greet.core":
			ch := make(chan json.RawMessage, 1)
			replies[`"c1"`] = ch
			write(map[string]any{"jsonrpc": "2.0", "id": "c1", "method": "core", "params": map[string]any{"method": "fs.list", "params": map[string]any{}}})
			id := m.ID
			go func() { write(map[string]any{"jsonrpc": "2.0", "id": id, "result": <-ch}) }()
		case c.Method == "greet.slow":
		case c.Method == "greet.die":
			os.Exit(3)
		default:
			write(map[string]any{"jsonrpc": "2.0", "id": m.ID, "error": map[string]any{"code": -32601, "message": "no " + c.Method}})
		}
	}
}

func start(t *testing.T, core func(context.Context, string, json.RawMessage) (any, error), emit func(string, json.RawMessage)) *Process {
	t.Helper()
	exe, _ := os.Executable()
	p, err := Start(context.Background(), Config{Argv: []string{exe}, Env: []string{"BACKEND_FAKE=1"}, Log: io.Discard, Init: map[string]any{"plugin": "greet"}, Core: core, Emit: emit})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.Stop)
	return p
}

func TestBackend(t *testing.T) {
	events := make(chan string, 4)
	p := start(t, func(ctx context.Context, method string, params json.RawMessage) (any, error) {
		if method == "fs.list" {
			return nil, errors.New("fs.list is not granted to Greet")
		}
		return "?", nil
	}, func(event string, data json.RawMessage) { events <- event + " " + string(data) })
	ctx := context.Background()

	res, err := p.Call(ctx, "call", map[string]any{"method": "greet.hello"})
	if err != nil || string(res) != `"hello"` {
		t.Fatalf("call: %s %v", res, err)
	}
	if e := <-events; e != `greet.said "hi"` {
		t.Fatalf("emit: %s", e)
	}
	// The backend asks the core: the pod's answer (here a refusal) reaches it.
	res, err = p.Call(ctx, "call", map[string]any{"method": "greet.core"})
	if err != nil || !strings.Contains(string(res), "not granted") {
		t.Fatalf("core: %s %v", res, err)
	}
	if _, err := p.Call(ctx, "call", map[string]any{"method": "greet.nope"}); err == nil || err.Error() != "no greet.nope" {
		t.Fatalf("an error of the backend: %v", err)
	}
	// A call it does not answer times out; the process lives on.
	short, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
	defer cancel()
	if _, err := p.Call(short, "call", map[string]any{"method": "greet.slow"}); err == nil {
		t.Fatal("timeout")
	}
	if _, err := p.Call(ctx, "call", map[string]any{"method": "greet.hello"}); err != nil {
		t.Fatal("still alive", err)
	}
	// It dies: its pending calls fail, the next ones too.
	if _, err := p.Call(ctx, "call", map[string]any{"method": "greet.die"}); err == nil || !strings.Contains(err.Error(), "ended") {
		t.Fatalf("died: %v", err)
	}
	<-p.Done()
	if _, err := p.Call(ctx, "call", map[string]any{"method": "greet.hello"}); err == nil {
		t.Fatal("a dead backend answers nothing")
	}
}

func TestStop(t *testing.T) {
	p := start(t, nil, nil)
	p.Stop()
	select {
	case <-p.Done():
	case <-time.After(3 * time.Second):
		t.Fatal("stopped")
	}
}
