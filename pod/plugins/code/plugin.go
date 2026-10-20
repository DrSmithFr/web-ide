// Package code is the Code plugin: the view of a file buffer, the default view of the file tabs. It is
// locked (required): the IDE does not run without it. It has no method of its own: the files and
// the buffers are the core's (fs.*, buffer.*).
package code

import (
	_ "embed"

	"github.com/DrSmithFr/web-ide/pod/plugins"
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
