package docker

import (
	dk "github.com/DrSmithFr/web-ide/plugins/docker/internal/docker"
	"github.com/DrSmithFr/web-ide/pod/runtime"
)

// dockerOf is the docker command of the project of rt, a part of its runtime made on first use.
func dockerOf(rt *runtime.Runtime) *dk.Docker {
	return rt.Part("docker", func() any {
		exists := func(p string) bool { _, err := rt.FS.Stat(p); return err == nil }
		return dk.New(rt.Runner, rt.Root, exists)
	}).(*dk.Docker)
}
