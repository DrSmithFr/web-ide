package server

import (
	"net"
	"strings"
	"testing"
	"time"
)

// The tools of the IDE in the MCP (#68): consoles, Docker, previews, and the tools a window
// runs (answered here by the test client as the page would).
func TestMCPIDETools(t *testing.T) {
	_, ts := newServer(t)
	a, _ := dial(t, ts, "secret-token-0123456789abcdef0123")
	dir := t.TempDir()
	id := a.call("projects.create", map[string]any{"type": "local", "path": dir})["result"].(map[string]any)["id"].(string)
	a.call("project.open", map[string]any{"id": id})
	m := &mcpClient{t: t, url: ts.URL, token: "secret-token-0123456789abcdef0123"}
	cwd := map[string]any{"cwd": dir}
	with := func(kv ...any) map[string]any {
		out := map[string]any{"cwd": dir}
		for i := 0; i < len(kv); i += 2 {
			out[kv[i].(string)] = kv[i+1]
		}
		return out
	}

	// Consoles: list, type, read.
	c := a.call("console.create", map[string]any{"kind": "task", "title": "Echo", "command": []string{"sh", "-c", "cat"}})["result"].(map[string]any)
	cid := c["id"].(string)
	if out := m.ok("list_consoles", cwd); !strings.Contains(out, cid+" · command · Echo · running") {
		t.Fatalf("list_consoles: %s", out)
	}
	if out := m.ok("console_input", with("console_id", cid, "text", "hello-console")); !strings.Contains(out, "hello-console") {
		t.Fatalf("console_input: %s", out)
	}
	if out := m.ok("read_console", with("console_id", cid)); !strings.Contains(out, `Console "Echo", running`) || !strings.Contains(out, "hello-console") {
		t.Fatalf("read_console: %s", out)
	}
	if out, failed := m.tool("read_console", with("console_id", "nope")); !failed {
		t.Fatalf("unknown console: %s", out)
	}

	// Docker: a wrong call is an error for the model.
	if out, failed := m.tool("docker_logs", cwd); !failed || !strings.Contains(out, "service or container is missing") {
		t.Fatalf("docker_logs: %s", out)
	}

	// share_preview: the saved serve command, offered in the window; nothing runs.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	port := ln.Addr().(*net.TCPAddr).Port
	a.call("commands.save", map[string]any{"commands": []map[string]any{{"name": "Dev", "command": "sleep 30", "flag": "serve", "port": port}}})
	if out := m.ok("share_preview", with("title", "Home page")); !strings.Contains(out, `"Home page"`) || !strings.Contains(out, "Do not run the command yourself") {
		t.Fatalf("share_preview: %s", out)
	}
	ev := a.waitEvent("assistant.ui", func(d map[string]any) bool { return d["tool"] == "share_preview" })
	spec := ev["args"].(map[string]any)["spec"].(map[string]any)
	if spec["title"] != "Home page" || spec["command"] != "sleep 30" || int(spec["port"].(float64)) != port || spec["project"] != id || ev["chat"] != "" {
		t.Fatalf("share_preview event: %+v", ev)
	}
	if out := m.ok("list_previews", cwd); out != "No preview runs for this project." {
		t.Fatalf("list_previews: %s", out)
	}

	// A tool run by the window: the page answers with assistant.ui.result.
	answer := func(tool string, res map[string]any) map[string]any {
		ev := a.waitEvent("assistant.ui", func(d map[string]any) bool { return d["tool"] == tool })
		a.call("assistant.ui.result", map[string]any{"id": ev["id"], "content": res["content"], "status": res["status"], "image": res["image"]})
		return ev
	}
	done := make(chan string, 1)
	go func() { done <- m.ok("open_file", with("path", "a.go", "line", 3)) }()
	ev = answer("open_file", map[string]any{"content": "a.go opened in the editor, line 3.", "status": "ok"})
	if args := ev["args"].(map[string]any); args["path"] != "a.go" || args["line"].(float64) != 3 || ev["project"] != id {
		t.Fatalf("open_file event: %+v", ev)
	}
	if out := <-done; out != "a.go opened in the editor, line 3." {
		t.Fatalf("open_file: %s", out)
	}

	// request_doodle: the drawing comes back as an image with its description.
	type rich struct{ r map[string]any }
	got := make(chan rich, 1)
	go func() {
		got <- rich{m.rpc("tools/call", map[string]any{"name": "request_doodle", "arguments": with("title", "Layout")})}
	}()
	answer("request_doodle", map[string]any{"content": "Doodle \"Layout\" drawn by the user.", "status": "ok", "image": "data:image/png;base64,iVBORw0K"})
	r := (<-got).r
	content := r["content"].([]any)
	if len(content) != 2 || content[0].(map[string]any)["text"] != "Doodle \"Layout\" drawn by the user." ||
		content[1].(map[string]any)["type"] != "image" || content[1].(map[string]any)["data"] != "iVBORw0K" || content[1].(map[string]any)["mimeType"] != "image/png" {
		t.Fatalf("request_doodle: %+v", r)
	}
	go func() {
		got <- rich{m.rpc("tools/call", map[string]any{"name": "request_doodle", "arguments": with("title", "Layout")})}
	}()
	answer("request_doodle", map[string]any{"content": "closed", "status": "cancelled"})
	if text := (<-got).r["content"].([]any)[0].(map[string]any)["text"]; text != "The user closed the doodle without sending it." {
		t.Fatalf("doodle closed: %v", text)
	}

	// Without a window of the project, the tools of the window fail.
	other := t.TempDir()
	a.call("projects.create", map[string]any{"type": "local", "path": other})
	start := time.Now()
	if out, failed := m.tool("show_message", map[string]any{"cwd": other, "text": "hi"}); !failed || !strings.Contains(out, "no window of the IDE is open") || time.Since(start) > 2*time.Second {
		t.Fatalf("no window: %s", out)
	}
}
