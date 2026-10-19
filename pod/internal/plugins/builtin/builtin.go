// Package builtin lists the built-in plugins, compiled into the pod.
package builtin

import (
	"github.com/DrSmithFr/web-ide/pod/internal/plugins"
	"github.com/DrSmithFr/web-ide/pod/internal/plugins/hello"
)

// Registry holds the built-in plugins; tests adds the plugins of the browser tests.
func Registry(tests bool) *plugins.Registry {
	r := plugins.NewRegistry()
	var list []plugins.Plugin
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
