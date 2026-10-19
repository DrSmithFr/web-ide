package server

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

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
	if list := opened["plugins"].([]any); len(list) != 2 || list[1].(map[string]any)["enabled"] != true {
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
}
