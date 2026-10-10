package server

import (
	"context"
	"encoding/json"
	"path"

	"github.com/DrSmithFr/web-ide/pod/internal/commands"
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

// runSaved runs a saved command in rt. A console of the command still running is
// answered as running unless restart; it is closed (ended or restarted) and replaced.
func (s *Server) runSaved(c *Client, rt *runtime.Runtime, id string, restart bool) (map[string]any, error) {
	list, err := commands.Load(rt.FS, rt.IdeDir)
	if err != nil {
		return nil, err
	}
	cmd, ok := commands.Find(list, id)
	if !ok {
		return nil, i18n.New("command not found")
	}
	olds := rt.Consoles.OfCommand(cmd.ID)
	for _, o := range olds {
		if !o.Exited && !restart {
			return map[string]any{"running": o}, nil
		}
	}
	replaced := ""
	for _, o := range olds {
		if rt.Consoles.Close(o.ID) == nil {
			s.emitter(rt.P.ID)("console.closed", map[string]string{"id": o.ID}, "")
			replaced = o.ID
		}
	}
	cwd := rt.Root
	if cmd.Cwd != "" {
		cwd = path.Join(rt.Root, cmd.Cwd)
	}
	info, err := rt.Consoles.RunCommand(cmd.ID, cmd.Name, commands.Argv(cmd), cwd)
	if err != nil {
		return nil, err
	}
	except := ""
	if c != nil {
		except = c.id
	}
	s.emitter(rt.P.ID)("console.created", info, except)
	return map[string]any{"console": info, "replaced": replaced}, nil
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
		list, err := commands.Load(rt.FS, rt.IdeDir)
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
		return s.runSaved(c, rt, a.ID, a.Restart)
	}))
}
