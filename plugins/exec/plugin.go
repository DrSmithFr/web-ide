// Package exec is the Exec plugin: the consoles of a project (terminals and tasks) and its saved
// commands. It provides the terminal capability (the consoles other plugins open) and commands.
// The processes themselves are held by the console manager of the runtime and the keeper, so
// they outlive the windows and the pod.
package exec

import (
	"context"
	_ "embed"
	"encoding/base64"
	"encoding/json"

	"antennae.space/ide/lib/i18n"
	"antennae.space/ide/pod/commands"
	"antennae.space/ide/pod/plugins"
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

// handle registers a method whose parameters are bound to A.
func handle[A any](h plugins.Host, method string, f func(ctx context.Context, c plugins.Call, a A) (any, error)) {
	h.Handle(method, func(ctx context.Context, c plugins.Call) (any, error) {
		var a A
		if len(c.Params) > 0 && string(c.Params) != "null" {
			if err := json.Unmarshal(c.Params, &a); err != nil {
				return nil, err
			}
		}
		return f(ctx, c, a)
	})
}

type none struct{}

func (p *Plugin) Register(h plugins.Host) {
	handle(h, "console.list", func(ctx context.Context, c plugins.Call, _ none) (any, error) {
		return c.Runtime.Consoles.List(), nil
	})
	handle(h, "console.create", func(ctx context.Context, c plugins.Call, a struct {
		Kind, Title, Cwd string
		Command          []string
		Cols, Rows       int
	}) (any, error) {
		info, err := c.Runtime.Consoles.Create(a.Kind, a.Title, a.Command, a.Cwd, a.Cols, a.Rows)
		if err == nil {
			c.EmitOthers("console.created", info)
		}
		return info, err
	})
	handle(h, "console.attach", func(ctx context.Context, c plugins.Call, a struct {
		ID string
		// From: the offset the page already has (a reconnection): only what follows.
		From *int64
	}) (any, error) {
		info, buf, end, err := c.Runtime.Consoles.Snapshot(a.ID)
		if err != nil {
			return nil, err
		}
		start := end - int64(len(buf))
		if a.From != nil && *a.From >= start && *a.From <= end {
			buf, start = buf[*a.From-start:], *a.From
		}
		// The output events that follow start at offset; the data starts at start.
		return map[string]any{"info": info, "data": base64.StdEncoding.EncodeToString(buf), "start": start, "offset": end}, nil
	})
	handle(h, "console.input", func(ctx context.Context, c plugins.Call, a struct{ ID, Data string }) (any, error) {
		return nil, c.Runtime.Consoles.Input(a.ID, []byte(a.Data))
	})
	handle(h, "console.resize", func(ctx context.Context, c plugins.Call, a struct {
		ID         string
		Cols, Rows int
	}) (any, error) {
		return nil, c.Runtime.Consoles.Resize(a.ID, a.Cols, a.Rows)
	})
	handle(h, "console.rename", func(ctx context.Context, c plugins.Call, a struct{ ID, Title string }) (any, error) {
		err := c.Runtime.Consoles.Rename(a.ID, a.Title)
		if err == nil {
			c.Emit("console.renamed", map[string]string{"id": a.ID, "title": a.Title})
		}
		return nil, err
	})
	handle(h, "console.close", func(ctx context.Context, c plugins.Call, a struct{ ID string }) (any, error) {
		err := c.Runtime.Consoles.Close(a.ID)
		if err == nil {
			c.Emit("console.closed", map[string]string{"id": a.ID})
		}
		return nil, err
	})

	// The saved commands, kept in the .ide of the main project; an invalid file is reported
	// (shown) and left as it is.
	handle(h, "commands.list", func(ctx context.Context, c plugins.Call, _ none) (any, error) {
		list, err := commands.Load(c.Runtime.FS, c.Runtime.IdeDir)
		if err != nil {
			return map[string]any{"commands": []commands.Command{}, "error": i18n.Translate(c.Lang, err)}, nil
		}
		return map[string]any{"commands": list}, nil
	})
	handle(h, "commands.save", func(ctx context.Context, c plugins.Call, a struct{ Commands []commands.Command }) (any, error) {
		list, err := commands.Save(c.Runtime.FS, c.Runtime.IdeDir, a.Commands)
		if err != nil {
			return nil, err
		}
		c.EmitFamily("commands.changed", map[string]any{"commands": list})
		return list, nil
	})
	handle(h, "commands.run", func(ctx context.Context, c plugins.Call, a struct {
		ID      string
		Restart bool
	}) (any, error) {
		list, err := commands.Load(c.Runtime.FS, c.Runtime.IdeDir)
		if err != nil {
			return nil, err
		}
		cmd, ok := commands.Find(list, a.ID)
		if !ok {
			return nil, i18n.Errorf("command not found: %s", a.ID)
		}
		// The window that asked adds the console itself.
		var emit commands.Emit = func(event string, data any, except string) {
			if except != "" {
				c.EmitOthers(event, data)
			} else {
				c.Emit(event, data)
			}
		}
		return commands.RunSaved(c.Runtime.Consoles, c.Runtime.Root, cmd, a.Restart, emit, "self")
	})
}
