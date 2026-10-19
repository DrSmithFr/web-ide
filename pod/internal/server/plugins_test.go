package server

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DrSmithFr/web-ide/pod/internal/plugins"
	"github.com/DrSmithFr/web-ide/pod/internal/plugins/builtin"
	"github.com/DrSmithFr/web-ide/pod/internal/sshx"
)

// The test plugin hello: its RPC method, its assistant tool (assistant and MCP) and its
// enabling per project in .ide/plugins.json.
func TestPlugins(t *testing.T) {
	s, ts := newServerWith(t, func(s *Server) { s.Plugins = builtin.Registry(true) })
	a, _ := dial(t, ts, s.Token)
	dir := t.TempDir()
	id := a.call("projects.create", map[string]any{"type": "local", "path": dir})["result"].(map[string]any)["id"].(string)
	opened := a.call("project.open", map[string]any{"id": id})["result"].(map[string]any)
	if list := opened["plugins"].([]any); len(list) != 4 || list[3].(map[string]any)["enabled"] != true {
		t.Fatalf("plugins of project.open: %v", opened["plugins"])
	}
	name := filepath.Base(dir)
	if r := a.call("hello.greet", map[string]any{"name": "x"})["result"].(map[string]any); r["text"] != "Hello x from "+name {
		t.Fatalf("hello.greet: %v", r)
	}
	rt, _ := s.openRuntime(id, sshx.Creds{})
	if defs := s.pluginDefs(rt); len(defs) != 3 || !strings.Contains(string(defs[2]), `"hello_greet"`) {
		t.Fatalf("assistant tools: %s", defs)
	}
	m := &mcpClient{t: t, url: ts.URL, token: s.Token}
	if tools := m.rpc("tools/list", nil)["tools"].([]any); len(tools) != len(mcpTools)+3 {
		t.Fatalf("tools/list: %d tools", len(tools))
	}
	if text := m.ok("hello_greet", map[string]any{"cwd": dir, "name": "y"}); text != "Hello y from "+name {
		t.Fatalf("hello_greet: %s", text)
	}

	// Disabled: saved, told to the windows, refused everywhere.
	a.call("plugins.set", map[string]any{"id": "hello", "enabled": false})
	a.waitEvent("plugins.changed", nil)
	data, _ := os.ReadFile(filepath.Join(dir, ".ide", "plugins.json"))
	var f struct{ Disabled []string }
	if json.Unmarshal(data, &f); len(f.Disabled) != 1 || f.Disabled[0] != "hello" {
		t.Fatalf("plugins.json: %s", data)
	}
	if e := a.callRawIn("", "hello.greet", nil)["error"]; e == nil || !strings.Contains(e.(map[string]any)["message"].(string), "disabled") {
		t.Fatalf("hello.greet disabled: %v", e)
	}
	if defs := s.pluginDefs(rt); len(defs) != 2 {
		t.Fatalf("assistant tools of a disabled plugin: %s", defs)
	}
	if text, failed := m.tool("hello_greet", map[string]any{"cwd": dir}); !failed || !strings.Contains(text, "disabled") {
		t.Fatalf("MCP of a disabled plugin: %s", text)
	}
	if e := a.callRawIn("", "plugins.set", map[string]any{"id": "nope", "enabled": false})["error"]; e == nil {
		t.Fatal("unknown plugin accepted")
	}

	// Exec off: no terminal, so Docker is off too; the consoles and their tools are refused.
	if r := a.call("console.list", nil); r["error"] != nil {
		t.Fatalf("console.list: %v", r)
	}
	states := a.call("plugins.set", map[string]any{"id": "exec", "enabled": false})["result"].(map[string]any)["plugins"].([]any)
	for _, st := range states {
		m := st.(map[string]any)
		if id := m["manifest"].(map[string]any)["id"]; id == "docker" && (m["enabled"] != false || m["missing"].([]any)[0] != "terminal") {
			t.Fatalf("docker without terminal: %v", m)
		}
	}
	if e := a.callRawIn("", "console.list", nil)["error"]; e == nil || !strings.Contains(e.(map[string]any)["message"].(string), "disabled") {
		t.Fatalf("console.list with Exec off: %v", e)
	}
	if text, failed := m.tool("list_consoles", map[string]any{"cwd": dir}); !failed || !strings.Contains(text, "terminal") {
		t.Fatalf("list_consoles with Exec off: %s", text)
	}
	if defs := s.offered(rt, []json.RawMessage{json.RawMessage(`{"type":"function","function":{"name":"read_console"}}`), json.RawMessage(`{"type":"function","function":{"name":"bash"}}`)}); len(defs) != 1 {
		t.Fatalf("offered with Exec off: %s", defs)
	}
}

// A coded error of a plugin reaches the page with its code and data (db_password asks the password).
func TestPluginCodedError(t *testing.T) {
	e := toRPCError(&plugins.CodedError{Code: "db_password", Data: map[string]string{"kind": "dbpassword"}, Err: errors.New("password needed")}, "en")
	if e.Code != "db_password" || e.Message != "password needed" || e.Data.(map[string]string)["kind"] != "dbpassword" {
		t.Fatalf("%+v", e)
	}
}
