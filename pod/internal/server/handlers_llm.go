package server

import (
	"context"
	"encoding/json"
	"path"
	"path/filepath"
	"strings"

	"webide/pod/internal/llm"
	"webide/pod/internal/runtime"
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
	// llm.chat streams llm.delta events to the asking window only; stream is an id
	// chosen by the page to match them.
	s.handle("llm.chat", func(ctx context.Context, c *Client, p json.RawMessage) (any, error) {
		a, err := bind[struct {
			llm.ChatRequest
			Stream string `json:"stream"`
		}](p)
		if err != nil {
			return nil, err
		}
		return s.LLM.Chat(ctx, a.ChatRequest, func(d llm.Delta) {
			c.push("llm.delta", map[string]any{"stream": a.Stream, "content": d.Content, "reasoning": d.Reasoning, "tool": d.Tool})
		})
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
		return nil, s.LLM.SaveChat(loc(c, rt), a.Chat)
	}))
	s.handle("llm.chats.delete", withProject(func(ctx context.Context, c *Client, rt *runtime.Runtime, p json.RawMessage) (any, error) {
		a, err := bind[idArg](p)
		if err != nil {
			return nil, err
		}
		return nil, s.LLM.DeleteChat(loc(c, rt), a.ID)
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
		a, err := bind[struct{ Scope, Content string }](p)
		if err != nil {
			return nil, err
		}
		if a.Scope == "global" {
			return nil, s.LLM.SaveGlobalPrompt(a.Content)
		}
		target := path.Join(rt.Root, llm.ProjectPromptFile)
		if strings.TrimSpace(a.Content) == "" {
			if _, err := rt.FS.Stat(target); err != nil {
				return nil, nil
			}
			return nil, rt.Delete(target)
		}
		_, err = rt.Write(target, a.Content, "")
		return nil, err
	}))
}
