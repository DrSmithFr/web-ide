package server

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/DrSmithFr/web-ide/pod/plugins/install"
)

// The external plugins of the lock join the registry when the pod starts; one whose folder is
// gone is left out.
func TestExternalsAtStart(t *testing.T) {
	s, ts := newServerWith(t, func(s *Server) {
		manifest := `{"id": "greet", "name": "Greet", "version": "1.0.0", "apiVersion": 1, "permissions": ["files:read", "net"], "assets": "https://x/{version}/{arch}.tar.gz", "contributes": {}}`
		dir := s.Store.Path("plugins", "greet", "1.0.0")
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		os.WriteFile(filepath.Join(dir, "plugin.json"), []byte(manifest), 0o600)
		os.MkdirAll(filepath.Join(dir, "web"), 0o700)
		os.MkdirAll(filepath.Join(dir, "i18n"), 0o700)
		os.WriteFile(filepath.Join(dir, "web", "index.js"), []byte("ide.register({})"), 0o600)
		os.WriteFile(filepath.Join(dir, "i18n", "fr.json"), []byte(`{"Hello": "Bonjour"}`), 0o600)
		_ = install.WriteLock(s.Store, install.Lock{Plugins: map[string]install.Locked{
			"greet": {URL: "https://example.test/greet", Version: "1.0.0", SHA256: "x", Grants: []string{}},
			"gone":  {URL: "https://example.test/gone", Version: "1.0.0"},
		}})
	})
	e, ok := s.Plugins.External("greet")
	if !ok || e.Version != "1.0.0" || e.URL != "https://example.test/greet" || e.Manifest.Name != "Greet" {
		t.Fatalf("greet: %+v %v", e, ok)
	}
	if _, ok := s.Plugins.External("gone"); ok {
		t.Fatal("a plugin without its folder is left out")
	}

	// Its page module and its catalogs, for the sandboxed frames of the page.
	b, err := s.pluginBundle("greet")
	m, _ := b.(map[string]any)
	if err != nil || m["js"] != "ide.register({})" || m["catalogs"].(map[string]map[string]string)["fr"]["Hello"] != "Bonjour" {
		t.Fatalf("bundle: %v %v", b, err)
	}
	if _, err := s.pluginBundle("gone"); err == nil {
		t.Fatal("not installed")
	}

	// Its grants, changed in Settings: a permission it does not ask is refused; the lock follows.
	a, _ := dial(t, ts, s.Token)
	if r := a.callRaw("plugins.grants", map[string]any{"id": "greet", "grants": []string{"exec"}}); r["error"] == nil {
		t.Fatal("a permission it does not ask")
	}
	a.call("plugins.grants", map[string]any{"id": "greet", "grants": []string{"net"}})
	lock, _ := install.ReadLock(s.Store)
	if e, _ := s.Plugins.External("greet"); len(e.Grants) != 1 || e.Grants[0] != "net" || lock.Plugins["greet"].Grants[0] != "net" {
		t.Fatalf("grants: %v %v", e.Grants, lock.Plugins["greet"].Grants)
	}
	if h := a.call("plugins.host", nil)["result"].(map[string]any); h["landlock"] == nil {
		t.Fatal("plugins.host")
	}
}
