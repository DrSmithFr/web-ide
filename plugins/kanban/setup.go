package kanban

import (
	"log"
	"strings"
	"sync"

	kb "antennae.space/ide/plugins/kanban/kanban"
	"antennae.space/ide/pod/commands"
	"antennae.space/ide/pod/runtime"
)

// migrateMu keeps two loads from moving the setup command of the kanban twice.
var migrateMu sync.Mutex

// projectCommands loads the saved commands of the project of rt. The setup command of the
// kanban settings (before the setup flag) becomes a setup command once, then is cleared: when
// a window opens the project, or before a worktree is set up.
func (s *K) projectCommands(rt *runtime.Runtime) ([]commands.Command, error) {
	migrateMu.Lock()
	defer migrateMu.Unlock()
	list, err := s.Commands(rt)
	if err != nil {
		return nil, err
	}
	root, err := s.Projects.Root(rt.P.ID)
	if err != nil || !s.Kanban.Has(kb.LocationOf(root)) {
		return list, nil
	}
	loc := kb.LocationOf(root)
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
	s.pod.EmitFamily(root.ID, "commands.changed", map[string]any{"commands": saved}, "")
	return saved, nil
}
