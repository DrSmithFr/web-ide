package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DrSmithFr/web-ide/pod/internal/store"
)

func load(t *testing.T, body string) (*Config, *store.Store) {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if body != "" {
		if err := os.WriteFile(st.Path("config.json"), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	cfg, err := Load(st)
	if err != nil {
		t.Fatal(err)
	}
	return cfg, st
}

// The projects folder is the key "projects"; the former key "workspace" is still read, and the
// next save writes "projects" only.
func TestProjectsKey(t *testing.T) {
	home, _ := os.UserHomeDir()
	if cfg, _ := load(t, ""); cfg.Projects != filepath.Join(home, "Apps") {
		t.Fatalf("default: %q", cfg.Projects)
	}
	if cfg, _ := load(t, `{"projects":"/p"}`); cfg.Projects != "/p" {
		t.Fatalf("projects: %q", cfg.Projects)
	}
	cfg, st := load(t, `{"workspace":"/old"}`)
	if cfg.Projects != "/old" {
		t.Fatalf("former key: %q", cfg.Projects)
	}
	if cfg, _ := load(t, `{"workspace":"/old","projects":"/new"}`); cfg.Projects != "/new" {
		t.Fatalf("projects wins: %q", cfg.Projects)
	}
	if err := cfg.Save(st); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(st.Path("config.json"))
	if got := string(raw); !strings.Contains(got, `"projects": "/old"`) && !strings.Contains(got, `"projects":"/old"`) || strings.Contains(got, "workspace") {
		t.Fatalf("saved: %s", got)
	}
}
