package acp

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/DrSmithFr/web-ide/pod/internal/i18n"
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
}

// Agents are the agents the Agents tool offers, versions pinned (spike of 2026-10-13).
var Agents = []Agent{
	{ID: "claude", Name: "Claude Code", Bin: "claude-agent-acp", Package: "@agentclientprotocol/claude-agent-acp@0.89.1"},
	{ID: "opencode", Name: "opencode", Bin: "opencode", Args: []string{"acp"}, Paths: []string{"~/.opencode/bin/opencode"}, Hint: "curl -fsSL https://opencode.ai/install | bash"},
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
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	out, err := exec.CommandContext(ctx, "npm", "install", "--prefix", c.Dir, "--no-audit", "--no-fund", a.Package).CombinedOutput()
	if err != nil {
		text := strings.TrimSpace(string(out))
		if len(text) > 1500 {
			text = text[len(text)-1500:]
		}
		return i18n.Errorf("the installation failed: %s", text)
	}
	return nil
}
