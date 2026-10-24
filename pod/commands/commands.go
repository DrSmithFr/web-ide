// Package commands keeps the saved commands of a project (Console ▶ menu): a name, a
// multiline bash command and a flag, in .ide/commands.json of the main project.
package commands

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"path"
	"strings"

	"antennae.space/ide/lib/i18n"
	"antennae.space/ide/pod/execx"
	"antennae.space/ide/pod/fsx"
	"antennae.space/ide/pod/idedir"
)

// Flags say what a command is for; the IDE relies on some of them (setup, serve).
var Flags = []string{"serve", "build", "test", "lint", "setup", "other"}

type Command struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Command     string `json:"command"`
	Flag        string `json:"flag"`
	// Cwd is a folder relative to the root of the worktree it runs in ("" the root).
	Cwd string `json:"cwd,omitempty"`
	// Port the server of a serve command listens on (0: none).
	Port int `json:"port,omitempty"`
}

type file struct {
	Commands []Command `json:"commands"`
}

const name = "commands.json"

// Load reads the commands of a .ide folder: none when the file is missing, an error when
// it cannot be read (the file is then left as it is).
func Load(fs fsx.FS, ideDir string) ([]Command, error) {
	data, err := fs.Read(path.Join(ideDir, name))
	if err != nil {
		if _, serr := fs.Stat(path.Join(ideDir, name)); serr != nil {
			return []Command{}, nil
		}
		return nil, err
	}
	var f file
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, i18n.Errorf("invalid .ide/commands.json: %v", err)
	}
	if f.Commands == nil {
		f.Commands = []Command{}
	}
	return f.Commands, nil
}

// Save checks and writes the commands (in their order); new ones get an id.
func Save(fs fsx.FS, ideDir string, list []Command) ([]Command, error) {
	if list == nil {
		list = []Command{}
	}
	seen := map[string]bool{}
	for i := range list {
		c := &list[i]
		c.Name = strings.TrimSpace(c.Name)
		c.Cwd = strings.Trim(strings.TrimSpace(c.Cwd), "/")
		if c.Flag != "serve" {
			c.Port = 0 // a port is for a server only
		}
		if err := Check(*c); err != nil {
			return nil, err
		}
		if c.ID == "" || seen[c.ID] {
			c.ID = newID()
		}
		seen[c.ID] = true
	}
	if err := idedir.Ensure(fs, ideDir); err != nil {
		return nil, err
	}
	data, _ := json.MarshalIndent(file{Commands: list}, "", "  ")
	return list, fs.Write(path.Join(ideDir, name), append(data, '\n'))
}

// Check validates a command.
func Check(c Command) error {
	if strings.TrimSpace(c.Name) == "" {
		return i18n.New("a command needs a name")
	}
	if strings.TrimSpace(c.Command) == "" {
		return i18n.Errorf("the command “%s” is empty", c.Name)
	}
	ok := false
	for _, f := range Flags {
		ok = ok || f == c.Flag
	}
	if !ok {
		return i18n.Errorf("unknown flag: %s", c.Flag)
	}
	if strings.HasPrefix(c.Cwd, "/") || strings.HasPrefix(c.Cwd, "~") || strings.Contains("/"+c.Cwd+"/", "/../") {
		return i18n.Errorf("the folder of “%s” must be inside the project", c.Name)
	}
	if c.Port < 0 || c.Port > 65535 {
		return i18n.Errorf("invalid port: %d", c.Port)
	}
	return nil
}

// Find returns the command of an id.
func Find(list []Command, id string) (Command, bool) {
	for _, c := range list {
		if c.ID == id {
			return c, true
		}
	}
	return Command{}, false
}

// Argv is what runs a command: bash, as a login shell (the PATH of the user).
func Argv(c Command) []string { return []string{"bash", "-lc", c.Command} }

// OfFlag keeps the commands of a flag, in their order.
func OfFlag(list []Command, flag string) []Command {
	var out []Command
	for _, c := range list {
		if c.Flag == flag {
			out = append(out, c)
		}
	}
	return out
}

// SetupScript is what sets a new worktree up: its setup commands one after the other, each
// in its folder, stopping at the first one that fails; "" when there is none.
func SetupScript(list []Command) string {
	var steps []string
	for _, c := range OfFlag(list, "setup") {
		step := "echo " + execx.Quote("$ "+c.Name) + " && "
		if c.Cwd != "" {
			step += "cd " + execx.Quote(c.Cwd) + " && "
		}
		steps = append(steps, "("+step+"bash -lc "+execx.Quote(c.Command)+")")
	}
	return strings.Join(steps, " && ")
}

func newID() string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
