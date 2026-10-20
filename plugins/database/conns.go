package database

import (
	"github.com/DrSmithFr/web-ide/plugins/database/internal/db"
	"github.com/DrSmithFr/web-ide/pod/runtime"
)

// conns is the part of a runtime holding its database connections: they close with the project.
// A worktree shares those of its main project, their secrets included.
type conns struct{ *db.Manager }

func (c conns) Stop(bool) { c.CloseAll() }

// connsOf are the connections of the project of rt, made on first use.
func connsOf(rt *runtime.Runtime) *db.Manager {
	return rt.Part("database", func() any {
		return conns{db.NewManager(db.Deps{FS: rt.FS, Root: rt.Root, IdeDir: rt.IdeDir, ProjectID: rt.SettingsID(), Local: rt.Local,
			Runner: rt.Runner, Pool: rt.Pool(), Store: rt.Store()})}
	}).(conns).Manager
}
