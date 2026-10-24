package server

import (
	"antennae.space/ide/lib/i18n"
	"antennae.space/ide/pod/commands"
	"antennae.space/ide/pod/runtime"
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

// projectCommands loads the saved commands of the project of rt.
func (s *Server) projectCommands(rt *runtime.Runtime) ([]commands.Command, error) {
	return commands.Load(rt.FS, rt.IdeDir)
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
