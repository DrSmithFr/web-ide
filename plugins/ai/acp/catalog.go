package acp

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/DrSmithFr/web-ide/lib/i18n"
)

// Agent is a CLI agent the IDE can run through ACP.
type Agent struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// Bin is the command speaking ACP; Args follow it.
	Bin  string   `json:"-"`
	Args []string `json:"-"`
	// Package is the npm package of an adapter the IDE installs (empty: installed by hand).
	Package string `json:"package,omitempty"`
	// Needs is a command the adapter runs (pi-acp runs pi).
	Needs string `json:"-"`
	// Paths are other places of Bin than the PATH (installers that do not touch it).
	Paths []string `json:"-"`
	// Hint tells how to install an agent the IDE does not install.
	Hint string `json:"hint,omitempty"`
	// Hidden are the commands the agent runs but does not list.
	Hidden []Command `json:"-"`
}

// Command is a slash command of an agent (ACP AvailableCommand).
type Command struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// Agents are the agents the Agents tool offers, versions pinned (spike of 2026-10-13).
var Agents = []Agent{
	{ID: "claude", Name: "Claude Code", Bin: "claude-agent-acp", Package: "@agentclientprotocol/claude-agent-acp@0.89.1"},
	{ID: "opencode", Name: "opencode", Bin: "opencode", Args: []string{"acp"}, Paths: []string{"~/.opencode/bin/opencode"}, Hint: "curl -fsSL https://opencode.ai/install | bash",
		Hidden: []Command{{Name: "compact", Description: "Compact the session context"}}},
	{ID: "pi", Name: "pi", Bin: "pi-acp", Package: "pi-acp@0.0.34", Needs: "pi"},
}

func agentByID(id string) (Agent, bool) {
	for _, a := range Agents {
		if a.ID == id {
			return a, true
		}
	}
	return Agent{}, false
}

// Status is an agent as the + menu shows it.
type Status struct {
	Agent
	Installed bool `json:"installed"`
	// Missing names a command the agent needs and that is not found.
	Missing string `json:"missing,omitempty"`
}

// Catalog finds the agents: in the PATH, then in the folder where the IDE installs adapters.
type Catalog struct {
	// Dir holds the adapters installed by the IDE (<data>/acp, an npm prefix).
	Dir string
}

func expand(p string) string {
	if strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, p[2:])
		}
	}
	return p
}

func (c Catalog) find(bin string, paths []string) string {
	if p, err := exec.LookPath(bin); err == nil {
		return p
	}
	for _, p := range append(paths, filepath.Join(c.Dir, "node_modules", ".bin", bin)) {
		p = expand(p)
		if st, err := os.Stat(p); err == nil && !st.IsDir() && st.Mode()&0o111 != 0 {
			return p
		}
	}
	return ""
}

func (c Catalog) List() []Status {
	out := make([]Status, 0, len(Agents))
	for _, a := range Agents {
		s := Status{Agent: a, Installed: c.find(a.Bin, a.Paths) != ""}
		if a.Needs != "" && c.find(a.Needs, nil) == "" {
			s.Missing = a.Needs
		}
		out = append(out, s)
	}
	return out
}

// Command is the command line of an agent.
func (c Catalog) Command(id string) ([]string, error) {
	a, ok := agentByID(id)
	if !ok {
		return nil, i18n.Errorf("unknown agent: %s", id)
	}
	bin := c.find(a.Bin, a.Paths)
	if bin == "" {
		return nil, i18n.Errorf("%s is not installed", a.Name)
	}
	return append([]string{bin}, a.Args...), nil
}

// installMu runs one npm at a time in Dir: two at once break each other's renames.
var installMu sync.Mutex

// staleNpm matches the folders an interrupted npm leaves in node_modules (".express-BMNsvdDe"):
// the next install fails with ENOTEMPTY when it renames to the same name.
var staleNpm = regexp.MustCompile(`^\.[^.].*-[A-Za-z0-9]{8}$`)

// Install installs the adapter of an agent with npm, in Dir.
func (c Catalog) Install(ctx context.Context, id string) error {
	a, ok := agentByID(id)
	if !ok || a.Package == "" {
		return i18n.Errorf("%s cannot be installed by the IDE", id)
	}
	if _, err := exec.LookPath("npm"); err != nil {
		return i18n.New("npm is not found: install Node.js first")
	}
	if err := os.MkdirAll(c.Dir, 0o755); err != nil {
		return err
	}
	installMu.Lock()
	defer installMu.Unlock()
	cleanStaleNpm(filepath.Join(c.Dir, "node_modules"))
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	out, err := exec.CommandContext(ctx, "npm", "install", "--prefix", c.Dir, "--no-audit", "--no-fund", a.Package).CombinedOutput()
	if err != nil {
		return i18n.Errorf("the installation failed: %s", npmErrors(string(out)))
	}
	return nil
}

// cleanStaleNpm removes the folders an interrupted npm left in node_modules and its scopes.
func cleanStaleNpm(dir string) {
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		switch {
		case !e.IsDir():
		case staleNpm.MatchString(e.Name()):
			os.RemoveAll(filepath.Join(dir, e.Name()))
		case strings.HasPrefix(e.Name(), "@"):
			cleanStaleNpm(filepath.Join(dir, e.Name()))
		}
	}
}

// npmErrors keeps the error lines of an npm output (its warnings, such as an unsupported node
// version the adapters run with anyway, hide the cause), at most 1500 bytes.
func npmErrors(out string) string {
	var lines []string
	for _, l := range strings.Split(out, "\n") {
		if strings.HasPrefix(l, "npm error") || strings.HasPrefix(l, "npm ERR!") {
			lines = append(lines, l)
		}
	}
	text := strings.TrimSpace(strings.Join(lines, "\n"))
	if text == "" {
		text = strings.TrimSpace(out)
	}
	if len(text) > 1500 {
		text = text[len(text)-1500:]
	}
	return text
}

// withHidden adds to the commands an agent sent the ones it runs without listing them.
func withHidden(commands json.RawMessage, agent string) json.RawMessage {
	a, _ := agentByID(agent)
	var list []map[string]any
	if len(a.Hidden) == 0 || json.Unmarshal(commands, &list) != nil {
		return commands
	}
	for _, h := range a.Hidden {
		found := false
		for _, c := range list {
			found = found || c["name"] == h.Name
		}
		if !found {
			list = append(list, map[string]any{"name": h.Name, "description": h.Description})
		}
	}
	out, _ := json.Marshal(list)
	return out
}
