package server

import (
	"context"
	"encoding/json"
	"log"
	"path"
	"strings"
	"sync"

	"github.com/DrSmithFr/web-ide/pod/internal/commands"
	"github.com/DrSmithFr/web-ide/pod/internal/console"
	"github.com/DrSmithFr/web-ide/pod/internal/i18n"
	"github.com/DrSmithFr/web-ide/pod/internal/runtime"
)

// Saved commands of a project (package commands): kept in the .ide of the main project,
// run as tasks in the worktree of the window, one console per command.

// emitFamily sends an event to the windows of a main project and of its worktrees.
func (s *Server) emitFamily(root, event string, data any) {
	s.mu.Lock()
	var targets []*Client
	for c := range s.clients {
		if c.project == "" {
			continue
		}
		if c.project == root {
			targets = append(targets, c)
		} else if p, ok := s.Projects.Get(c.project); ok && p.Parent == root {
			targets = append(targets, c)
		}
	}
	s.mu.Unlock()
	for _, c := range targets {
		c.push(event, data)
	}
}

// mainID is the main project of a runtime (itself, or the parent of a worktree).
func mainID(rt *runtime.Runtime) string {
	if rt.P.Parent != "" {
		return rt.P.Parent
	}
	return rt.P.ID
}

// migrateMu keeps two loads from moving the setup command of the kanban twice.
var migrateMu sync.Mutex

// projectCommands loads the saved commands of the project of rt. The setup command of the
// kanban settings (before the setup flag) becomes a setup command once, then is cleared.
func (s *Server) projectCommands(rt *runtime.Runtime) ([]commands.Command, error) {
	migrateMu.Lock()
	defer migrateMu.Unlock()
	list, err := commands.Load(rt.FS, rt.IdeDir)
	if err != nil {
		return nil, err
	}
	root, err := s.kanbanProject(rt.P.ID)
	if err != nil || !s.Kanban.Has(kanbanLoc(root)) {
		return list, nil
	}
	loc := kanbanLoc(root)
	meta, err := s.Kanban.Meta(loc)
	setup := strings.TrimSpace(meta["setup"])
	if err != nil || setup == "" {
		return list, nil
	}
	saved, err := commands.Save(rt.FS, rt.IdeDir, append(list, commands.Command{Name: "Worktree setup", Command: setup, Flag: "setup",
		Description: "Moved from the kanban settings"}))
	if err != nil {
		log.Printf("setup command of the kanban of %s not moved: %v", root.ID, err)
		return list, nil
	}
	if err := s.Kanban.SetMeta(loc, map[string]string{"setup": ""}); err != nil {
		return saved, err
	}
	s.emitFamily(root.ID, "commands.changed", map[string]any{"commands": saved})
	return saved, nil
}

// findCommand is a saved command of the project of rt by its id.
func (s *Server) findCommand(rt *runtime.Runtime, id string) (commands.Command, error) {
	list, err := s.projectCommands(rt)
	if err != nil {
		return commands.Command{}, err
	}
	cmd, ok := commands.Find(list, id)
	if !ok {
		return commands.Command{}, i18n.Errorf("command not found: %s", id)
	}
	return cmd, nil
}

// savedRun is what running a saved command did: Running, the console of the command
// still running (nothing started); else Console, the new one, which replaced Replaced.
type savedRun struct {
	Running  *console.Info `json:"running,omitempty"`
	Console  *console.Info `json:"console,omitempty"`
	Replaced string        `json:"replaced,omitempty"`
}

// runSaved runs a saved command in rt. A console of the command still running is
// answered as running unless restart; it is closed (ended or restarted) and replaced.
// except: the window that asked, which adds the console itself.
func (s *Server) runSaved(rt *runtime.Runtime, cmd commands.Command, restart bool, except string) (savedRun, error) {
	olds := rt.Consoles.OfCommand(cmd.ID)
	for _, o := range olds {
		if !o.Exited && !restart {
			return savedRun{Running: &o}, nil
		}
	}
	var out savedRun
	for _, o := range olds {
		if rt.Consoles.Close(o.ID) == nil {
			s.emitter(rt.P.ID)("console.closed", map[string]string{"id": o.ID}, "")
			out.Replaced = o.ID
		}
	}
	cwd := rt.Root
	if cmd.Cwd != "" {
		cwd = path.Join(rt.Root, cmd.Cwd)
	}
	info, err := rt.Consoles.RunCommand(cmd.ID, cmd.Name, commands.Argv(cmd), cwd)
	if err != nil {
		return savedRun{}, err
	}
	s.emitter(rt.P.ID)("console.created", info, except)
	out.Console = &info
	return out, nil
}

func (s *Server) registerCommands() {
	withRT := func(f func(ctx context.Context, c *Client, rt *runtime.Runtime, p json.RawMessage) (any, error)) handler {
		return func(ctx context.Context, c *Client, p json.RawMessage) (any, error) {
			rt, err := c.runtime()
			if err != nil {
				return nil, err
			}
			return f(ctx, c, rt, p)
		}
	}
	// The commands, and the error of an invalid file (shown, the file left as it is).
	s.handle("commands.list", withRT(func(ctx context.Context, c *Client, rt *runtime.Runtime, p json.RawMessage) (any, error) {
		list, err := s.projectCommands(rt)
		if err != nil {
			return map[string]any{"commands": []commands.Command{}, "error": i18n.Translate(c.language(), err)}, nil
		}
		return map[string]any{"commands": list}, nil
	}))
	s.handle("commands.save", withRT(func(ctx context.Context, c *Client, rt *runtime.Runtime, p json.RawMessage) (any, error) {
		a, err := bind[struct{ Commands []commands.Command }](p)
		if err != nil {
			return nil, err
		}
		list, err := commands.Save(rt.FS, rt.IdeDir, a.Commands)
		if err != nil {
			return nil, err
		}
		s.emitFamily(mainID(rt), "commands.changed", map[string]any{"commands": list})
		return list, nil
	}))
	s.handle("commands.run", withRT(func(ctx context.Context, c *Client, rt *runtime.Runtime, p json.RawMessage) (any, error) {
		a, err := bind[struct {
			ID      string
			Restart bool
		}](p)
		if err != nil {
			return nil, err
		}
		cmd, err := s.findCommand(rt, a.ID)
		if err != nil {
			return nil, err
		}
		return s.runSaved(rt, cmd, a.Restart, c.id)
	}))
}
