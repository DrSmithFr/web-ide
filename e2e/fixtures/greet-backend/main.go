// The backend of the fixture plugin greet of the externals suite (#130): JSON-RPC over stdio.
// It answers greet.hello (and emits greet.said), tries what it must not reach (greet.probe),
// asks the core for fs.list (greet.core) and runs its assistant tool greet_hello.
package main

import (
	"bufio"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type msg struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

var (
	mu      sync.Mutex
	out     = json.NewEncoder(os.Stdout)
	waiting = map[string]chan msg{}
	root    string
)

func write(v any) {
	mu.Lock()
	defer mu.Unlock()
	out.Encode(v)
}

func reply(id json.RawMessage, result any) {
	write(map[string]any{"jsonrpc": "2.0", "id": id, "result": result})
}

func fail(id json.RawMessage, text string) {
	write(map[string]any{"jsonrpc": "2.0", "id": id, "error": map[string]any{"code": -32000, "message": text}})
}

// core asks the pod a method of the core.
func core(method string, params any) (json.RawMessage, string) {
	ch := make(chan msg, 1)
	mu.Lock()
	id := "c" + time.Now().Format("150405.000000000")
	waiting[`"`+id+`"`] = ch
	mu.Unlock()
	write(map[string]any{"jsonrpc": "2.0", "id": id, "method": "core", "params": map[string]any{"method": method, "params": params}})
	m := <-ch
	if m.Error != nil {
		return nil, m.Error.Message
	}
	return m.Result, ""
}

func try(err error) string {
	if err == nil {
		return "ok"
	}
	return "blocked"
}

func main() {
	in := bufio.NewScanner(os.Stdin)
	in.Buffer(make([]byte, 1<<20), 8<<20)
	for in.Scan() {
		var m msg
		if json.Unmarshal(in.Bytes(), &m) != nil {
			continue
		}
		if m.Method == "" {
			mu.Lock()
			ch := waiting[string(m.ID)]
			delete(waiting, string(m.ID))
			mu.Unlock()
			if ch != nil {
				ch <- m
			}
			continue
		}
		go handle(m)
	}
}

func handle(m msg) {
	switch m.Method {
	case "initialize":
		var p struct {
			Project struct{ Root string }
		}
		json.Unmarshal(m.Params, &p)
		root = p.Project.Root
		reply(m.ID, map[string]any{})
	case "shutdown":
		reply(m.ID, map[string]any{})
		os.Exit(0)
	case "call":
		var c struct {
			Method string
			Params struct {
				Key string `json:"key"`
			}
		}
		json.Unmarshal(m.Params, &c)
		switch c.Method {
		case "greet.hello":
			write(map[string]any{"jsonrpc": "2.0", "method": "emit", "params": map[string]any{"event": "greet.said", "data": "said by the backend"}})
			reply(m.ID, "Hello from the backend")
		case "greet.probe":
			// What a confined backend must not reach: the pod's key, the network; what it may:
			// the project (files:read).
			_, keyErr := os.ReadFile(c.Params.Key)
			_, projErr := os.ReadFile(filepath.Join(root, "notes.txt"))
			conn, netErr := net.DialTimeout("tcp", "1.1.1.1:443", 2*time.Second)
			if conn != nil {
				conn.Close()
			}
			reply(m.ID, map[string]string{"key": try(keyErr), "project": try(projErr), "net": try(netErr)})
		case "greet.core":
			if _, e := core("fs.list", map[string]any{"path": ""}); e != "" {
				reply(m.ID, "refused: "+e)
			} else {
				reply(m.ID, "answered")
			}
		default:
			fail(m.ID, "no "+c.Method)
		}
	case "tool":
		reply(m.ID, map[string]any{"content": "Hello from the tool of the backend", "summary": "greeted"})
	default:
		fail(m.ID, "unknown method: "+m.Method)
	}
}
