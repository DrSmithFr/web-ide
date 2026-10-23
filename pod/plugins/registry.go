package plugins

import (
	"fmt"
	"slices"
	"sort"
	"sync"
)

// CoreCapabilities are provided by the core itself: none since Git is a plugin (#118).
var CoreCapabilities = []string{}

// CoreTools are the ids of the tools of the core, which no plugin may take.
var CoreTools = []string{
	"questions", "conflicts", "info"}

// Registry holds the built-in plugins, in their order of registration, and the external plugins
// installed on the pod (pod/plugins/install), by id.
type Registry struct {
	list  []Plugin
	tools map[string]string // assistant tool -> plugin

	mu        sync.RWMutex
	externals map[string]External
}

func NewRegistry() *Registry {
	return &Registry{tools: map[string]string{}, externals: map[string]External{}}
}

// External is an external plugin installed on the pod: its manifest, where it comes from, its
// version and the grants the user accepted. Its views run in a sandboxed frame of the page
// (#129); its backend: #130.
type External struct {
	Manifest Manifest `json:"-"`
	URL      string   `json:"url"`
	Version  string   `json:"version"`
	Grants   []string `json:"grants"`
	// Installed is false for a plugin a project uses that this pod lacks.
	Installed bool `json:"installed"`
}

// Builtin tells whether id is the one of a built-in plugin.
func (r *Registry) Builtin(id string) bool {
	_, ok := r.Get(id)
	return ok
}

// CheckExternal tells whether an external plugin can sit beside the built-ins: its id, its
// tools and its assistant tools are not theirs.
func (r *Registry) CheckExternal(m Manifest) error {
	if r.Builtin(m.ID) {
		return fmt.Errorf("plugin %s: the id of a built-in plugin", m.ID)
	}
	for _, t := range m.Contributes.Tools {
		if slices.Contains(CoreTools, t.ID) {
			return fmt.Errorf("plugin %s: tool %s is a tool of the core", m.ID, t.ID)
		}
		for _, o := range r.list {
			if slices.ContainsFunc(o.Manifest().Contributes.Tools, func(x Tool) bool { return x.ID == t.ID }) {
				return fmt.Errorf("plugin %s: tool %s is already the one of %s", m.ID, t.ID, o.Manifest().ID)
			}
		}
	}
	for _, name := range m.Contributes.toolNames() {
		if o, ok := r.tools[name]; ok {
			return fmt.Errorf("plugin %s: assistant tool %s is already the one of %s", m.ID, name, o)
		}
	}
	return nil
}

// SetExternal adds or replaces an installed external plugin.
func (r *Registry) SetExternal(e External) error {
	if err := r.CheckExternal(e.Manifest); err != nil {
		return err
	}
	e.Installed = true
	r.mu.Lock()
	defer r.mu.Unlock()
	r.externals[e.Manifest.ID] = e
	return nil
}

// RemoveExternal forgets an external plugin removed from the pod.
func (r *Registry) RemoveExternal(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.externals, id)
}

// External is an installed external plugin by id.
func (r *Registry) External(id string) (External, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	e, ok := r.externals[id]
	return e, ok
}

// Externals are the installed external plugins, by id.
func (r *Registry) Externals() []External {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]External, 0, len(r.externals))
	for _, e := range r.externals {
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Manifest.ID < out[j].Manifest.ID })
	return out
}

// Versions are the plugins of the pod and their versions, built-ins and externals (what the
// requirements of an external plugin are checked against).
func (r *Registry) Versions() map[string]string {
	out := map[string]string{}
	for _, p := range r.list {
		out[p.Manifest().ID] = p.Manifest().Version
	}
	for _, e := range r.Externals() {
		out[e.Manifest.ID] = e.Version
	}
	return out
}

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
	for _, name := range m.Contributes.toolNames() {
		if o, ok := r.tools[name]; ok {
			return fmt.Errorf("plugin %s: assistant tool %s is already the one of %s", m.ID, name, o)
		}
		r.tools[name] = m.ID
	}
	r.list = append(r.list, p)
	return nil
}

// All returns the built-in plugins in their order of registration.
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
	// External: an external plugin, enabled when the project uses it (Disabled false) and the
	// capabilities it requires are there.
	External *External `json:"external,omitempty"`
}

// Resolve tells which plugins run on a project, given the ones the user disabled: a plugin
// whose requirement is not provided (by the core or an enabled plugin) is off too, which may
// turn off the plugins that required it in turn.
func (r *Registry) Resolve(disabled []string) []State {
	return r.ResolveChoices(Choices{Disabled: disabled})
}

// ResolveChoices is Resolve with the external plugins: those installed, then those the project
// uses that this pod lacks.
func (r *Registry) ResolveChoices(ch Choices) []State {
	out := r.resolveBuiltins(ch.Disabled)
	// An external plugin used by the project runs its views (#129) when the capabilities it
	// requires are there; what it provides is not given to the others (its views reach nothing
	// but the sandbox API).
	provided := Provided(out)
	for _, e := range r.Externals() {
		e := e
		_, used := ch.External[e.Manifest.ID]
		s := State{Manifest: e.Manifest, Disabled: !used, Enabled: used, External: &e}
		for _, c := range e.Manifest.Requires {
			if !provided[c] {
				s.Missing = append(s.Missing, c)
				s.Enabled = false
			}
		}
		out = append(out, s)
	}
	ids := make([]string, 0, len(ch.External))
	for id := range ch.External {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		if _, ok := r.External(id); ok || r.Builtin(id) {
			continue
		}
		out = append(out, State{Manifest: Manifest{ID: id, Name: id, APIVersion: APIVersion}, External: &External{URL: ch.External[id]}})
	}
	return out
}

func (r *Registry) resolveBuiltins(disabled []string) []State {
	out := make([]State, len(r.list))
	for i, p := range r.list {
		m := p.Manifest()
		off := slices.Contains(disabled, m.ID) && !m.Required
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

// Provided are the capabilities a project has: the core's and those of its enabled plugins.
func Provided(states []State) map[string]bool {
	out := map[string]bool{}
	for _, c := range CoreCapabilities {
		out[c] = true
	}
	for _, s := range states {
		if s.Enabled && s.External == nil {
			for _, c := range s.Provides() {
				out[c] = true
			}
		}
	}
	return out
}

// Provides are the capabilities of the plugin.
func (s State) Provides() []string { return s.Manifest.Provides }
