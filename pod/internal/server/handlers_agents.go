package server

import (
	"context"
	"encoding/base64"
	"encoding/json"

	"github.com/DrSmithFr/web-ide/pod/internal/acp"
	"github.com/DrSmithFr/web-ide/pod/internal/i18n"
	"github.com/DrSmithFr/web-ide/pod/internal/runtime"
)

// Agents tool: sessions of CLI agents through ACP (package acp), one per tab.
func (s *Server) registerAgents() {
	s.registerAgentStats()
	catalog := func() acp.Catalog { return acp.Catalog{Dir: s.Store.Path("acp")} }
	withAgents := func(f func(ctx context.Context, c *Client, rt *runtime.Runtime, m *acp.Manager, p json.RawMessage) (any, error)) handler {
		return func(ctx context.Context, c *Client, p json.RawMessage) (any, error) {
			rt, err := c.runtime()
			if err != nil {
				return nil, err
			}
			if rt.Agents == nil {
				return nil, i18n.New("agents run on local projects only")
			}
			return f(ctx, c, rt, rt.Agents, p)
		}
	}
	withSession := func(f func(s *acp.Session, p json.RawMessage) (any, error)) handler {
		return withAgents(func(ctx context.Context, c *Client, rt *runtime.Runtime, m *acp.Manager, p json.RawMessage) (any, error) {
			a, err := bind[struct{ ID string }](p)
			if err != nil {
				return nil, err
			}
			sess, err := m.Get(a.ID)
			if err != nil {
				return nil, err
			}
			return f(sess, p)
		})
	}

	// The agents of the + menu; local: false on an SSH project (no agent there yet).
	s.handle("agents.catalog", func(ctx context.Context, c *Client, p json.RawMessage) (any, error) {
		local := true
		if rt, err := c.runtime(); err == nil {
			local = rt.Agents != nil
		}
		return map[string]any{"agents": catalog().List(), "local": local}, nil
	})
	s.handle("agents.install", func(ctx context.Context, c *Client, p json.RawMessage) (any, error) {
		a, err := bind[struct{ Agent string }](p)
		if err != nil {
			return nil, err
		}
		if err := catalog().Install(ctx, a.Agent); err != nil {
			return nil, err
		}
		return catalog().List(), nil
	})
	s.handle("agents.list", withAgents(func(ctx context.Context, c *Client, rt *runtime.Runtime, m *acp.Manager, p json.RawMessage) (any, error) {
		return m.List(), nil
	}))
	s.handle("agents.create", withAgents(func(ctx context.Context, c *Client, rt *runtime.Runtime, m *acp.Manager, p json.RawMessage) (any, error) {
		a, err := bind[struct{ Agent string }](p)
		if err != nil {
			return nil, err
		}
		info, err := m.Create(a.Agent, "")
		if err == nil {
			s.emitter(c.project)("agents.created", info, c.id)
		}
		return info, err
	}))
	s.handle("agents.get", withSession(func(sess *acp.Session, p json.RawMessage) (any, error) {
		info, entries := sess.Thread()
		return map[string]any{"info": info, "entries": entries}, nil
	}))
	s.handle("agents.prompt", withSession(func(sess *acp.Session, p json.RawMessage) (any, error) {
		a, err := bind[struct {
			Text  string
			Parts []acp.Part
		}](p)
		if err != nil {
			return nil, err
		}
		return nil, sess.Prompt(a.Text, a.Parts...)
	}))
	// A queued prompt taken back: its text goes back to the message box.
	s.handle("agents.unqueue", withSession(func(sess *acp.Session, p json.RawMessage) (any, error) {
		a, err := bind[struct{ Index int }](p)
		if err != nil {
			return nil, err
		}
		text, err := sess.Unqueue(a.Index)
		return map[string]string{"text": text}, err
	}))
	// An image of a prompt, as a data URL.
	s.handle("agents.image", withSession(func(sess *acp.Session, p json.RawMessage) (any, error) {
		a, err := bind[struct{ File string }](p)
		if err != nil {
			return nil, err
		}
		data, mime, err := sess.Image(a.File)
		if err != nil {
			return nil, err
		}
		return "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(data), nil
	}))
	s.handle("agents.cancel", withSession(func(sess *acp.Session, p json.RawMessage) (any, error) {
		return nil, sess.Cancel()
	}))
	s.handle("agents.answer", withSession(func(sess *acp.Session, p json.RawMessage) (any, error) {
		a, err := bind[struct {
			Index  int
			Option string
		}](p)
		if err != nil {
			return nil, err
		}
		return nil, sess.Answer(a.Index, a.Option)
	}))
	s.handle("agents.config", withSession(func(sess *acp.Session, p json.RawMessage) (any, error) {
		a, err := bind[struct{ Config, Value string }](p)
		if err != nil {
			return nil, err
		}
		return nil, sess.SetConfig(a.Config, a.Value)
	}))
	s.handle("agents.restart", withSession(func(sess *acp.Session, p json.RawMessage) (any, error) {
		return nil, sess.Restart()
	}))
	s.handle("agents.close", withAgents(func(ctx context.Context, c *Client, rt *runtime.Runtime, m *acp.Manager, p json.RawMessage) (any, error) {
		a, err := bind[struct{ ID string }](p)
		if err != nil {
			return nil, err
		}
		return nil, m.Close(a.ID)
	}))
}
