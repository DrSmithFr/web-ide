package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net"

	"strings"
	"testing"

	"github.com/DrSmithFr/web-ide/pod/internal/assistant"
	"github.com/DrSmithFr/web-ide/pod/internal/sshx"
)

// The saved commands seen by the models (list_commands, run_saved_command, share_preview,
// the MCP endpoint), by the previews, and the setup command of the kanban moved into them.
func TestSavedCommandsForModels(t *testing.T) {
	s, ts := newServer(t)
	a, _ := dial(t, ts, "secret-token-0123456789abcdef0123")
	dir := t.TempDir()
	id := a.call("projects.create", map[string]any{"type": "local", "path": dir})["result"].(map[string]any)["id"].(string)
	a.call("project.open", map[string]any{"id": id})

	// The setup command of the kanban settings (an older project) becomes a setup command, once,
	// when the project opens.
	a.call("kanban.meta.set", map[string]any{"values": map[string]string{"setup": "npm ci"}})
	a.call("project.open", map[string]any{"id": id})
	l := a.call("commands.list", nil)["result"].(map[string]any)["commands"].([]any)
	if len(l) != 1 || l[0].(map[string]any)["flag"] != "setup" || l[0].(map[string]any)["command"] != "npm ci" {
		t.Fatalf("migrated: %+v", l)
	}
	if m := a.call("kanban.list", nil)["result"].(map[string]any)["meta"].(map[string]any); m["setup"] != "" {
		t.Fatalf("meta: %+v", m)
	}
	if l := a.call("commands.list", nil)["result"].(map[string]any)["commands"].([]any); len(l) != 1 {
		t.Fatalf("migrated twice: %+v", l)
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	port := ln.Addr().(*net.TCPAddr).Port
	saved := a.call("commands.save", map[string]any{"commands": []map[string]any{
		l[0].(map[string]any),
		{"name": "Dev", "command": "echo serving\nsleep 30", "flag": "serve", "port": port, "description": "the app"},
		{"name": "Tests", "command": "echo tests-ok", "flag": "test"},
	}})["result"].([]any)
	dev, tests := saved[1].(map[string]any)["id"].(string), saved[2].(map[string]any)["id"].(string)

	rt, err := s.openRuntime(id, sshx.Creds{})
	if err != nil {
		t.Fatal(err)
	}
	args := func(js string) assistant.Args {
		var a assistant.Args
		if err := json.Unmarshal([]byte(js), &a); err != nil {
			t.Fatal(err)
		}
		return a
	}
	list, _, err := s.commandsText(rt)
	if err != nil || !strings.Contains(list, dev+" · serve · Dev — the app · port") || !strings.Contains(list, "    sleep 30") || !strings.Contains(list, "not running") {
		t.Fatalf("list_commands: %v\n%s", err, list)
	}

	// run_saved_command: the output of a command that ends; a running one is not started again.
	out, err := s.mcpRunSaved(context.Background(), rt, tests, false, 0)
	if err != nil || !strings.Contains(out, "Exit code: 0") || !strings.Contains(out, "tests-ok") {
		t.Fatalf("run tests: %v %s", err, out)
	}
	out, _ = s.mcpRunSaved(context.Background(), rt, dev, false, 1)
	if !strings.Contains(out, "still running") || !strings.Contains(out, "serving") {
		t.Fatalf("run dev: %s", out)
	}
	first := rt.Consoles.OfCommand(dev)[0].ID
	out, _ = s.mcpRunSaved(context.Background(), rt, dev, false, 0)
	if !strings.Contains(out, "already running in console "+first) || !strings.Contains(out, "restart=true") {
		t.Fatalf("running: %s", out)
	}
	if _, err := s.mcpRunSaved(context.Background(), rt, "nope", false, 0); err == nil {
		t.Fatal("unknown command run")
	}
	out, _ = s.mcpRunSaved(context.Background(), rt, dev, true, 1)
	if cs := rt.Consoles.OfCommand(dev); len(cs) != 1 || cs[0].ID == first || !strings.Contains(out, "still running") {
		t.Fatalf("restart: %+v %s", cs, out)
	}
	if list, _, _ := s.commandsText(rt); !strings.Contains(list, "running (console ") {
		t.Fatalf("state: %s", list)
	}

	// share_preview: the only serve command with a port when no command is given; the
	// saved command of the same text; an unknown id refused.
	res, err := s.sharePreviewIn(id, rt, args(`{"title":"Try it"}`))
	if err != nil || res.Preview.CommandID != dev || res.Preview.Port != port || res.Preview.Command != "echo serving\nsleep 30" {
		t.Fatalf("share_preview: %v %+v", err, res.Preview)
	}
	res, _ = s.sharePreviewIn(id, rt, args(`{"title":"x","command":"echo serving\nsleep 30"}`))
	if res.Preview == nil || res.Preview.CommandID != dev {
		t.Fatalf("same text: %+v", res.Preview)
	}
	res, _ = s.sharePreviewIn(id, rt, args(`{"title":"x","command":"npm run dev","port":3000}`))
	if res.Preview == nil || res.Preview.CommandID != "" {
		t.Fatalf("other command: %+v", res.Preview)
	}
	if _, err := s.sharePreviewIn(id, rt, args(`{"title":"x","command_id":"nope"}`)); err == nil {
		t.Fatal("unknown command_id")
	}

	// preview.open from a saved command takes its command, port and name.
	p := a.call("preview.open", map[string]any{"project": id, "commandId": dev})["result"].(map[string]any)
	if p["command"] != "echo serving\nsleep 30" || p["port"] != float64(port) || p["title"] != "Dev" || p["commandId"] != dev {
		t.Fatalf("preview: %+v", p)
	}
	if e := a.callRaw("preview.open", map[string]any{"project": id, "commandId": tests}); e["error"] == nil || !strings.Contains(fmt.Sprint(e["error"]), "no port") {
		t.Fatalf("no port: %+v", e)
	}

	// The MCP endpoint.
	m := &mcpClient{t: t, url: ts.URL, token: "secret-token-0123456789abcdef0123"}
	if text := m.ok("list_commands", map[string]any{"cwd": dir}); !strings.Contains(text, tests+" · test · Tests") {
		t.Fatalf("mcp list: %s", text)
	}
	if text := m.ok("run_saved_command", map[string]any{"cwd": dir, "id": tests}); !strings.Contains(text, "Exit code: 0") || !strings.Contains(text, "tests-ok") {
		t.Fatalf("mcp run: %s", text)
	}
	if text := m.ok("run_saved_command", map[string]any{"cwd": dir, "id": dev}); !strings.Contains(text, "already running") {
		t.Fatalf("mcp running: %s", text)
	}
}
