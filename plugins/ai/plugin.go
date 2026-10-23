// Package ai is the AI plugin: the assistant (local models, the turn loop and its tools,
// sub-agents, the Orchestrator, compaction, doodles and the board of a conversation) and the
// Agents tool (the sessions of the CLI agents, ACP). The plugin keeps the model servers, their
// keys and the conversations (pod/llm), and the sessions of the agents as a part of the runtime
// of a project (pod/acp); the core reaches them through hooks only: the Questions tool
// (Host.Questions), the synced vault (Host.Vault), the Kanban (caps.Agents). Off on a project:
// no assistant nor agents; the MCP for Claude Code stays the core's.
package ai

import (
	"context"
	_ "embed"
	"encoding/json"
	"time"

	"github.com/DrSmithFr/web-ide/lib/i18n"
	kbp "github.com/DrSmithFr/web-ide/plugins/kanban"
	"github.com/DrSmithFr/web-ide/pod/assistant"
	"github.com/DrSmithFr/web-ide/pod/commands"
	"github.com/DrSmithFr/web-ide/pod/llm"
	"github.com/DrSmithFr/web-ide/pod/runtime"
	"github.com/DrSmithFr/web-ide/pod/settingsync"

	"github.com/DrSmithFr/web-ide/pod/plugins"
)

//go:embed plugin.json
var manifest []byte

// A is the plugin: its methods run with the services the pod lends it.
type A struct {
	*plugins.Services
	m    plugins.Manifest
	pod  plugins.Pod
	host plugins.Host
	// LLM keeps the model servers and the conversations.
	LLM *llm.Manager
	// assistants are the conversations of the assistant running in the pod, asking those that
	// ask the user, agentParts the sessions of the agents of the open projects.
	assistants assistants
	asking     asking
	agentParts agentParts
}

func New() *A {
	m, err := plugins.Parse(manifest)
	if err != nil {
		panic(err)
	}
	return &A{m: m, assistants: assistants{runs: map[string]*assistantRun{}, slots: map[string]chan struct{}{}, waiting: map[string]int{}},
		asking: asking{chats: map[string]bool{}}, agentParts: agentParts{open: map[string]*agentsPart{}}}
}

func (s *A) Manifest() plugins.Manifest { return s.m }

func (s *A) Register(h plugins.Host) {
	s.host, s.pod = h, h.Pod()
	s.Services = s.pod.Services()
	s.LLM = llm.New(s.Store)
	if s.Keeper != nil {
		s.LLM.Relay = llm.KeeperRelay{C: s.Keeper} // the answers being written survive the pod too
	}
	// The bases close last, once the conversations running are saved (OnStop: the last
	// registered first).
	h.OnStop(s.LLM.Close)
	h.Vault(settingsync.VaultMap{Name: "llm", Read: s.LLM.Keys, Apply: func(keys map[string]string) error {
		if err := s.LLM.ApplyKeys(keys); err != nil {
			return err
		}
		s.pod.Broadcast("llm.config", s.LLM.View())
		return nil
	}})
	h.Questions(plugins.Questions{Prefix: "acp", List: s.agentQuestions, Answer: s.answerAgent})
	h.Questions(plugins.Questions{Prefix: "chat", List: s.chatQuestions})
	h.Provide("agents", agentsCap{s})
	// The sessions the keeper kept are taken back when a window opens the project.
	h.OnProjectOpen(func(rt *runtime.Runtime) { s.agentsOf(rt) })
	s.registerLLM()
	s.registerAssistant()
	s.registerAgents()
	s.registerMCP()
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
