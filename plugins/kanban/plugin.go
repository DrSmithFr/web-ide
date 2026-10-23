// Package kanban is the Kanban plugin: the tickets of a project, their lineages, milestones and
// objectives, the git workflow of a ticket (worktree, diff, merge) and the dev sessions of the
// agents (docs/design/kanban.md). The tickets are kept by its package kanban/, whose manager
// the plugin makes and closes; the AI reads it through the plugin. The plugin provides tickets
// and requires terminal (setup commands) and worktrees (Git).
package kanban

import (
	"context"
	_ "embed"
	"encoding/json"

	kb "github.com/DrSmithFr/web-ide/plugins/kanban/kanban"
	"github.com/DrSmithFr/web-ide/pod/plugins"
	"github.com/DrSmithFr/web-ide/pod/runtime"
)

//go:embed plugin.json
var manifest []byte

// K is the plugin: its methods run with the services the pod lends it.
type K struct {
	*plugins.Services
	// Kanban keeps the tickets of every project (one base each), for the pod's life.
	Kanban *kb.Manager
	m      plugins.Manifest
	pod    plugins.Pod
	host   plugins.Host
}

func New() *K {
	m, err := plugins.Parse(manifest)
	if err != nil {
		panic(err)
	}
	return &K{m: m}
}

func (s *K) Manifest() plugins.Manifest { return s.m }

func (s *K) Register(h plugins.Host) {
	s.host, s.pod = h, h.Pod()
	s.Services = s.pod.Services()
	s.Kanban = kb.NewManager(s.Store)
	h.OnStop(s.Kanban.Close)
	h.OnProjectOpen(func(rt *runtime.Runtime) { _, _ = s.projectCommands(rt) })
	s.registerKanban()
	s.registerKanbanGit()
	s.registerKanbanAgent()
	s.registerMilestones()
	s.registerObjectives()
}

// client is the window of a call: its project, its id, the events pushed to it.
type client struct {
	call    plugins.Call
	project string
	id      string
}

func (c *client) push(event string, data any)        { c.call.Window.Push(event, data) }
func (c *client) language() string                   { return c.call.Lang }
func (c *client) runtime() (*runtime.Runtime, error) { return c.call.Runtime, nil }

type handler func(ctx context.Context, c *client, p json.RawMessage) (any, error)

// handle registers a method of the kanban, run in the project of the window that calls it.
func (s *K) handle(name string, f handler) {
	s.host.Handle(name, func(ctx context.Context, call plugins.Call) (any, error) {
		return f(ctx, &client{call: call, project: call.Window.Project(), id: call.Window.ID()}, call.Params)
	})
}

func bind[T any](raw json.RawMessage) (T, error) {
	var v T
	if len(raw) == 0 || string(raw) == "null" {
		return v, nil
	}
	err := json.Unmarshal(raw, &v)
	return v, err
}
