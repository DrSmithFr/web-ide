package plugins

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"antennae.space/ide/pod/fsx"
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
		"core tool": {ID: "c", Contributes: Contributions{Tools: []Tool{{ID: "questions", Zone: "left"}}}},
		"assistant": {ID: "d", Contributes: Contributions{AssistantTools: []string{"a_x"}}},
		"mcp":       {ID: "e", Contributes: Contributions{MCPTools: []string{"a_x"}}},
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
	ch, err := SetEnabled(fs, dir, "docker", true)
	if err != nil || !slices.Equal(ch.Disabled, []string{"database"}) {
		t.Fatal(ch, err)
	}
	if again, _ := LoadDisabled(fs, dir); !slices.Equal(again, ch.Disabled) {
		t.Fatal(again)
	}
	// An external plugin used by the project: kept beside the disabled ones.
	if ch, err = SetExternal(fs, dir, "greet", "https://example.test/greet"); err != nil || ch.External["greet"] == "" {
		t.Fatal(ch, err)
	}
	if ch, _ = SetEnabled(fs, dir, "git", false); ch.External["greet"] == "" || !slices.Equal(ch.Disabled, []string{"database", "git"}) {
		t.Fatal("the externals are kept", ch)
	}
	if ch, _ = SetExternal(fs, dir, "greet", ""); ch.External != nil {
		t.Fatal(ch)
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
	// The built-in plugins live in plugins/<id>/ at the root of the repository.
	files, _ := filepath.Glob("../../plugins/*/plugin.json")
	if len(files) < 12 {
		t.Fatal("built-in manifests not found:", files)
	}
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := Parse(data); err != nil {
			t.Fatal(f, err)
		}
	}
}

// The external plugins are listed after the built-ins, never enabled in this step; one a
// project uses that the pod lacks shows as not installed.
func TestExternals(t *testing.T) {
	r := NewRegistry()
	if err := r.Add(fake{Manifest{ID: "git", Name: "Git", Version: "1.0.0", APIVersion: APIVersion, Contributes: Contributions{Tools: []Tool{{ID: "git", Label: "Git", Icon: "git", Zone: "left"}}}}}); err != nil {
		t.Fatal(err)
	}
	if err := r.SetExternal(External{Manifest: Manifest{ID: "git", APIVersion: APIVersion}}); err == nil {
		t.Fatal("the id of a built-in")
	}
	if err := r.SetExternal(External{Manifest: Manifest{ID: "x", APIVersion: APIVersion, Contributes: Contributions{Tools: []Tool{{ID: "git", Zone: "left"}}}}}); err == nil {
		t.Fatal("a tool of a built-in")
	}
	if err := r.SetExternal(External{Manifest: Manifest{ID: "greet", Name: "Greet", Version: "1.1.0", APIVersion: APIVersion}, URL: "u", Version: "1.1.0"}); err != nil {
		t.Fatal(err)
	}
	states := r.ResolveChoices(Choices{External: map[string]string{"greet": "u", "other": "v"}})
	if len(states) != 3 || states[1].Manifest.ID != "greet" || !states[1].Enabled || states[1].Disabled || !states[1].External.Installed {
		t.Fatalf("states: %+v", states)
	}
	if states[2].Manifest.ID != "other" || states[2].Enabled || states[2].External.Installed || states[2].External.URL != "v" {
		t.Fatalf("not installed: %+v", states[2])
	}
	if r.ResolveChoices(Choices{})[1].Enabled {
		t.Fatal("not used by the project: off")
	}
	// What an external plugin provides is not given to the others.
	r.RemoveExternal("greet")
	_ = r.SetExternal(External{Manifest: Manifest{ID: "greet", APIVersion: APIVersion, Provides: []string{"tickets"}, Requires: []string{"nope"}}, Version: "1.1.0"})
	states = r.ResolveChoices(Choices{External: map[string]string{"greet": "u"}})
	if Provided(states)["tickets"] || states[1].Enabled || states[1].Missing[0] != "nope" {
		t.Fatalf("capabilities of an external: %+v", states[1])
	}
	if v := r.Versions(); v["greet"] != "1.1.0" || v["git"] != "1.0.0" {
		t.Fatal(v)
	}
	r.RemoveExternal("greet")
	if len(r.Externals()) != 0 || !r.ResolveChoices(Choices{})[0].Enabled {
		t.Fatal("removed")
	}
}
