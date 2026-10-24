package kanban

import (
	"context"
	"encoding/json"

	"antennae.space/ide/lib/i18n"
	kb "antennae.space/ide/plugins/kanban/kanban"
	"antennae.space/ide/pod/caps"
	"antennae.space/ide/pod/projects"
	"antennae.space/ide/pod/runtime"
)

// The development of a ticket by an ACP agent (kanban setting devAgent): one session of the
// Agents tool per ticket, in its worktree, with the MCP server of the IDE (docs/design/kanban.md).

// devAgent is the ACP agent that develops the tickets of a kanban ("" for the assistant).
func (s *K) devAgent(loc kb.Location) string {
	meta, _ := s.Kanban.Meta(loc)
	if a := meta["devAgent"]; a != "assistant" {
		return a
	}
	return ""
}

// ticketWorktree is the project of the worktree a ticket works in (the one of the root of its
// lineage).
func (s *K) ticketWorktree(root *projects.Project, t *kb.Ticket) (string, error) {
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

// agents is the agents capability of the project of rt (the AI plugin), nil when it is off.
func (s *K) agents(rt *runtime.Runtime) caps.Agents {
	ag, _ := s.pod.Capability(rt, "agents").(caps.Agents)
	return ag
}

// ticketSession finds the session of a ticket in the files of the Agents tool of its
// worktree (open or not).
func (s *K) ticketSession(rt *runtime.Runtime, root *projects.Project, t *kb.Ticket) (project string, sess caps.AgentSession, ok bool) {
	owner := t.ID
	if t.Parent != 0 {
		owner = t.Parent
	}
	project = projects.ChildID(root.ID, owner)
	if ag := s.agents(rt); ag != nil {
		sess, ok = ag.TicketSession(project, t.ID)
	}
	return project, sess, ok
}

func (s *K) registerKanbanAgent() {
	h := func(f func(ctx context.Context, c *client, k gctx, p json.RawMessage) (any, error)) handler {
		return func(ctx context.Context, c *client, p json.RawMessage) (any, error) {
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
	s.handle("kanban.agent", h(func(ctx context.Context, c *client, k gctx, p json.RawMessage) (any, error) {
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
		ag := s.agents(k.rt)
		if ag == nil {
			return nil, i18n.Errorf("the %s plugin is disabled on this project", "AI")
		}
		t, err := s.Kanban.Get(k.loc, a.ID)
		if err != nil {
			return nil, err
		}
		project, err := s.ticketWorktree(k.root, t)
		if err != nil {
			return nil, err
		}
		sess, created, err := ag.Develop(project, name, caps.TicketBrief{ID: t.ID, Parent: t.Parent, Feedback: a.Feedback,
			Role: a.Role, Title: t.Title, Branch: t.Branch, Markdown: kb.Markdown(t), Text: a.Text})
		if err != nil {
			return nil, err
		}
		if !created {
			return map[string]any{"project": project, "session": sess.ID}, nil
		}
		_ = s.Kanban.Event(k.loc, t.ID, kb.ByUser, "Development given to {agent}", kb.Params{"agent": sess.Name})
		s.emitKanban(k.root.ID, t.ID, nil)
		return map[string]any{"project": project, "session": sess.ID}, nil
	}))
	// kanban.agent.find tells where the session of a ticket is ({} when it has none).
	s.handle("kanban.agent.find", h(func(ctx context.Context, c *client, k gctx, p json.RawMessage) (any, error) {
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
		project, sess, ok := s.ticketSession(k.rt, k.root, t)
		if !ok {
			return map[string]any{}, nil
		}
		return map[string]any{"project": project, "session": sess.ID, "name": sess.Name, "state": sess.State}, nil
	}))
}
