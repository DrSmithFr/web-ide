// Package builtin lists the built-in plugins, compiled into the pod.
package builtin

import (
	"antennae.space/ide/plugins/ai"
	"antennae.space/ide/plugins/code"
	"antennae.space/ide/plugins/database"
	"antennae.space/ide/plugins/docker"
	"antennae.space/ide/plugins/exec"
	"antennae.space/ide/plugins/files"
	"antennae.space/ide/plugins/git"
	"antennae.space/ide/plugins/hello"
	"antennae.space/ide/plugins/kanban"
	"antennae.space/ide/plugins/languages"
	"antennae.space/ide/plugins/previews"
	"antennae.space/ide/plugins/search"
	"antennae.space/ide/plugins/services"
	"antennae.space/ide/pod/plugins"
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
