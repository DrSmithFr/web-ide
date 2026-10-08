package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/coder/websocket"

	"github.com/DrSmithFr/web-ide/pod/internal/config"
	"github.com/DrSmithFr/web-ide/pod/internal/hfcache"
	"github.com/DrSmithFr/web-ide/pod/internal/llm"
	"github.com/DrSmithFr/web-ide/pod/internal/projects"
	"github.com/DrSmithFr/web-ide/pod/internal/sessions"
	"github.com/DrSmithFr/web-ide/pod/internal/settings"
	"github.com/DrSmithFr/web-ide/pod/internal/sshx"
	"github.com/DrSmithFr/web-ide/pod/internal/store"
)

type wsClient struct {
	t      *testing.T
	c      *websocket.Conn
	nextID int64
	events chan map[string]any
	resps  map[int64]chan map[string]any
}

func newServer(t *testing.T) (*Server, *httptest.Server) {
	st, _ := store.Open(t.TempDir())
	cfg, _ := config.Load(st)
	reg, _ := projects.Load(st)
	sets, _ := settings.Load(st)
	s := &Server{Cfg: cfg, Store: st, Token: "secret-token-0123456789abcdef0123", Projects: reg, Settings: sets,
		Sessions: sessions.New(st), LLM: llm.New(st), Models: hfcache.New(st.Path("models", "hf")), Pool: sshx.NewPool(sshx.NewHostKeys(st.Path("known_hosts"))),
		Static: fstest.MapFS{"index.html": {Data: []byte("<html>app</html>")}}}
	s.Init()
	ts := httptest.NewServer(s)
	t.Cleanup(func() {
		ts.Close()
		s.Shutdown()
	})
	return s, ts
}

func dial(t *testing.T, ts *httptest.Server, token string) (*wsClient, error) {
	h := http.Header{}
	h.Set("Cookie", cookieName+"="+token)
	h.Set("Origin", ts.URL)
	c, _, err := websocket.Dial(context.Background(), strings.Replace(ts.URL, "http", "ws", 1)+"/ws", &websocket.DialOptions{HTTPHeader: h})
	if err != nil {
		return nil, err
	}
	c.SetReadLimit(64 << 20)
	w := &wsClient{t: t, c: c, events: make(chan map[string]any, 100), resps: map[int64]chan map[string]any{}}
	go func() {
		for {
			_, data, err := c.Read(context.Background())
			if err != nil {
				return
			}
			var m map[string]any
			json.Unmarshal(data, &m)
			if _, ok := m["event"]; ok {
				w.events <- m
			} else if ch, ok := w.resps[int64(m["id"].(float64))]; ok {
				ch <- m
			}
		}
	}()
	return w, nil
}

// callRaw returns the response, error included.
func (w *wsClient) callRaw(method string, params any) map[string]any {
	return w.callRawIn("", method, params)
}

// callRawIn runs a request in an attached worktree of the window (its own project when empty).
func (w *wsClient) callRawIn(project, method string, params any) map[string]any {
	w.nextID++
	id := w.nextID
	ch := make(chan map[string]any, 1)
	w.resps[id] = ch
	data, _ := json.Marshal(map[string]any{"id": id, "method": method, "params": params, "project": project})
	if err := w.c.Write(context.Background(), websocket.MessageText, data); err != nil {
		w.t.Fatal(err)
	}
	select {
	case m := <-ch:
		return m
	case <-time.After(5 * time.Second):
		w.t.Fatalf("%s: timeout", method)
	}
	return nil
}

func (w *wsClient) call(method string, params any) map[string]any {
	return w.callIn("", method, params)
}

func (w *wsClient) callIn(project, method string, params any) map[string]any {
	m := w.callRawIn(project, method, params)
	if e, ok := m["error"]; ok {
		w.t.Fatalf("%s: %v", method, e)
	}
	return m
}

func (w *wsClient) waitEvent(name string, match func(map[string]any) bool) map[string]any {
	deadline := time.After(5 * time.Second)
	for {
		select {
		case e := <-w.events:
			if e["event"] == name && (match == nil || match(e["data"].(map[string]any))) {
				return e["data"].(map[string]any)
			}
		case <-deadline:
			w.t.Fatalf("event %s not received", name)
		}
	}
}

func TestPairing(t *testing.T) {
	_, ts := newServer(t)
	if _, err := dial(t, ts, "wrong"); err == nil {
		t.Fatal("websocket accepted without the pairing token")
	}
	res, _ := http.Get(ts.URL + "/project/x")
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("page served without pairing: %d", res.StatusCode)
	}
	jar := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	res, _ = jar.Get(ts.URL + "/?token=secret-token-0123456789abcdef0123")
	if res.StatusCode != http.StatusFound || !strings.Contains(res.Header.Get("Set-Cookie"), cookieName) {
		t.Fatalf("pairing by URL failed: %d", res.StatusCode)
	}
}

func TestProjectFilesAndRemoteChanges(t *testing.T) {
	_, ts := newServer(t)
	a, err := dial(t, ts, "secret-token-0123456789abcdef0123")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := dial(t, ts, "secret-token-0123456789abcdef0123")
	dir := t.TempDir()
	file := filepath.Join(dir, "main.go")
	os.WriteFile(file, []byte("package main\n"), 0o644)

	p := a.call("projects.create", map[string]any{"type": "local", "path": dir})["result"].(map[string]any)
	if p["name"] != filepath.Base(dir) {
		t.Fatalf("derived name = %v", p["name"])
	}
	id := p["id"].(string)
	// Every new project is a git repository (with files: no first commit).
	if _, err := os.Stat(filepath.Join(dir, ".git")); err != nil || p["gitError"] != nil {
		t.Fatalf("git not initialized: %v %v", err, p["gitError"])
	}
	open := a.call("project.open", map[string]any{"id": id})["result"].(map[string]any)
	if open["root"] != dir || open["project"].(map[string]any)["gitSetup"] != nil {
		t.Fatalf("root = %v", open["root"])
	}
	b.call("project.open", map[string]any{"id": id})

	f := a.call("fs.read", map[string]any{"path": file})["result"].(map[string]any)
	if f["content"] != "package main\n" || f["readOnly"] != false {
		t.Fatalf("read = %+v", f)
	}
	b.call("fs.read", map[string]any{"path": file})

	// Unsaved buffer shared with the other window.
	a.call("buffer.sync", map[string]any{"path": file, "content": "package main // wip\n"})
	ev := b.waitEvent("buffer.synced", nil)
	if ev["content"] != "package main // wip\n" {
		t.Fatalf("buffer.synced = %+v", ev)
	}

	// Save from a: b receives the new version.
	a.call("fs.write", map[string]any{"path": file, "content": "package main\n\nfunc main() {}\n"})
	ev = b.waitEvent("fs.changed", func(d map[string]any) bool { return d["saved"] == true })
	if !strings.Contains(ev["content"].(string), "func main") {
		t.Fatalf("fs.changed = %+v", ev)
	}

	// Change by another tool (an AI agent): both windows get it, once.
	os.WriteFile(file, []byte("package main\n\nfunc main() { println(1) }\n"), 0o644)
	ev = a.waitEvent("fs.changed", func(d map[string]any) bool { return d["saved"] == nil })
	if !strings.Contains(ev["content"].(string), "println(1)") {
		t.Fatalf("external change = %+v", ev)
	}

	// A Latin-1 file with CRLF line ends: read as UTF-8 with LF, written back in its format.
	latin := filepath.Join(dir, "latin.txt")
	os.WriteFile(latin, []byte("caf\xe9\r\nfin\r\n"), 0o644)
	l := a.call("fs.read", map[string]any{"path": latin})["result"].(map[string]any)
	if l["content"] != "café\nfin\n" || l["encoding"] != "windows-1252" || l["eol"] != "crlf" || l["binary"] == true {
		t.Fatalf("latin read = %+v", l)
	}
	a.call("fs.write", map[string]any{"path": latin, "content": "café\nfin!\n", "encoding": "windows-1252", "eol": "crlf"})
	if data, _ := os.ReadFile(latin); string(data) != "caf\xe9\r\nfin!\r\n" {
		t.Fatalf("latin written = %q", data)
	}
	a.call("fs.write", map[string]any{"path": latin, "content": "café\n", "encoding": "utf-8-bom", "eol": "lf"})
	if data, _ := os.ReadFile(latin); string(data) != "\xef\xbb\xbfcafé\n" {
		t.Fatalf("converted = %q", data)
	}
	if r := a.callRaw("fs.write", map[string]any{"path": latin, "content": "€ ✓", "encoding": "windows-1252"}); r["error"] == nil {
		t.Fatal("a character windows-1252 cannot encode must be an error")
	}

	// Files outside the project are read-only.
	outside := filepath.Join(t.TempDir(), "lib.d.ts")
	os.WriteFile(outside, []byte("declare const x: number\n"), 0o644)
	o := a.call("fs.read", map[string]any{"path": outside})["result"].(map[string]any)
	if o["readOnly"] != true {
		t.Fatal("file outside the project must be read-only")
	}

	// Session pushed by a reaches b.
	a.call("session.update", map[string]any{"session": map[string]any{"layout": map[string]any{"type": "leaf", "id": "p1", "tabs": []string{}}}})
	b.waitEvent("session.changed", nil)

	// Search.
	r := a.call("search.grep", map[string]any{"query": "println"})["result"].(map[string]any)
	if len(r["matches"].([]any)) != 1 {
		t.Fatalf("search = %+v", r)
	}

	// A folder marked as excluded leaves the search and "go to file"; the mark reaches b.
	os.MkdirAll(filepath.Join(dir, "gen"), 0o755)
	os.WriteFile(filepath.Join(dir, "gen", "out.go"), []byte("println()\n"), 0o644)
	if r := a.call("search.grep", map[string]any{"query": "println"})["result"].(map[string]any); len(r["matches"].([]any)) != 2 {
		t.Fatalf("search before the mark = %+v", r)
	}
	a.call("folders.mark", map[string]any{"path": filepath.Join(dir, "gen"), "mark": "excluded"})
	if ev := b.waitEvent("folders.changed", nil); ev["gen"] != "excluded" {
		t.Fatalf("folders.changed = %+v", ev)
	}
	if data, _ := os.ReadFile(filepath.Join(dir, ".ide", "folders.json")); !strings.Contains(string(data), `"gen": "excluded"`) {
		t.Fatalf("folders.json = %s", data)
	}
	if r := a.call("search.grep", map[string]any{"query": "println"})["result"].(map[string]any); len(r["matches"].([]any)) != 1 {
		t.Fatalf("search in an excluded folder = %+v", r)
	}
	for _, f := range a.call("search.files", nil)["result"].([]any) {
		if strings.Contains(f.(string), "/gen/") {
			t.Fatalf("excluded file listed: %v", f)
		}
	}
	if m := a.call("folders.get", nil)["result"].(map[string]any); m["gen"] != "excluded" {
		t.Fatalf("folders.get = %+v", m)
	}

	// Files left out by .gitignore.
	os.WriteFile(filepath.Join(dir, ".gitignore"), []byte("*.log\n"), 0o644)
	ign := a.call("git.ignored", map[string]any{"paths": []string{filepath.Join(dir, "x.log"), file}})["result"].([]any)
	if len(ign) != 1 || ign[0] != filepath.Join(dir, "x.log") {
		t.Fatalf("ignored = %+v", ign)
	}

	// Terminal: output comes back as an event.
	c := a.call("console.create", map[string]any{"command": []string{"echo", "hello-pod"}, "kind": "task"})["result"].(map[string]any)
	a.waitEvent("console.exit", func(d map[string]any) bool { return d["id"] == c["id"] })
	at := a.call("console.attach", map[string]any{"id": c["id"]})["result"].(map[string]any)
	if at["data"] == "" {
		t.Fatal("empty scrollback")
	}
}

func TestCappedOutput(t *testing.T) {
	c := &capped{}
	big := bytes.Repeat([]byte("x"), execHead+execTail+1000)
	if n, _ := c.Write(big); n != len(big) {
		t.Fatalf("write returned %d", n)
	}
	_, _ = c.Write([]byte("FIN"))
	text, cut := c.String()
	if !cut || !strings.HasSuffix(text, "FIN") || !strings.Contains(text, "output cut") || len(text) > execHead+execTail+100 {
		t.Fatalf("capped: cut=%v len=%d", cut, len(text))
	}
}

func TestExecRun(t *testing.T) {
	_, ts := newServer(t)
	a, err := dial(t, ts, "secret-token-0123456789abcdef0123")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "f.txt"), []byte("contenu"), 0o644)
	id := a.call("projects.create", map[string]any{"type": "local", "path": dir})["result"].(map[string]any)["id"].(string)
	a.call("project.open", map[string]any{"id": id})
	r := a.call("exec.run", map[string]any{"command": "cat f.txt; echo erreur >&2; exit 4"})["result"].(map[string]any)
	if r["output"] != "contenuerreur\n" || r["code"] != float64(4) || r["timedOut"] != false {
		t.Fatalf("exec: %+v", r)
	}
	r = a.call("exec.run", map[string]any{"command": "sleep 5; echo trop tard", "timeout": 1})["result"].(map[string]any)
	if r["timedOut"] != true || strings.Contains(r["output"].(string), "trop tard") {
		t.Fatalf("timeout: %+v", r)
	}
}

// The clipboard history is shared by every window, without an open project.
func TestClipboardHistory(t *testing.T) {
	_, ts := newServer(t)
	a, _ := dial(t, ts, "secret-token-0123456789abcdef0123")
	b, _ := dial(t, ts, "secret-token-0123456789abcdef0123")
	a.call("clipboard.add", map[string]any{"text": "one", "max": 10})
	a.call("clipboard.add", map[string]any{"text": "two", "max": 10})
	for e := range b.events {
		if e["event"] == "clipboard.changed" {
			if len(e["data"].([]any)) != 1 {
				t.Fatalf("event = %v", e)
			}
			break
		}
	}
	l := b.call("clipboard.list", nil)["result"].([]any)
	if len(l) != 2 || l[0].(map[string]any)["text"] != "two" {
		t.Fatalf("list = %v", l)
	}
	a.call("clipboard.remove", map[string]any{"text": "two"})
	if l := a.call("clipboard.list", nil)["result"].([]any); len(l) != 1 {
		t.Fatalf("after remove = %v", l)
	}
}
