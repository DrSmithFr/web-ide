// Package docker is the Docker plugin: the Compose stack of the project, the containers of the
// host, their logs, the disk usage, and the docker_ps / docker_logs tools of the assistant. The
// docker command itself is run by pod/docker, through the runtime of the project.
package docker

import (
	"context"
	_ "embed"
	"encoding/json"
	"strconv"
	"sync"
	"sync/atomic"

	"antennae.space/ide/lib/i18n"
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

func bind[T any](raw json.RawMessage) (T, error) {
	var v T
	if len(raw) == 0 || string(raw) == "null" {
		return v, nil
	}
	err := json.Unmarshal(raw, &v)
	return v, err
}

// handle registers a method whose parameters are bound to A.
func handle[A any](h plugins.Host, method string, f func(ctx context.Context, c plugins.Call, a A) (any, error)) {
	h.Handle(method, func(ctx context.Context, c plugins.Call) (any, error) {
		a, err := bind[A](c.Params)
		if err != nil {
			return nil, err
		}
		return f(ctx, c, a)
	})
}

type none struct{}

var streamSeq atomic.Int64

func (p *Plugin) Register(h plugins.Host) {
	type profilesArg struct {
		Profiles []string `json:"profiles"`
	}
	type idArg struct {
		ID string `json:"id"`
	}
	handle(h, "status", func(ctx context.Context, c plugins.Call, _ none) (any, error) {
		st := dockerOf(c.Runtime).Status(ctx)
		if st.Err != nil {
			st.Error = i18n.Translate(c.Lang, st.Err)
		}
		return st, nil
	})
	handle(h, "stack", func(ctx context.Context, c plugins.Call, a profilesArg) (any, error) {
		return dockerOf(c.Runtime).Stack(ctx, a.Profiles)
	})
	handle(h, "containers", func(ctx context.Context, c plugins.Call, _ none) (any, error) {
		return dockerOf(c.Runtime).Containers(ctx)
	})
	handle(h, "inspect", func(ctx context.Context, c plugins.Call, a idArg) (any, error) {
		return dockerOf(c.Runtime).Inspect(ctx, a.ID)
	})
	handle(h, "stats", func(ctx context.Context, c plugins.Call, a struct {
		IDs []string `json:"ids"`
	}) (any, error) {
		return dockerOf(c.Runtime).Stats(ctx, a.IDs)
	})
	handle(h, "compose", func(ctx context.Context, c plugins.Call, a struct {
		Profiles []string `json:"profiles"`
		Action   string   `json:"action"`
		Service  string   `json:"service"`
	}) (any, error) {
		return nil, dockerOf(c.Runtime).Compose(ctx, a.Profiles, a.Action, a.Service)
	})
	handle(h, "container", func(ctx context.Context, c plugins.Call, a struct {
		Action string `json:"action"`
		ID     string `json:"id"`
	}) (any, error) {
		return nil, dockerOf(c.Runtime).Container(ctx, a.Action, a.ID)
	})

	// logs follows the logs of a container (id) or of the stack (no id) and pushes docker.log
	// events {stream, lines} to the window, then docker.logEnd when docker stops.
	handle(h, "logs", func(ctx context.Context, c plugins.Call, a struct {
		ID       string   `json:"id"`
		Profiles []string `json:"profiles"`
		Tail     int      `json:"tail"`
	}) (any, error) {
		if a.Tail <= 0 {
			a.Tail = 500
		}
		proc, err := dockerOf(c.Runtime).Logs(a.ID, a.Profiles, a.Tail)
		if err != nil {
			return nil, err
		}
		id := "l" + strconv.FormatInt(streamSeq.Add(1), 10)
		var once sync.Once
		stop := func() { once.Do(func() { _ = proc.Kill() }) }
		c.Window.Hold(id, stop)
		go pumpLogs(c.Window, id, proc, stop)
		return map[string]string{"stream": id}, nil
	})
	handle(h, "logsStop", func(ctx context.Context, c plugins.Call, a struct {
		Stream string `json:"stream"`
	}) (any, error) {
		c.Window.Release(a.Stream)
		return nil, nil
	})
	// logsTail gives the last lines of a container or a service.
	handle(h, "logsTail", func(ctx context.Context, c plugins.Call, a struct {
		ID       string   `json:"id"`
		Service  string   `json:"service"`
		Profiles []string `json:"profiles"`
		Lines    int      `json:"lines"`
	}) (any, error) {
		if a.ID == "" && a.Service == "" {
			return nil, i18n.New("a container or a service is needed")
		}
		out, err := dockerOf(c.Runtime).LogsTail(ctx, a.ID, a.Service, a.Profiles, min(max(a.Lines, 1), 2000))
		return map[string]string{"output": out}, err
	})
	handle(h, "disk", func(ctx context.Context, c plugins.Call, _ none) (any, error) {
		return dockerOf(c.Runtime).Disk(ctx)
	})
	handle(h, "prune", func(ctx context.Context, c plugins.Call, a struct {
		What string `json:"what"`
	}) (any, error) {
		freed, err := dockerOf(c.Runtime).Prune(ctx, a.What)
		return map[string]string{"reclaimed": freed}, err
	})
	handle(h, "remove", func(ctx context.Context, c plugins.Call, a struct {
		Kind string `json:"kind"`
		ID   string `json:"id"`
	}) (any, error) {
		return nil, dockerOf(c.Runtime).RemoveObject(ctx, a.Kind, a.ID)
	})
	registerTools(h)
}
