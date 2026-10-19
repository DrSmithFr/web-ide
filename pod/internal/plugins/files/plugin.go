// Package files is the Files plugin: the Explorer of the project and the Files tab of Search Everywhere. It is
// locked (required): the IDE does not run without it. It has no method of its own: the files and
// the buffers are the core's (fs.*, buffer.*).
package files

import (
	_ "embed"

	"github.com/DrSmithFr/web-ide/pod/internal/plugins"
)

//go:embed plugin.json
var manifest []byte

type Plugin struct{ m plugins.Manifest }

func New() *Plugin {
	m, err := plugins.Parse(manifest)
	if err != nil {
		panic(err)
	}
	return &Plugin{m: m}
}

func (p *Plugin) Manifest() plugins.Manifest { return p.m }

func (p *Plugin) Register(h plugins.Host) {}
