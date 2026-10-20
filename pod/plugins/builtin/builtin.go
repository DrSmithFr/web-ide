// Package builtin lists the built-in plugins, compiled into the pod.
package builtin

import (
	"github.com/DrSmithFr/web-ide/pod/plugins"
	"github.com/DrSmithFr/web-ide/pod/plugins/ai"
	"github.com/DrSmithFr/web-ide/pod/plugins/code"
	"github.com/DrSmithFr/web-ide/pod/plugins/database"
	"github.com/DrSmithFr/web-ide/pod/plugins/docker"
	"github.com/DrSmithFr/web-ide/pod/plugins/exec"
	"github.com/DrSmithFr/web-ide/pod/plugins/files"
	"github.com/DrSmithFr/web-ide/pod/plugins/git"
	"github.com/DrSmithFr/web-ide/pod/plugins/hello"
	"github.com/DrSmithFr/web-ide/pod/plugins/kanban"
	"github.com/DrSmithFr/web-ide/pod/plugins/languages"
	"github.com/DrSmithFr/web-ide/pod/plugins/previews"
	"github.com/DrSmithFr/web-ide/pod/plugins/search"
)

// Registry holds the built-in plugins; tests adds the plugins of the browser tests.
func Registry(tests bool) *plugins.Registry {
	r := plugins.NewRegistry()
	list := []plugins.Plugin{files.New(), code.New(), exec.New(), search.New(), git.New(), kanban.New(), ai.New(), previews.New(), languages.New(), database.New(), docker.New()}
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
