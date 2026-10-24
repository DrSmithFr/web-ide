package server

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"antennae.space/ide/lib/i18n"
	"antennae.space/ide/pod/assistant"
	"antennae.space/ide/pod/plugins"
	"antennae.space/ide/pod/plugins/backend"
	"antennae.space/ide/pod/plugins/confine"
	"antennae.space/ide/pod/runtime"
)

// The backends of the external plugins (docs/design/plugins.md, *Confined backend*): one
// process per plugin and project, a part of its runtime, started through plugin-exec on the
// first call, stopped with the project or when it is idle. Its <id>.* methods and its assistant
// tools are routed to it; its calls to the core need their grant.

// backendLogCap caps the error output kept of a backend.
const backendLogCap = 1 << 20

// extPart is the backend of an external plugin in a project.
type extPart struct {
	s      *Server
	rt     *runtime.Runtime
	id     string
	mu     sync.Mutex
	proc   *backend.Process
	deaths []time.Time
}

func (e *extPart) Stop(bool) { e.stop() }
func (e *extPart) Idle()     { e.stop() }

func (e *extPart) stop() {
	e.mu.Lock()
	p := e.proc
	e.proc = nil
	e.mu.Unlock()
	if p != nil {
		p.Stop()
	}
}

// process is the running backend, started when there is none; one that died three times in a
// minute stays off until the project opens again.
func (e *extPart) process(ctx context.Context) (*backend.Process, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.proc != nil {
		select {
		case <-e.proc.Done():
			e.deaths = append(e.deaths, time.Now())
			e.proc = nil
		default:
			return e.proc, nil
		}
	}
	recent := 0
	for _, t := range e.deaths {
		if time.Since(t) < time.Minute {
			recent++
		}
	}
	if recent >= 3 {
		return nil, i18n.Errorf("the backend of %s ended three times in a minute: it is off until the project opens again", e.id)
	}
	p, err := e.s.startBackend(ctx, e.rt, e.id)
	if err != nil {
		return nil, err
	}
	e.proc = p
	return p, nil
}

// dropBackends stops the backends of a plugin in every open project (it is updated or removed).
func (s *Server) dropBackends(id string) {
	s.mu.Lock()
	rts := make([]*runtime.Runtime, 0, len(s.runtimes))
	for _, rt := range s.runtimes {
		if rt != nil {
			rts = append(rts, rt)
		}
	}
	s.mu.Unlock()
	for _, rt := range rts {
		rt.DropPart("ext:" + id)
	}
}

// backendOf is the backend part of an external plugin in the project of rt.
func (s *Server) backendOf(rt *runtime.Runtime, id string) *extPart {
	return rt.Part("ext:"+id, func() any { return &extPart{s: s, rt: rt, id: id} }).(*extPart)
}

// cappedLog keeps the first bytes a backend writes to its error output.
type cappedLog struct {
	f *os.File
	n int
}

func (l *cappedLog) Write(b []byte) (int, error) {
	if l.n < backendLogCap {
		k := min(len(b), backendLogCap-l.n)
		_, _ = l.f.Write(b[:k])
		l.n += k
	}
	return len(b), nil
}

// startBackend starts the backend of an external plugin confined for the project of rt.
func (s *Server) startBackend(ctx context.Context, rt *runtime.Runtime, id string) (*backend.Process, error) {
	e, ok := s.Plugins.External(id)
	if !ok || e.Manifest.Backend == "" {
		return nil, i18n.Errorf("the plugin %s has no backend", id)
	}
	if confine.ABI() < 1 {
		return nil, i18n.New("this kernel has no Landlock: an external plugin's backend cannot run confined")
	}
	exe, err := os.Executable()
	if err != nil {
		return nil, err
	}
	dir := filepath.Join(s.plugins.installer.Dir(id), e.Version)
	data := s.Store.Path("plugin-data", id)
	if err := os.MkdirAll(filepath.Join(data, "tmp"), 0o700); err != nil {
		return nil, err
	}
	argv := []string{exe, "plugin-exec", "-plugin", dir, "-data", data}
	write, read := slices.Contains(e.Grants, "files:write"), slices.Contains(e.Grants, "files:read")
	// The files of a project on this machine, as granted; an SSH project is reached through the
	// core calls only.
	if rt.Local && (read || write) {
		argv = append(argv, "-project", rt.Root)
		if write {
			argv = append(argv, "-write")
		}
	}
	if slices.Contains(e.Grants, "net") {
		argv = append(argv, "-net")
	}
	argv = append(argv, "--", filepath.Join(dir, filepath.FromSlash(e.Manifest.Backend)))
	logFile, err := os.OpenFile(filepath.Join(data, "backend.log"), os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, err
	}
	p, err := backend.Start(ctx, backend.Config{
		Argv: argv,
		Env:  []string{"PATH=/usr/local/bin:/usr/bin:/bin", "HOME=" + data, "TMPDIR=" + filepath.Join(data, "tmp"), "LANG=C.UTF-8"},
		Dir:  data,
		Log:  &cappedLog{f: logFile},
		Init: map[string]any{"apiVersion": plugins.APIVersion, "plugin": id, "version": e.Version, "grants": e.Grants,
			"project": map[string]any{"id": rt.P.ID, "root": rt.Root, "local": rt.Local}},
		Core: func(ctx context.Context, method string, params json.RawMessage) (any, error) {
			return s.backendCore(ctx, rt, e, method, params)
		},
		Emit: func(event string, data json.RawMessage) {
			// Its own events only, to the windows of the project.
			if strings.HasPrefix(event, id+".") {
				s.emitter(rt.P.ID)(event, data, "")
			}
		},
	})
	if err != nil {
		logFile.Close()
		return nil, i18n.Errorf("the backend of %s did not start: %v", id, err)
	}
	go func() {
		<-p.Done()
		logFile.Close()
	}()
	return p, nil
}

// backendCore runs a method of the core for a backend, with its grant, in the project of rt.
func (s *Server) backendCore(ctx context.Context, rt *runtime.Runtime, e plugins.External, method string, params json.RawMessage) (res any, err error) {
	if !slices.Contains(e.Grants, "core:"+method) {
		return nil, i18n.Errorf("%s is not granted to %s", method, e.Manifest.Name)
	}
	h, ok := s.handlers[method]
	if !ok {
		return nil, i18n.Errorf("unknown method: %s", method)
	}
	// A client without a window, in the project: what the method pushes goes nowhere.
	c := newClient(s, ctx)
	c.project = rt.P.ID
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("%s: internal error of the pod", method)
		}
	}()
	return h(ctx, c, params)
}

// externalHandler routes an <id>.* method to the backend of an external plugin ("" for none).
func (s *Server) externalHandler(method string) handler {
	id, _, ok := strings.Cut(method, ".")
	if !ok {
		return nil
	}
	e, ok := s.Plugins.External(id)
	if !ok || e.Manifest.Backend == "" {
		return nil
	}
	return withRuntime(func(ctx context.Context, c *Client, rt *runtime.Runtime, p json.RawMessage) (any, error) {
		if !s.pluginOn(rt, id) {
			return nil, i18n.Errorf("the %s plugin is disabled on this project", e.Manifest.Name)
		}
		proc, err := s.backendOf(rt, id).process(ctx)
		if err != nil {
			return nil, err
		}
		res, err := proc.Call(ctx, "call", map[string]any{"method": method, "params": p, "lang": c.language()})
		if err != nil {
			return nil, err
		}
		return res, nil
	})
}

// externalTools are the assistant tools of the external plugins with a backend, run by it.
func (s *Server) externalTools() []pluginTool {
	var out []pluginTool
	for _, e := range s.Plugins.Externals() {
		if e.Manifest.Backend == "" {
			continue
		}
		id := e.Manifest.ID
		for _, name := range e.Manifest.Contributes.AssistantTools {
			sc := e.Manifest.Contributes.ToolSchemas[name]
			name := name
			t := plugins.AssistantTool{Name: name, Description: sc.Description, Props: sc.Properties, Required: sc.Required,
				Run: func(ctx context.Context, call plugins.Call) (plugins.Result, error) {
					proc, err := s.backendOf(call.Runtime, id).process(ctx)
					if err != nil {
						return plugins.Result{}, err
					}
					raw, err := proc.Call(ctx, "tool", map[string]any{"name": name, "args": call.Params, "lang": call.Lang})
					if err != nil {
						return plugins.Result{}, err
					}
					var r struct {
						Content string `json:"content"`
						Summary string `json:"summary"`
						Error   bool   `json:"error"`
					}
					if err := json.Unmarshal(raw, &r); err != nil {
						return plugins.Result{}, i18n.Errorf("the backend of %s answered %s badly: %v", id, name, err)
					}
					return plugins.Result{Content: r.Content, Summary: assistant.Plain(r.Summary), Error: r.Error}, nil
				}}
			out = append(out, pluginTool{plugin: id, tool: t, def: assistant.Func(t.Name, t.Description, t.Props, t.Required...)})
		}
	}
	return out
}

// allPluginTools are the assistant tools of the built-ins, then of the external plugins.
func (s *Server) allPluginTools() []pluginTool {
	return append(append([]pluginTool{}, s.plugins.tools...), s.externalTools()...)
}
