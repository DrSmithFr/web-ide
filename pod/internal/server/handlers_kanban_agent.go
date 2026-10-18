package server

import (
	"context"
	"encoding/json"
	"net"
	"strings"

	"github.com/DrSmithFr/web-ide/pod/internal/acp"
	"github.com/DrSmithFr/web-ide/pod/internal/assistant"
	"github.com/DrSmithFr/web-ide/pod/internal/i18n"
	"github.com/DrSmithFr/web-ide/pod/internal/kanban"
	"github.com/DrSmithFr/web-ide/pod/internal/projects"
	"github.com/DrSmithFr/web-ide/pod/internal/sshx"
)

// The development of a ticket by an ACP agent (kanban setting devAgent): one session of the
// Agents tool per ticket, in its worktree, with the MCP server of the IDE (docs/design/kanban.md).

// agentsMCP is the MCP server of the IDE as the sessions of the Agents tool reach it: the
// listen address of the pod, the token as a bearer. Named like the server users add to Claude
// Code (web-ide), so that the session's one replaces it instead of doubling its tools.
func (s *Server) agentsMCP() []acp.MCPServer {
	base := s.MCPBase
	if base == "" && s.Cfg != nil && s.Cfg.Addr != "" {
		host, port, err := net.SplitHostPort(s.Cfg.Addr)
		if err != nil {
			return nil
		}
		if host == "" || host == "0.0.0.0" || host == "::" {
			host = "127.0.0.1"
		}
		base = "http://" + net.JoinHostPort(host, port)
	}
	if base == "" {
		return nil
	}
	return []acp.MCPServer{{Type: "http", Name: "web-ide", URL: strings.TrimSuffix(base, "/") + mcpPath,
		Headers: []acp.Header{{Name: "Authorization", Value: "Bearer " + s.Token}}}}
}

// devAgent is the ACP agent that develops the tickets of a kanban ("" for the assistant).
func (s *Server) devAgent(loc kanban.Location) string {
	meta, _ := s.Kanban.Meta(loc)
	if a := meta["devAgent"]; a != "assistant" {
		return a
	}
	return ""
}

// ticketWorktree is the project of the worktree a ticket works in (the one of the root of its
// lineage).
func (s *Server) ticketWorktree(root *projects.Project, t *kanban.Ticket) (string, error) {
	owner := t.ID
	if t.Parent != 0 {
		owner = t.Parent
	}
	id := projects.ChildID(root.ID, owner)
	if _, ok := s.Projects.Get(id); !ok {
		return "", i18n.New("the ticket has no worktree: start its development first")
	}
	return id, nil
}

// ticketSession finds the session of a ticket in the files of the Agents tool of its
// worktree (open or not).
func (s *Server) ticketSession(root *projects.Project, t *kanban.Ticket) (project string, info acp.Info, entries []acp.Entry, ok bool) {
	owner := t.ID
	if t.Parent != 0 {
		owner = t.Parent
	}
	project = projects.ChildID(root.ID, owner)
	info, entries, ok = acp.Find(s.Store.Path("agents", project), t.ID)
	return project, info, entries, ok
}

func (s *Server) registerKanbanAgent() {
	h := func(f func(ctx context.Context, c *Client, k gctx, p json.RawMessage) (any, error)) handler {
		return func(ctx context.Context, c *Client, p json.RawMessage) (any, error) {
			root, err := s.kanbanProject(c.project)
			if err != nil {
				return nil, err
			}
			rt, err := c.runtime()
			if err != nil {
				return nil, err
			}
			return f(ctx, c, gctx{loc: kanbanLoc(root), root: root, rt: rt}, p)
		}
	}
	// kanban.agent gives a ticket to its development agent: its session in the worktree
	// (opened with the briefing of the role), or the message in the session it has.
	s.handle("kanban.agent", h(func(ctx context.Context, c *Client, k gctx, p json.RawMessage) (any, error) {
		a, err := bind[struct {
			ID       int64  `json:"id"`
			Role     string `json:"role"`
			Feedback int64  `json:"feedback"`
			Text     string `json:"text"`
		}](p)
		if err != nil {
			return nil, err
		}
		name := s.devAgent(k.loc)
		if name == "" {
			return nil, i18n.New("the kanban develops with the assistant: no development agent is set")
		}
		if _, ok := assistant.RoleInstructions[a.Role]; !ok {
			return nil, i18n.Errorf("unknown role: %s", a.Role)
		}
		t, err := s.Kanban.Get(k.loc, a.ID)
		if err != nil {
			return nil, err
		}
		project, err := s.ticketWorktree(k.root, t)
		if err != nil {
			return nil, err
		}
		rt, err := s.openRuntime(project, sshx.Creds{})
		if err != nil {
			return nil, err
		}
		if rt.Agents == nil {
			return nil, i18n.New("agents run on local projects only")
		}
		prompt := assistant.TicketPrompt(t.ID, a.Role, t.Branch, t.Parent, a.Feedback, kanban.Markdown(t))
		if a.Text != "" {
			prompt += "\n\n---\n\n" + a.Text
		}
		for _, info := range rt.Agents.List() {
			if info.Ticket != t.ID {
				continue
			}
			sess, err := rt.Agents.Get(info.ID)
			if err != nil {
				break
			}
			if err := sess.Prompt(prompt); err != nil {
				return nil, err
			}
			return map[string]any{"project": project, "session": info.ID}, nil
		}
		info, err := rt.Agents.CreateWith(name, "", acp.Options{Title: "#" + itoa(t.ID) + " " + t.Title, Ticket: t.ID, Prompt: prompt})
		if err != nil {
			return nil, err
		}
		s.emitter(project)("agents.created", info, "")
		_ = s.Kanban.Event(k.loc, t.ID, kanban.ByUser, "Development given to {agent}", kanban.Params{"agent": info.Name})
		s.emitKanban(k.root.ID, t.ID, nil)
		return map[string]any{"project": project, "session": info.ID}, nil
	}))
	// kanban.agent.find tells where the session of a ticket is ({} when it has none).
	s.handle("kanban.agent.find", h(func(ctx context.Context, c *Client, k gctx, p json.RawMessage) (any, error) {
		a, err := bind[struct {
			ID int64 `json:"id"`
		}](p)
		if err != nil {
			return nil, err
		}
		t, err := s.Kanban.Get(k.loc, a.ID)
		if err != nil {
			return nil, err
		}
		project, info, _, ok := s.ticketSession(k.root, t)
		if !ok {
			return map[string]any{}, nil
		}
		return map[string]any{"project": project, "session": info.ID, "name": info.Name, "state": info.State}, nil
	}))
}
