// Package search is the Search plugin: the text of the project (a text or an RE2 expression),
// for the Search tool and the Text tab of Search Everywhere. The list of the files
// (search.files) stays the core's: the explorer and Search Everywhere use it.
package search

import (
	"context"
	_ "embed"
	"encoding/json"

	lib "antennae.space/ide/plugins/search/internal/search"
	"antennae.space/ide/pod/caps"
	"antennae.space/ide/pod/plugins"
	"antennae.space/ide/pod/runtime"
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

func (p *Plugin) Register(h plugins.Host) {
	h.Provide("search", grep{})
	h.Handle("grep", func(ctx context.Context, c plugins.Call) (any, error) {
		var o lib.Options
		if len(c.Params) > 0 {
			if err := json.Unmarshal(c.Params, &o); err != nil {
				return nil, err
			}
		}
		if o.Query == "" {
			return lib.Result{Matches: []lib.Match{}}, nil
		}
		return grep{}.Grep(ctx, c.Runtime, o)
	})
}

// grep is the search capability (caps.Search) the plugin provides: the search_text tool of the
// assistant uses it.
type grep struct{}

func (grep) Grep(ctx context.Context, rt *runtime.Runtime, o caps.SearchOptions) (*caps.SearchResult, error) {
	o.Exclude = rt.Excluded()
	if rt.Local {
		return lib.Local(ctx, rt.Root, o)
	}
	return lib.Remote(ctx, rt.Runner, rt.Root, o)
}
