package server

import (
	"context"
	"encoding/json"

	"webide/pod/internal/db"
)

func (s *Server) registerDB() {
	h := func(f func(ctx context.Context, c *Client, m *db.Manager, p json.RawMessage) (any, error)) handler {
		return func(ctx context.Context, c *Client, p json.RawMessage) (any, error) {
			rt, err := c.runtime()
			if err != nil {
				return nil, err
			}
			return f(ctx, c, rt.DB, p)
		}
	}
	notify := func(c *Client) {
		if rt, err := c.runtime(); err == nil {
			s.emitter(c.project)("db.changed", rt.DB.List(), "")
		}
	}
	type connArg struct {
		ID     string    `json:"id"`
		Secret db.Secret `json:"secret"`
	}

	s.handle("db.list", h(func(ctx context.Context, c *Client, m *db.Manager, p json.RawMessage) (any, error) {
		return m.List(), nil
	}))
	s.handle("db.save", h(func(ctx context.Context, c *Client, m *db.Manager, p json.RawMessage) (any, error) {
		a, err := bind[struct {
			Config db.ConnConfig `json:"config"`
			Secret db.Secret     `json:"secret"`
		}](p)
		if err != nil {
			return nil, err
		}
		v, err := m.Save(a.Config, a.Secret)
		if err == nil {
			notify(c)
		}
		return v, err
	}))
	s.handle("db.delete", h(func(ctx context.Context, c *Client, m *db.Manager, p json.RawMessage) (any, error) {
		a, err := bind[connArg](p)
		if err != nil {
			return nil, err
		}
		err = m.Delete(a.ID)
		if err == nil {
			notify(c)
		}
		return nil, err
	}))
	s.handle("db.test", h(func(ctx context.Context, c *Client, m *db.Manager, p json.RawMessage) (any, error) {
		a, err := bind[struct {
			Config db.ConnConfig `json:"config"`
			Secret db.Secret     `json:"secret"`
		}](p)
		if err != nil {
			return nil, err
		}
		return m.Test(ctx, a.Config, a.Secret)
	}))
	s.handle("db.connect", h(func(ctx context.Context, c *Client, m *db.Manager, p json.RawMessage) (any, error) {
		a, err := bind[connArg](p)
		if err != nil {
			return nil, err
		}
		err = m.Connect(ctx, a.ID, a.Secret)
		notify(c)
		return nil, err
	}))
	s.handle("db.disconnect", h(func(ctx context.Context, c *Client, m *db.Manager, p json.RawMessage) (any, error) {
		a, err := bind[connArg](p)
		if err != nil {
			return nil, err
		}
		m.Disconnect(a.ID)
		notify(c)
		return nil, nil
	}))
	s.handle("db.children", h(func(ctx context.Context, c *Client, m *db.Manager, p json.RawMessage) (any, error) {
		a, err := bind[struct {
			ID   string  `json:"id"`
			Node db.Node `json:"node"`
		}](p)
		if err != nil {
			return nil, err
		}
		nodes, err := m.Children(ctx, a.ID, a.Node)
		if a.Node.Kind == "" {
			notify(c)
		}
		return nodes, err
	}))
	type objArg struct {
		ID    string `json:"id"`
		DB    string `json:"db"`
		Table string `json:"table"`
		Index string `json:"index"`
	}
	s.handle("db.ddl", h(func(ctx context.Context, c *Client, m *db.Manager, p json.RawMessage) (any, error) {
		a, err := bind[objArg](p)
		if err != nil {
			return nil, err
		}
		return m.DDL(ctx, a.ID, a.DB, a.Table)
	}))
	s.handle("db.indexDef", h(func(ctx context.Context, c *Client, m *db.Manager, p json.RawMessage) (any, error) {
		a, err := bind[objArg](p)
		if err != nil {
			return nil, err
		}
		return m.IndexDef(ctx, a.ID, a.DB, a.Table, a.Index)
	}))
	s.handle("db.page", h(func(ctx context.Context, c *Client, m *db.Manager, p json.RawMessage) (any, error) {
		a, err := bind[struct {
			ID      string `json:"id"`
			DB      string `json:"db"`
			Table   string `json:"table"`
			Offset  int    `json:"offset"`
			Limit   int    `json:"limit"`
			Refresh bool   `json:"refresh"`
		}](p)
		if err != nil {
			return nil, err
		}
		if a.Limit <= 0 || a.Limit > 1000 {
			a.Limit = 100
		}
		return m.Page(ctx, a.ID, a.DB, a.Table, a.Offset, a.Limit, a.Refresh)
	}))

	type consoleArg struct {
		ID     string `json:"id"`
		ConnID string `json:"connId"`
		DB     string `json:"db"`
		Query  string `json:"query"`
		On     bool   `json:"on"`
	}
	s.handle("db.consoleOpen", h(func(ctx context.Context, c *Client, m *db.Manager, p json.RawMessage) (any, error) {
		a, err := bind[consoleArg](p)
		if err != nil {
			return nil, err
		}
		return m.ConsoleOpen(a.ID, a.ConnID, a.DB), nil
	}))
	s.handle("db.exec", h(func(ctx context.Context, c *Client, m *db.Manager, p json.RawMessage) (any, error) {
		a, err := bind[consoleArg](p)
		if err != nil {
			return nil, err
		}
		// The statement must survive the request context: cancelling goes through db.cancel.
		res, st, err := m.Exec(context.WithoutCancel(ctx), a.ID, a.Query)
		s.emitter(c.project)("db.consoleState", st, "")
		return map[string]any{"result": res, "state": st}, err
	}))
	s.handle("db.cancel", h(func(ctx context.Context, c *Client, m *db.Manager, p json.RawMessage) (any, error) {
		a, err := bind[consoleArg](p)
		if err != nil {
			return nil, err
		}
		return nil, m.Cancel(a.ID)
	}))
	s.handle("db.autocommit", h(func(ctx context.Context, c *Client, m *db.Manager, p json.RawMessage) (any, error) {
		a, err := bind[consoleArg](p)
		if err != nil {
			return nil, err
		}
		st, err := m.SetAutoCommit(ctx, a.ID, a.On)
		s.emitter(c.project)("db.consoleState", st, "")
		return st, err
	}))
	endTx := func(commit bool) handler {
		return h(func(ctx context.Context, c *Client, m *db.Manager, p json.RawMessage) (any, error) {
			a, err := bind[consoleArg](p)
			if err != nil {
				return nil, err
			}
			st, err := m.EndTx(ctx, a.ID, commit)
			s.emitter(c.project)("db.consoleState", st, "")
			return st, err
		})
	}
	s.handle("db.commit", endTx(true))
	s.handle("db.rollback", endTx(false))
	s.handle("db.consoleClose", h(func(ctx context.Context, c *Client, m *db.Manager, p json.RawMessage) (any, error) {
		a, err := bind[consoleArg](p)
		if err != nil {
			return nil, err
		}
		m.ConsoleClose(a.ID)
		return nil, nil
	}))
	s.handle("db.history", h(func(ctx context.Context, c *Client, m *db.Manager, p json.RawMessage) (any, error) {
		a, err := bind[consoleArg](p)
		if err != nil {
			return nil, err
		}
		return m.History(a.ConnID), nil
	}))
}
