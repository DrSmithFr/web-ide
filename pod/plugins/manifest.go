// Package plugins is the plugin system of the pod (docs/design/plugins.md): the manifest of a
// plugin, the registry of the built-in plugins, the capabilities they provide and require, and
// their enabling per project (.ide/plugins.json of the main project).
package plugins

import (
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strings"
)

// APIVersion is the version of the plugin API, frozen as 1 once Git moved (#118): a change of
// the API is a new version (docs/design/plugins.md, *API v1*).
const APIVersion = 1

// Manifest is the plugin.json of a plugin.
type Manifest struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Version    string `json:"version"`
	APIVersion int    `json:"apiVersion"`
	// Required: a locked plugin, always enabled (the IDE does not run without it: Files, Code).
	Required bool `json:"required,omitempty"`
	// Permissions are shown to the user (exec:docker, net…); not enforced for trusted plugins.
	Permissions []string `json:"permissions,omitempty"`
	Provides    []string `json:"provides,omitempty"`
	Requires    []string `json:"requires,omitempty"`
	// Namespaces are the prefixes of the RPC methods and the actions it owns besides its id
	// (console and commands for exec: their names were kept when the tool became a plugin).
	Namespaces  []string      `json:"namespaces,omitempty"`
	Contributes Contributions `json:"contributes"`
}

// Owns tells whether a method or action name (prefix.name) belongs to the plugin.
func (m Manifest) Owns(name string) bool {
	prefix, _, ok := strings.Cut(name, ".")
	return ok && (prefix == m.ID || slices.Contains(m.Namespaces, prefix))
}

// Contributions are what a plugin adds to the IDE at each contribution point.
type Contributions struct {
	Tools    []Tool    `json:"tools,omitempty"`
	TabViews []TabView `json:"tabViews,omitempty"`
	// AssistantTools are the names of the tools the plugin offers to the assistant and the MCP;
	// each one is registered with its handler (Host.AssistantTool).
	AssistantTools []string `json:"assistantTools,omitempty"`
	// MCPTools are the names of the tools it offers to the MCP only (Host.MCPTool).
	MCPTools []string  `json:"mcpTools,omitempty"`
	Commands []Command `json:"commands,omitempty"`
}

// toolNames are its tools of the assistant and of the MCP: one namespace.
func (c Contributions) toolNames() []string {
	return append(append([]string{}, c.AssistantTools...), c.MCPTools...)
}

// Tool is a tool of the window, shown from a rail icon. Its command tool.<id> is implied.
type Tool struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	Icon  string `json:"icon"`
	// Zone is the default tool zone: left, bottomLeft, bottomRight or right.
	Zone string `json:"zone"`
	// Before is the tool it comes before in its zone by default (else it comes last).
	Before string `json:"before,omitempty"`
}

// TabView is a kind of tab, opened as plugin:<plugin>/<id>.
type TabView struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

// Command is an action of the keymap, Search Everywhere and the menus, named <plugin>.<id>.
type Command struct {
	ID       string `json:"id"`
	Label    string `json:"label"`
	Category string `json:"category,omitempty"`
	// Keys are its default shortcuts (physical keys: Ctrl+Enter, Alt+KeyD…).
	Keys []string `json:"keys,omitempty"`
	// Replaces is the id the action had before (a tool of the core moved to a plugin): the
	// shortcuts set by the user for it are kept.
	Replaces string `json:"replaces,omitempty"`
	// InTerminal: it also runs while a terminal has the focus.
	InTerminal bool `json:"inTerminal,omitempty"`
}

// Parse reads and checks a plugin.json.
func Parse(data []byte) (Manifest, error) {
	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return m, fmt.Errorf("invalid plugin.json: %v", err)
	}
	return m, m.Check()
}

var (
	idRe   = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)
	toolRe = regexp.MustCompile(`^[a-z][a-z_0-9]*$`)
	zones  = map[string]bool{"left": true, "bottomLeft": true, "bottomRight": true, "right": true}
)

// Check validates a manifest: ids, zones, the API version.
func (m Manifest) Check() error {
	if !idRe.MatchString(m.ID) {
		return fmt.Errorf("plugin id %q: lowercase letters, digits and dashes", m.ID)
	}
	if m.APIVersion != APIVersion {
		return fmt.Errorf("plugin %s: API version %d, the pod speaks %d", m.ID, m.APIVersion, APIVersion)
	}
	seen := map[string]bool{}
	for _, t := range m.Contributes.Tools {
		if !idRe.MatchString(t.ID) || seen[t.ID] {
			return fmt.Errorf("plugin %s: invalid or repeated tool id %q", m.ID, t.ID)
		}
		if !zones[t.Zone] {
			return fmt.Errorf("plugin %s: tool %s has no valid zone (%q)", m.ID, t.ID, t.Zone)
		}
		seen[t.ID] = true
	}
	for _, v := range m.Contributes.TabViews {
		if !idRe.MatchString(v.ID) {
			return fmt.Errorf("plugin %s: invalid tab view id %q", m.ID, v.ID)
		}
	}
	for _, n := range m.Namespaces {
		if !idRe.MatchString(n) {
			return fmt.Errorf("plugin %s: invalid namespace %q", m.ID, n)
		}
	}
	for _, c := range m.Contributes.Commands {
		if strings.Contains(c.ID, ".") && !m.Owns(c.ID) {
			return fmt.Errorf("plugin %s: command %s is out of its namespaces", m.ID, c.ID)
		}
	}
	for _, name := range m.Contributes.toolNames() {
		if !toolRe.MatchString(name) {
			return fmt.Errorf("plugin %s: invalid assistant tool name %q", m.ID, name)
		}
	}
	return nil
}
