// Package database is the Database explorer plugin: the connections of the project (SQLite,
// PostgreSQL, Redis), their tree, the table view and the SQL consoles. The connections
// themselves are held by pod/internal/db in the runtime of the project, which closes them.
package database

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"

	"github.com/DrSmithFr/web-ide/pod/internal/db"
	"github.com/DrSmithFr/web-ide/pod/internal/i18n"
	"github.com/DrSmithFr/web-ide/pod/internal/plugins"
)

//go:embed plugin.json
var manifest []byte

type Plugin struct{ m plugins.Manifest }

func New() *Plugin {
	m, err := plugins.Parse(manifest)
	if err != nil {
		panic(err)
	}
	return &Plugin{m: m}
}

func (p *Plugin) Manifest() plugins.Manifest { return p.m }

// handle registers a method whose parameters are bound to A, run on the connections of the
// project; a connection needing its password answers db_password, so the page asks it.
func handle[A any](h plugins.Host, method string, f func(ctx context.Context, c plugins.Call, m *db.Manager, a A) (any, error)) {
	h.Handle(method, func(ctx context.Context, c plugins.Call) (any, error) {
		var a A
		if len(c.Params) > 0 && string(c.Params) != "null" {
			if err := json.Unmarshal(c.Params, &a); err != nil {
				return nil, err
			}
		}
		res, err := f(ctx, c, c.Runtime.DB, a)
		var np *db.NeedPassword
		if errors.As(err, &np) {
			msg := i18n.Translate(c.Lang, np.Msg)
			return res, &plugins.CodedError{Code: "db_password", Data: map[string]string{"kind": "dbpassword", "prompt": msg}, Err: np.Msg}
		}
		return res, err
	})
}

type none struct{}

func (p *Plugin) Register(h plugins.Host) {
	notify := func(c plugins.Call) { c.Emit("db.changed", c.Runtime.DB.List()) }
	type connArg struct {
		ID     string    `json:"id"`
		Secret db.Secret `json:"secret"`
	}
	type configArg struct {
		Config db.ConnConfig `json:"config"`
		Secret db.Secret     `json:"secret"`
	}
	handle(h, "list", func(ctx context.Context, c plugins.Call, m *db.Manager, _ none) (any, error) {
		return m.List(), nil
	})
	handle(h, "save", func(ctx context.Context, c plugins.Call, m *db.Manager, a configArg) (any, error) {
		v, err := m.Save(a.Config, a.Secret)
		if err == nil {
			notify(c)
		}
		return v, err
	})
	handle(h, "delete", func(ctx context.Context, c plugins.Call, m *db.Manager, a connArg) (any, error) {
		err := m.Delete(a.ID)
		if err == nil {
			notify(c)
		}
		return nil, err
	})
	handle(h, "test", func(ctx context.Context, c plugins.Call, m *db.Manager, a configArg) (any, error) {
		ms, err := m.Test(ctx, a.Config, a.Secret)
		if err != nil {
			return nil, err
		}
		return i18n.T(c.Lang, "Connection successful (%d ms)", ms), nil
	})
	handle(h, "connect", func(ctx context.Context, c plugins.Call, m *db.Manager, a connArg) (any, error) {
		err := m.Connect(ctx, a.ID, a.Secret)
		notify(c)
		return nil, err
	})
	handle(h, "disconnect", func(ctx context.Context, c plugins.Call, m *db.Manager, a connArg) (any, error) {
		m.Disconnect(a.ID)
		notify(c)
		return nil, nil
	})
	handle(h, "children", func(ctx context.Context, c plugins.Call, m *db.Manager, a struct {
		ID   string  `json:"id"`
		Node db.Node `json:"node"`
	}) (any, error) {
		nodes, err := m.Children(ctx, a.ID, a.Node)
		if a.Node.Kind == "" {
			notify(c)
		}
		return nodes, err
	})
	type objArg struct {
		ID    string `json:"id"`
		DB    string `json:"db"`
		Table string `json:"table"`
		Index string `json:"index"`
	}
	handle(h, "ddl", func(ctx context.Context, c plugins.Call, m *db.Manager, a objArg) (any, error) {
		return m.DDL(ctx, a.ID, a.DB, a.Table)
	})
	handle(h, "indexDef", func(ctx context.Context, c plugins.Call, m *db.Manager, a objArg) (any, error) {
		return m.IndexDef(ctx, a.ID, a.DB, a.Table, a.Index)
	})
	handle(h, "page", func(ctx context.Context, c plugins.Call, m *db.Manager, a struct {
		ID      string `json:"id"`
		DB      string `json:"db"`
		Table   string `json:"table"`
		Offset  int    `json:"offset"`
		Limit   int    `json:"limit"`
		Refresh bool   `json:"refresh"`
	}) (any, error) {
		if a.Limit <= 0 || a.Limit > 1000 {
			a.Limit = 100
		}
		return m.Page(ctx, a.ID, a.DB, a.Table, a.Offset, a.Limit, a.Refresh)
	})

	type consoleArg struct {
		ID     string `json:"id"`
		ConnID string `json:"connId"`
		DB     string `json:"db"`
		Query  string `json:"query"`
		On     bool   `json:"on"`
	}
	handle(h, "consoleOpen", func(ctx context.Context, c plugins.Call, m *db.Manager, a consoleArg) (any, error) {
		return m.ConsoleOpen(a.ID, a.ConnID, a.DB), nil
	})
	handle(h, "exec", func(ctx context.Context, c plugins.Call, m *db.Manager, a consoleArg) (any, error) {
		// The statement must survive the request context: cancelling goes through db.cancel.
		res, st, err := m.Exec(context.WithoutCancel(ctx), a.ID, a.Query)
		c.Emit("db.consoleState", st)
		return map[string]any{"result": res, "state": st}, err
	})
	handle(h, "cancel", func(ctx context.Context, c plugins.Call, m *db.Manager, a consoleArg) (any, error) {
		return nil, m.Cancel(a.ID)
	})
	handle(h, "autocommit", func(ctx context.Context, c plugins.Call, m *db.Manager, a consoleArg) (any, error) {
		st, err := m.SetAutoCommit(ctx, a.ID, a.On)
		c.Emit("db.consoleState", st)
		return st, err
	})
	endTx := func(commit bool) func(ctx context.Context, c plugins.Call, m *db.Manager, a consoleArg) (any, error) {
		return func(ctx context.Context, c plugins.Call, m *db.Manager, a consoleArg) (any, error) {
			st, err := m.EndTx(ctx, a.ID, commit)
			c.Emit("db.consoleState", st)
			return st, err
		}
	}
	handle(h, "commit", endTx(true))
	handle(h, "rollback", endTx(false))
	handle(h, "consoleClose", func(ctx context.Context, c plugins.Call, m *db.Manager, a consoleArg) (any, error) {
		m.ConsoleClose(a.ID)
		return nil, nil
	})
	handle(h, "history", func(ctx context.Context, c plugins.Call, m *db.Manager, a consoleArg) (any, error) {
		return m.History(a.ConnID), nil
	})
}
