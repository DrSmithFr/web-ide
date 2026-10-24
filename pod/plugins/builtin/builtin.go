// Package builtin lists the built-in plugins, compiled into the pod.
package builtin

import (
	"github.com/DrSmithFr/web-ide/plugins/ai"
	"github.com/DrSmithFr/web-ide/plugins/code"
	"github.com/DrSmithFr/web-ide/plugins/database"
	"github.com/DrSmithFr/web-ide/plugins/docker"
	"github.com/DrSmithFr/web-ide/plugins/exec"
	"github.com/DrSmithFr/web-ide/plugins/files"
	"github.com/DrSmithFr/web-ide/plugins/git"
	"github.com/DrSmithFr/web-ide/plugins/hello"
	"github.com/DrSmithFr/web-ide/plugins/kanban"
	"github.com/DrSmithFr/web-ide/plugins/languages"
	"github.com/DrSmithFr/web-ide/plugins/previews"
	"github.com/DrSmithFr/web-ide/plugins/search"
	"github.com/DrSmithFr/web-ide/plugins/services"
	"github.com/DrSmithFr/web-ide/pod/plugins"
)

// Registry holds the built-in plugins; tests adds the plugins of the browser tests.
func Registry(tests bool) *plugins.Registry {
	r := plugins.NewRegistry()
	list := []plugins.Plugin{files.New(), code.New(), exec.New(), search.New(), git.New(), kanban.New(), ai.New(), previews.New(), services.New(), languages.New(), database.New(), docker.New()}
	if tests {
		list = append(list, hello.New())
	}
	for _, p := range list {
		if err := r.Add(p); err != nil {
			panic(err)
		}
	}
	return r
}
