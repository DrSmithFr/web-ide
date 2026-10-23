package server

import (
	"log"
	"strings"
	"sync"

	"github.com/DrSmithFr/web-ide/pod/kanban"

	"github.com/DrSmithFr/web-ide/lib/i18n"
	"github.com/DrSmithFr/web-ide/pod/commands"
	"github.com/DrSmithFr/web-ide/pod/runtime"
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
	root, err := s.Projects.Root(rt.P.ID)
	if err != nil || !s.Kanban.Has(kanban.LocationOf(root)) {
		return list, nil
	}
	loc := kanban.LocationOf(root)
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

// runSaved runs a saved command in rt (commands.RunSaved), its events to the windows of rt.
func (s *Server) runSaved(rt *runtime.Runtime, cmd commands.Command, restart bool, except string) (commands.Run, error) {
	return commands.RunSaved(rt.Consoles, rt.Root, cmd, restart, commands.Emit(s.emitter(rt.P.ID)), except)
}
