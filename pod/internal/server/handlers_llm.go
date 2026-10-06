package server

import (
	"context"
	"encoding/json"
	"path"
	"path/filepath"
	"strings"

	"github.com/DrSmithFr/web-ide/pod/internal/llm"
	"github.com/DrSmithFr/web-ide/pod/internal/runtime"
)

func (s *Server) registerLLM() {
	s.handle("llm.config", func(ctx context.Context, c *Client, p json.RawMessage) (any, error) {
		return s.LLM.View(), nil
	})
	s.handle("llm.server.save", func(ctx context.Context, c *Client, p json.RawMessage) (any, error) {
		a, err := bind[struct {
			llm.Server
			// The page never receives the key: an empty key keeps the stored one unless
			// clearKey is set.
			ClearKey bool `json:"clearKey"`
		}](p)
		if err != nil {
			return nil, err
		}
		if err := s.LLM.SaveServer(a.Server, !a.ClearKey); err != nil {
			return nil, err
		}
		view := s.LLM.View()
		s.broadcast("llm.config", view, c)
		return view, nil
	})
	s.handle("llm.server.delete", func(ctx context.Context, c *Client, p json.RawMessage) (any, error) {
		a, err := bind[struct{ ID string }](p)
		if err != nil {
			return nil, err
		}
		if err := s.LLM.DeleteServer(a.ID); err != nil {
			return nil, err
		}
		view := s.LLM.View()
		s.broadcast("llm.config", view, c)
		return view, nil
	})
	s.handle("llm.select", func(ctx context.Context, c *Client, p json.RawMessage) (any, error) {
		a, err := bind[struct{ Server, Model string }](p)
		if err != nil {
			return nil, err
		}
		return nil, s.LLM.Select(a.Server, a.Model)
	})
	s.handle("llm.models", func(ctx context.Context, c *Client, p json.RawMessage) (any, error) {
		a, err := bind[struct{ Server string }](p)
		if err != nil {
			return nil, err
		}
		return s.LLM.Models(ctx, a.Server)
	})
	// llm.chat runs a completion as a job of the pod and streams llm.delta events to the
	// asking window only; stream is an id chosen by the page. If the window goes away
	// (reload), the job goes on and llm.attach follows it again; Stop cancels it.
	push := func(c *Client, stream string) func(llm.Delta) {
		return func(d llm.Delta) {
			c.push("llm.delta", struct {
				Stream string `json:"stream"`
				llm.Delta
			}{stream, d})
		}
	}
	wait := func(ctx context.Context, c *Client, stream string, snapshot, watch bool) (any, error) {
		var onSnap func(llm.Snapshot)
		if snapshot {
			onSnap = func(sn llm.Snapshot) {
				c.push("llm.delta", struct {
					Stream     string `json:"stream"`
					IsSnapshot bool   `json:"snapshot"`
					llm.Snapshot
				}{stream, true, sn})
			}
		}
		res, err := s.LLM.WaitChat(ctx, stream, onSnap, push(c, stream))
		if err != nil && !watch && ctx.Err() != nil && c.ctx.Err() == nil {
			// Cancelled by the page itself (button Stop), not by a lost connection.
			s.LLM.CancelChat(stream)
		}
		return res, err
	}
	s.handle("llm.chat", func(ctx context.Context, c *Client, p json.RawMessage) (any, error) {
		a, err := bind[struct {
			llm.ChatRequest
			Stream string `json:"stream"`
		}](p)
		if err != nil {
			return nil, err
		}
		if err := s.LLM.StartChat(a.Stream, a.ChatRequest); err != nil {
			return nil, err
		}
		return wait(ctx, c, a.Stream, false, false)
	})
	s.handle("llm.attach", func(ctx context.Context, c *Client, p json.RawMessage) (any, error) {
		a, err := bind[struct {
			Stream string `json:"stream"`
			// Watch: a window that only follows (another one runs the conversation); its
			// end never cancels the completion.
			Watch bool `json:"watch"`
		}](p)
		if err != nil {
			return nil, err
		}
		return wait(ctx, c, a.Stream, true, a.Watch)
	})
	// Speech recognition models cached by the pod (downloaded by the page through it).
	s.handle("models.list", func(ctx context.Context, c *Client, p json.RawMessage) (any, error) {
		return s.Models.List()
	})
	s.handle("models.delete", func(ctx context.Context, c *Client, p json.RawMessage) (any, error) {
		a, err := bind[struct{ Repo string }](p)
		if err != nil {
			return nil, err
		}
		return nil, s.Models.Delete(a.Repo)
	})

	// Conversations, instructions and skills of the project of the connection.
	withProject := func(f func(ctx context.Context, c *Client, rt *runtime.Runtime, p json.RawMessage) (any, error)) handler {
		return func(ctx context.Context, c *Client, p json.RawMessage) (any, error) {
			rt, err := c.runtime()
			if err != nil {
				return nil, err
			}
			return f(ctx, c, rt, p)
		}
	}
	loc := func(c *Client, rt *runtime.Runtime) llm.ChatLocation {
		// The worktree of a ticket keeps its conversations with those of its parent.
		if p, ok := s.Projects.Get(c.project); ok && p.Parent != "" {
			if parent, err := s.kanbanProject(c.project); err == nil {
				k := kanbanLoc(parent)
				return llm.ChatLocation{Project: k.Project, IdeDir: k.IdeDir}
			}
		}
		l := llm.ChatLocation{Project: c.project}
		if rt.Local {
			l.IdeDir = filepath.Join(rt.Root, ".ide")
		}
		return l
	}
	proj := func(rt *runtime.Runtime) llm.Project { return llm.Project{Root: rt.Root, FS: rt.FS} }
	type idArg struct {
		ID string `json:"id"`
	}

	s.handle("llm.chats.list", withProject(func(ctx context.Context, c *Client, rt *runtime.Runtime, p json.RawMessage) (any, error) {
		return s.LLM.ListChats(loc(c, rt))
	}))
	s.handle("llm.chats.get", withProject(func(ctx context.Context, c *Client, rt *runtime.Runtime, p json.RawMessage) (any, error) {
		a, err := bind[idArg](p)
		if err != nil {
			return nil, err
		}
		return s.LLM.GetChat(loc(c, rt), a.ID)
	}))
	s.handle("llm.chats.save", withProject(func(ctx context.Context, c *Client, rt *runtime.Runtime, p json.RawMessage) (any, error) {
		a, err := bind[struct {
			Chat json.RawMessage `json:"chat"`
		}](p)
		if err != nil {
			return nil, err
		}
		if err := s.LLM.SaveChat(loc(c, rt), a.Chat); err != nil {
			return nil, err
		}
		// The other windows showing this conversation reload it.
		var id struct {
			ID string `json:"id"`
		}
		_ = json.Unmarshal(a.Chat, &id)
		s.emitter(c.project)("llm.saved", id, c.id)
		return nil, nil
	}))
	s.handle("llm.chats.delete", withProject(func(ctx context.Context, c *Client, rt *runtime.Runtime, p json.RawMessage) (any, error) {
		a, err := bind[idArg](p)
		if err != nil {
			return nil, err
		}
		return nil, s.LLM.DeleteChat(loc(c, rt), a.ID)
	}))
	s.handle("llm.chats.rename", withProject(func(ctx context.Context, c *Client, rt *runtime.Runtime, p json.RawMessage) (any, error) {
		a, err := bind[struct{ ID, Title string }](p)
		if err != nil {
			return nil, err
		}
		if err := s.LLM.RenameChat(loc(c, rt), a.ID, a.Title); err != nil {
			return nil, err
		}
		// The tickets linking this conversation show its new title.
		if root, err := s.kanbanProject(c.project); err == nil {
			s.Kanban.RenameChat(kanbanLoc(root), a.ID, a.Title)
		}
		return nil, nil
	}))
	s.handle("llm.context", withProject(func(ctx context.Context, c *Client, rt *runtime.Runtime, p json.RawMessage) (any, error) {
		return s.LLM.LoadContext(proj(rt)), nil
	}))
	s.handle("llm.skill.read", withProject(func(ctx context.Context, c *Client, rt *runtime.Runtime, p json.RawMessage) (any, error) {
		a, err := bind[struct{ Name string }](p)
		if err != nil {
			return nil, err
		}
		return s.LLM.ReadSkill(proj(rt), a.Name)
	}))
	s.handle("llm.skill.file", withProject(func(ctx context.Context, c *Client, rt *runtime.Runtime, p json.RawMessage) (any, error) {
		a, err := bind[struct{ Name, File string }](p)
		if err != nil {
			return nil, err
		}
		return s.LLM.ReadSkillFile(proj(rt), a.Name, a.File)
	}))
	s.handle("llm.prompt.save", withProject(func(ctx context.Context, c *Client, rt *runtime.Runtime, p json.RawMessage) (any, error) {
		a, err := bind[struct{ Scope, Kind, Content string }](p)
		if err != nil {
			return nil, err
		}
		if a.Scope == "global" {
			return nil, s.LLM.SaveGlobalPrompt(a.Kind, a.Content)
		}
		target := path.Join(rt.Root, llm.ProjectPromptFile(a.Kind))
		if strings.TrimSpace(a.Content) == "" {
			if _, err := rt.FS.Stat(target); err != nil {
				return nil, nil
			}
			return nil, rt.Delete(target)
		}
		_, err = rt.Write(target, a.Content, runtime.Format{}, "")
		return nil, err
	}))
}
