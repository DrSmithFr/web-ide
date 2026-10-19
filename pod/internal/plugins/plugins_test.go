package plugins

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/DrSmithFr/web-ide/pod/internal/fsx"
)

type fake struct{ m Manifest }

func (f fake) Manifest() Manifest { m := f.m; m.APIVersion = APIVersion; return m }
func (f fake) Register(Host)      {}

func TestCheck(t *testing.T) {
	ok := Manifest{APIVersion: APIVersion, ID: "docker", Contributes: Contributions{Tools: []Tool{{ID: "dock", Zone: "right"}}, AssistantTools: []string{"docker_ps"}}}
	if err := ok.Check(); err != nil {
		t.Fatal(err)
	}
	for name, m := range map[string]Manifest{
		"id":      {ID: "Docker"},
		"api":     {ID: "x", APIVersion: 2},
		"zone":    {ID: "x", Contributes: Contributions{Tools: []Tool{{ID: "x", Zone: "top"}}}},
		"repeat":  {ID: "x", Contributes: Contributions{Tools: []Tool{{ID: "x", Zone: "left"}, {ID: "x", Zone: "left"}}}},
		"tool":    {ID: "x", Contributes: Contributions{AssistantTools: []string{"Bad name"}}},
		"tabview": {ID: "x", Contributes: Contributions{TabViews: []TabView{{ID: "a/b"}}}},
	} {
		if m.Check() == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestAdd(t *testing.T) {
	r := NewRegistry()
	if err := r.Add(fake{Manifest{APIVersion: APIVersion, ID: "a", Contributes: Contributions{Tools: []Tool{{ID: "a", Zone: "left"}}, AssistantTools: []string{"a_x"}}}}); err != nil {
		t.Fatal(err)
	}
	for name, m := range map[string]Manifest{
		"twice":     {ID: "a"},
		"tool":      {ID: "b", Contributes: Contributions{Tools: []Tool{{ID: "a", Zone: "left"}}}},
		"core tool": {ID: "c", Contributes: Contributions{Tools: []Tool{{ID: "kanban", Zone: "left"}}}},
		"assistant": {ID: "d", Contributes: Contributions{AssistantTools: []string{"a_x"}}},
	} {
		if r.Add(fake{m}) == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if r.OwnerOf("a_x") != "a" || r.OwnerOf("bash") != "" || len(r.All()) != 1 {
		t.Fatal("registry", r.All())
	}
}

func TestResolve(t *testing.T) {
	r := NewRegistry()
	for _, m := range []Manifest{
		{ID: "db", Provides: []string{"sql"}},
		{ID: "orm", Requires: []string{"sql"}},
		{ID: "admin", Requires: []string{"sql"}},
		{ID: "lost", Requires: []string{"nothing"}},
	} {
		if err := r.Add(fake{m}); err != nil {
			t.Fatal(err)
		}
	}
	on := Enabled(r.Resolve(nil))
	if !on["db"] || !on["orm"] || !on["admin"] || on["lost"] {
		t.Fatal("all enabled", on)
	}
	states := r.Resolve([]string{"db"})
	on = Enabled(states)
	if len(on) != 0 {
		t.Fatal("db off turns orm and admin off", on)
	}
	if !states[0].Disabled || states[1].Disabled || !slices.Equal(states[1].Missing, []string{"sql"}) {
		t.Fatal("states", states)
	}
	if err := r.Add(fake{Manifest{ID: "core", Required: true}}); err != nil {
		t.Fatal(err)
	}
	if on := Enabled(r.Resolve([]string{"core"})); !on["core"] {
		t.Fatal("a required plugin cannot be disabled")
	}
	if d := Dependents(r.Resolve(nil), "db"); !slices.Equal(d, []string{"orm", "admin"}) {
		t.Fatal("dependents", d)
	}
}

func TestEnabledFile(t *testing.T) {
	dir := filepath.Join(t.TempDir(), ".ide")
	fs := fsx.Local{}
	if list, err := LoadDisabled(fs, dir); err != nil || len(list) != 0 {
		t.Fatal(list, err)
	}
	if _, err := SetEnabled(fs, dir, "docker", false); err != nil {
		t.Fatal(err)
	}
	if _, err := SetEnabled(fs, dir, "database", false); err != nil {
		t.Fatal(err)
	}
	list, err := SetEnabled(fs, dir, "docker", true)
	if err != nil || !slices.Equal(list, []string{"database"}) {
		t.Fatal(list, err)
	}
	if again, _ := LoadDisabled(fs, dir); !slices.Equal(again, list) {
		t.Fatal(again)
	}
	if _, err := os.Stat(filepath.Join(dir, ".gitignore")); err != nil {
		t.Fatal(".ide kept out of git", err)
	}
	_ = os.WriteFile(filepath.Join(dir, "plugins.json"), []byte("{"), 0o644)
	if _, err := LoadDisabled(fs, dir); err == nil {
		t.Fatal("invalid file accepted")
	}
}

func TestBuiltinManifests(t *testing.T) {
	for _, f := range []string{"hello/plugin.json", "docker/plugin.json", "database/plugin.json", "exec/plugin.json", "search/plugin.json", "git/plugin.json", "previews/plugin.json", "languages/plugin.json", "files/plugin.json", "code/plugin.json"} {
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := Parse(data); err != nil {
			t.Fatal(f, err)
		}
	}
}
