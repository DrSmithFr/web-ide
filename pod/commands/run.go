package commands

import (
	"path"

	"github.com/DrSmithFr/web-ide/pod/console"
)

// Run is what running a saved command did: Running, the console of the command still running
// (nothing started); else Console, the new one, which replaced Replaced.
type Run struct {
	Running  *console.Info `json:"running,omitempty"`
	Console  *console.Info `json:"console,omitempty"`
	Replaced string        `json:"replaced,omitempty"`
}

// Emit tells the windows of the project what changed (except: the window that asked, which
// adds the console itself).
type Emit func(event string, data any, except string)

// RunSaved runs a saved command in the consoles of a project rooted at root. A console of the
// command still running is answered as running unless restart; it is closed (ended or
// restarted) and replaced. The saved commands of the Exec plugin, the assistant and the
// previews run through it.
func RunSaved(cons *console.Manager, root string, cmd Command, restart bool, emit Emit, except string) (Run, error) {
	olds := cons.OfCommand(cmd.ID)
	for _, o := range olds {
		if !o.Exited && !restart {
			return Run{Running: &o}, nil
		}
	}
	var out Run
	for _, o := range olds {
		if cons.Close(o.ID) == nil {
			emit("console.closed", map[string]string{"id": o.ID}, "")
			out.Replaced = o.ID
		}
	}
	cwd := root
	if cmd.Cwd != "" {
		cwd = path.Join(root, cmd.Cwd)
	}
	info, err := cons.RunCommand(cmd.ID, cmd.Name, Argv(cmd), cwd)
	if err != nil {
		return Run{}, err
	}
	emit("console.created", info, except)
	out.Console = &info
	return out, nil
}
