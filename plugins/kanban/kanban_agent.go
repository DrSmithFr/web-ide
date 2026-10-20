package kanban

import (
	"context"
	"encoding/json"

	"github.com/DrSmithFr/web-ide/lib/i18n"
	"github.com/DrSmithFr/web-ide/pod/acp"
	"github.com/DrSmithFr/web-ide/pod/assistant"
	kb "github.com/DrSmithFr/web-ide/pod/kanban"
	"github.com/DrSmithFr/web-ide/pod/projects"
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

// ticketSession finds the session of a ticket in the files of the Agents tool of its
// worktree (open or not).
func (s *K) ticketSession(root *projects.Project, t *kb.Ticket) (project string, info acp.Info, entries []acp.Entry, ok bool) {
	owner := t.ID
	if t.Parent != 0 {
		owner = t.Parent
	}
	project = projects.ChildID(root.ID, owner)
	info, entries, ok = acp.Find(s.Store.Path("agents", project), t.ID)
	return project, info, entries, ok
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
		rt, err := s.pod.Runtime(project)
		if err != nil {
			return nil, err
		}
		if rt.Agents == nil {
			return nil, i18n.New("agents run on local projects only")
		}
		prompt := assistant.TicketPrompt(t.ID, a.Role, t.Branch, t.Parent, a.Feedback, kb.Markdown(t))
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
		s.pod.Emit(project, "agents.created", info)
		_ = s.Kanban.Event(k.loc, t.ID, kb.ByUser, "Development given to {agent}", kb.Params{"agent": info.Name})
		s.emitKanban(k.root.ID, t.ID, nil)
		return map[string]any{"project": project, "session": info.ID}, nil
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
		project, info, _, ok := s.ticketSession(k.root, t)
		if !ok {
			return map[string]any{}, nil
		}
		return map[string]any{"project": project, "session": info.ID, "name": info.Name, "state": info.State}, nil
	}))
}
