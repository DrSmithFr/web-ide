// Package builtin lists the built-in plugins, compiled into the pod.
package builtin

import (
	"github.com/DrSmithFr/web-ide/pod/internal/plugins"
	"github.com/DrSmithFr/web-ide/pod/internal/plugins/database"
	"github.com/DrSmithFr/web-ide/pod/internal/plugins/docker"
	"github.com/DrSmithFr/web-ide/pod/internal/plugins/exec"
	"github.com/DrSmithFr/web-ide/pod/internal/plugins/git"
	"github.com/DrSmithFr/web-ide/pod/internal/plugins/hello"
	"github.com/DrSmithFr/web-ide/pod/internal/plugins/languages"
	"github.com/DrSmithFr/web-ide/pod/internal/plugins/previews"
	"github.com/DrSmithFr/web-ide/pod/internal/plugins/search"
)

// Registry holds the built-in plugins; tests adds the plugins of the browser tests.
func Registry(tests bool) *plugins.Registry {
	r := plugins.NewRegistry()
	list := []plugins.Plugin{exec.New(), search.New(), git.New(), previews.New(), languages.New(), database.New(), docker.New()}
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
