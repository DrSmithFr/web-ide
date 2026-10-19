// Package ai is the AI plugin: the assistant (local models, the turn loop and its tools,
// sub-agents, the Orchestrator, compaction, doodles and the board of a conversation) and the
// Agents tool (the sessions of the CLI agents, ACP). The conversations are kept by
// pod/internal/llm and the agents run in the runtime of a project (pod/internal/acp): both are
// the pod's, lent to the plugin, since the MCP and the Questions tool read them too. Off on a
// project: no assistant nor agents; the MCP for Claude Code stays the core's.
package ai

import (
	"context"
	_ "embed"
	"encoding/json"
	"github.com/DrSmithFr/web-ide/pod/internal/assistant"
	"github.com/DrSmithFr/web-ide/pod/internal/commands"
	"github.com/DrSmithFr/web-ide/pod/internal/i18n"
	kbp "github.com/DrSmithFr/web-ide/pod/internal/plugins/kanban"
	"github.com/DrSmithFr/web-ide/pod/internal/runtime"
	"time"

	"github.com/DrSmithFr/web-ide/pod/internal/plugins"
)

//go:embed plugin.json
var manifest []byte

// A is the plugin: its methods run with the services the pod lends it.
type A struct {
	*plugins.Services
	m    plugins.Manifest
	pod  plugins.Pod
	host plugins.Host
	// assistants are the conversations of the assistant running in the pod.
	assistants assistants
}

func New() *A {
	m, err := plugins.Parse(manifest)
	if err != nil {
		panic(err)
	}
	return &A{m: m, assistants: assistants{runs: map[string]*assistantRun{}, slots: map[string]chan struct{}{}, waiting: map[string]int{}}}
}

func (s *A) Manifest() plugins.Manifest { return s.m }

func (s *A) Register(h plugins.Host) {
	s.host, s.pod = h, h.Pod()
	s.Services = s.pod.Services()
	s.registerLLM()
	s.registerAssistant()
	s.registerAgents()
	// The conversations that ran when the pod stopped are taken back, and saved when it stops.
	h.OnStart(s.ResumeRuns)
	h.OnStop(s.saveRunning)
}

// Client is the window of a call: its project, its id, the events pushed to it.
type Client struct {
	call    plugins.Call
	project string
	id      string
}

func (c *Client) push(event string, data any) {
	if c.call.Window != nil {
		c.call.Window.Push(event, data)
	}
}
func (c *Client) language() string { return c.call.Lang }

// gone tells that the connection of the window is closed.
func (c *Client) gone() bool {
	if c.call.Window == nil {
		return false
	}
	select {
	case <-c.call.Window.Done():
		return true
	default:
		return false
	}
}
func (c *Client) runtime() (*runtime.Runtime, error) {
	if c.call.Runtime == nil {
		return nil, i18n.New("no project open on this connection")
	}
	return c.call.Runtime, nil
}

type handler func(ctx context.Context, c *Client, p json.RawMessage) (any, error)

// global are the methods of the configuration of the pod (model servers, speech models): they
// answer without a project, as Settings asks them.
var global = map[string]bool{
	"llm.config": true, "llm.models": true, "llm.select": true, "llm.children": true, "llm.server.save": true, "llm.server.delete": true,
	"models.list": true, "models.delete": true,
}

// handle registers a method of the plugin, run in the project of the window that calls it.
func (s *A) handle(name string, f handler) {
	reg := s.host.Handle
	if global[name] {
		reg = s.host.HandleGlobal
	}
	reg(name, func(ctx context.Context, call plugins.Call) (any, error) {
		c := &Client{call: call}
		if call.Window != nil {
			c.project, c.id = call.Window.Project(), call.Window.ID()
		}
		return f(ctx, c, call.Params)
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

// kb is the Kanban plugin, for the kanban tools of the assistant and the Orchestrator (nil
// without it).
func (s *A) kb() *kbp.K {
	p, _ := s.pod.Plugin("kanban")
	k, _ := p.(*kbp.K)
	return k
}

func (s *A) stopping() bool { return s.pod.Stopping() }

// findCommand is a saved command of the project of rt.
func (s *A) findCommand(rt *runtime.Runtime, id string) (commands.Command, error) {
	list, err := s.Commands(rt)
	if err != nil {
		return commands.Command{}, err
	}
	cmd, ok := commands.Find(list, id)
	if !ok {
		return commands.Command{}, i18n.Errorf("command not found: %s", id)
	}
	return cmd, nil
}

// uiCall asks a window of the project to run a tool of the interface for the conversation of r.
func (s *A) uiCall(r *assistantRun, name string, args any, wait bool, timeout time.Duration) (plugins.UIResult, bool, bool) {
	return s.pod.UICall(r.ctx, r.project, r.id, name, args, wait, timeout)
}

func (s *A) uiTool(r *assistantRun, name string, a assistant.Args) (assistant.ToolResult, error) {
	quiet := a.Bool("quiet", false)
	res, window, answered := s.uiCall(r, name, a, !quiet, 10*time.Second)
	switch {
	case !window:
		return assistant.ToolResult{Content: "No IDE window is open: the user will not see it.", Summary: assistant.T("no window open", nil).Raw(), Status: "ok"}, nil
	case quiet:
		return assistant.ToolResult{}, nil
	case !answered:
		return assistant.ToolResult{Content: "No IDE window answered: the user may not see it.", Summary: assistant.T("no window open", nil).Raw(), Status: "ok"}, nil
	}
	return assistant.ToolResult{Content: res.Content, Summary: res.Summary, Status: res.Status}, nil
}
