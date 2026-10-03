package server

import (
	"context"
	"encoding/json"
	"errors"

	"webide/pod/internal/llm"
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

	project := func(c *Client) (string, error) {
		if c.project == "" {
			return "", errors.New("aucun projet ouvert")
		}
		return c.project, nil
	}
	s.handle("llm.chats.list", func(ctx context.Context, c *Client, p json.RawMessage) (any, error) {
		pid, err := project(c)
		if err != nil {
			return nil, err
		}
		return s.LLM.ListChats(pid)
	})
	s.handle("llm.chats.get", func(ctx context.Context, c *Client, p json.RawMessage) (any, error) {
		a, err := bind[struct{ ID string }](p)
		if err != nil {
			return nil, err
		}
		pid, err := project(c)
		if err != nil {
			return nil, err
		}
		return s.LLM.GetChat(pid, a.ID)
	})
	s.handle("llm.chats.save", func(ctx context.Context, c *Client, p json.RawMessage) (any, error) {
		a, err := bind[struct {
			Chat json.RawMessage `json:"chat"`
		}](p)
		if err != nil {
			return nil, err
		}
		pid, err := project(c)
		if err != nil {
			return nil, err
		}
		return nil, s.LLM.SaveChat(pid, a.Chat)
	})
	s.handle("llm.chats.delete", func(ctx context.Context, c *Client, p json.RawMessage) (any, error) {
		a, err := bind[struct{ ID string }](p)
		if err != nil {
			return nil, err
		}
		pid, err := project(c)
		if err != nil {
			return nil, err
		}
		return nil, s.LLM.DeleteChat(pid, a.ID)
	})
}
