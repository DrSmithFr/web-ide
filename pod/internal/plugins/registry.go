package plugins

import (
	"fmt"
	"slices"
)

// CoreCapabilities are provided by the core itself, as long as Console and Git are not plugins.
var CoreCapabilities = []string{"terminal", "worktrees"}

// CoreTools are the ids of the tools of the core, which no plugin may take.
var CoreTools = []string{"explorer", "search", "git", "kanban", "console", "problems", "forwards", "database",
	"assistant", "agents", "questions", "structure", "conflicts", "info"}

// Registry holds the built-in plugins, in their order of registration.
type Registry struct {
	list  []Plugin
	tools map[string]string // assistant tool -> plugin
}

func NewRegistry() *Registry { return &Registry{tools: map[string]string{}} }

// Add checks a plugin against the others and keeps it.
func (r *Registry) Add(p Plugin) error {
	m := p.Manifest()
	if err := m.Check(); err != nil {
		return err
	}
	for _, o := range r.list {
		if o.Manifest().ID == m.ID {
			return fmt.Errorf("plugin %s registered twice", m.ID)
		}
		for _, t := range o.Manifest().Contributes.Tools {
			if slices.ContainsFunc(m.Contributes.Tools, func(x Tool) bool { return x.ID == t.ID }) {
				return fmt.Errorf("plugin %s: tool %s is already the one of %s", m.ID, t.ID, o.Manifest().ID)
			}
		}
	}
	for _, t := range m.Contributes.Tools {
		if slices.Contains(CoreTools, t.ID) {
			return fmt.Errorf("plugin %s: tool %s is a tool of the core", m.ID, t.ID)
		}
	}
	for _, name := range m.Contributes.AssistantTools {
		if o, ok := r.tools[name]; ok {
			return fmt.Errorf("plugin %s: assistant tool %s is already the one of %s", m.ID, name, o)
		}
		r.tools[name] = m.ID
	}
	r.list = append(r.list, p)
	return nil
}

// All returns the plugins in their order of registration.
func (r *Registry) All() []Plugin { return r.list }

// Get returns a plugin by id.
func (r *Registry) Get(id string) (Plugin, bool) {
	for _, p := range r.list {
		if p.Manifest().ID == id {
			return p, true
		}
	}
	return nil, false
}

// OwnerOf is the plugin of an assistant tool ("" for a tool of the core).
func (r *Registry) OwnerOf(tool string) string { return r.tools[tool] }

// State is a plugin as a project sees it.
type State struct {
	Manifest Manifest `json:"manifest"`
	// Enabled: the plugin runs on the project (not disabled, and its requirements met).
	Enabled bool `json:"enabled"`
	// Disabled by the user on the project.
	Disabled bool `json:"disabled"`
	// Missing are the capabilities it requires that nothing enabled provides.
	Missing []string `json:"missing,omitempty"`
}

// Resolve tells which plugins run on a project, given the ones the user disabled: a plugin
// whose requirement is not provided (by the core or an enabled plugin) is off too, which may
// turn off the plugins that required it in turn.
func (r *Registry) Resolve(disabled []string) []State {
	out := make([]State, len(r.list))
	for i, p := range r.list {
		m := p.Manifest()
		off := slices.Contains(disabled, m.ID)
		out[i] = State{Manifest: m, Enabled: !off, Disabled: off}
	}
	for changed := true; changed; {
		changed = false
		provided := map[string]bool{}
		for _, c := range CoreCapabilities {
			provided[c] = true
		}
		for _, s := range out {
			if s.Enabled {
				for _, c := range s.Manifest.Provides {
					provided[c] = true
				}
			}
		}
		for i := range out {
			s := &out[i]
			if !s.Enabled {
				continue
			}
			s.Missing = nil
			for _, c := range s.Manifest.Requires {
				if !provided[c] {
					s.Missing = append(s.Missing, c)
				}
			}
			if len(s.Missing) > 0 {
				s.Enabled = false
				changed = true
			}
		}
	}
	return out
}

// Enabled is the set of the plugins running on a project.
func Enabled(states []State) map[string]bool {
	out := map[string]bool{}
	for _, s := range states {
		if s.Enabled {
			out[s.Manifest.ID] = true
		}
	}
	return out
}

// Dependents are the enabled plugins that require a capability the plugin id provides.
func Dependents(states []State, id string) []string {
	var provides []string
	for _, s := range states {
		if s.Manifest.ID == id {
			provides = s.Manifest.Provides
		}
	}
	var out []string
	for _, s := range states {
		if s.Manifest.ID == id || !s.Enabled {
			continue
		}
		for _, c := range s.Manifest.Requires {
			if slices.Contains(provides, c) && !slices.Contains(CoreCapabilities, c) {
				out = append(out, s.Manifest.ID)
				break
			}
		}
	}
	return out
}
