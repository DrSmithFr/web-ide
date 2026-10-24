// Package previews is the plugin of the port forwards (local ports reaching an address seen from
// the SSH host of a project) and of the app previews (a web app of a project served on a
// temporary URL). Their managers (internal/forwards, internal/preview) are the plugin's, one per
// pod: they outlive the windows, close when the pod stops, and the server hands the plugin the
// previews asked through the tunnel (Host.HandlePreview). The methods keep their names,
// tunnels.* and preview.*; the previews capability (caps.Previews) serves the share_preview and
// list_previews tools of the assistant and the MCP.
package previews

import (
	"context"
	"crypto/rand"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"time"

	"antennae.space/ide/lib/i18n"
	"antennae.space/ide/plugins/previews/internal/forwards"
	"antennae.space/ide/plugins/previews/internal/preview"
	"antennae.space/ide/pod/plugins"
	"antennae.space/ide/pod/runtime"
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

// ForwardIdle is how long the port forwards stay open once the last window of the pod has left.
var ForwardIdle = 5 * time.Minute

func (p *Plugin) Register(h plugins.Host) {
	pod := h.Pod()
	fw := forwards.New(func() { pod.Broadcast("tunnels.changed", nil) })
	var pv *preview.Manager
	pv = preview.New(&preview.CLI{}, pod.Authorized, func() { pod.Broadcast("preview.changed", pv.List()) })
	h.HandlePreview(pv.Handler)
	h.OnStop(func() {
		fw.CloseAll()
		pv.Shutdown()
	})
	h.Provide("previews", previewsCap{pod: pod, pv: pv})
	// The enabled port forwards of an SSH project open with it.
	h.OnProjectOpen(func(rt *runtime.Runtime) {
		if rt.Local {
			return
		}
		for _, sp := range specsOf(rt) {
			if sp.Enabled && !fw.IsOpen(rt.P.ID, sp.ID) {
				_ = fw.Open(rt.P.ID, sp, rt.Dial) // the error shows in the state of the port forward
			}
		}
	})
	// Every port forward closes a while after the last window of the pod has left.
	var idle *time.Timer
	h.OnWindows(func(n int) {
		if idle != nil {
			idle.Stop()
			idle = nil
		}
		if n == 0 {
			idle = time.AfterFunc(ForwardIdle, fw.CloseAll)
		}
	})
	state := func(rt *runtime.Runtime) []forwards.State {
		if rt.Local {
			return []forwards.State{}
		}
		return fw.State(rt.P.ID, specsOf(rt))
	}
	h.Handle("tunnels.get", func(ctx context.Context, c plugins.Call) (any, error) {
		return state(c.Runtime), nil
	})
	// tunnels.save adds a port forward (no id) or replaces one, then opens or closes it.
	h.Handle("tunnels.save", func(ctx context.Context, c plugins.Call) (any, error) {
		rt := c.Runtime
		sp, err := bind[forwards.Spec](c.Params)
		if err != nil {
			return nil, err
		}
		if rt.Local {
			return nil, i18n.New("port forwards are for SSH projects")
		}
		if err := sp.Check(); err != nil {
			return nil, err
		}
		list := specsOf(rt)
		found := false
		for i := range list {
			if list[i].ID == sp.ID && sp.ID != "" {
				list[i], found = sp, true
			}
		}
		if !found {
			if sp.ID != "" {
				return nil, i18n.New("port forward not found")
			}
			b := make([]byte, 4)
			_, _ = rand.Read(b)
			sp.ID = hex.EncodeToString(b)
			list = append(list, sp)
		}
		if err := saveSpecs(rt, list); err != nil {
			return nil, err
		}
		if sp.Enabled {
			err = fw.Open(rt.P.ID, sp, rt.Dial)
		} else {
			fw.Close(rt.P.ID, sp.ID)
		}
		pod.Broadcast("tunnels.changed", nil)
		return state(rt), err
	})
	h.Handle("tunnels.delete", func(ctx context.Context, c plugins.Call) (any, error) {
		rt := c.Runtime
		a, err := bind[struct {
			ID string `json:"id"`
		}](c.Params)
		if err != nil {
			return nil, err
		}
		list := specsOf(rt)
		kept := list[:0]
		for _, sp := range list {
			if sp.ID != a.ID {
				kept = append(kept, sp)
			}
		}
		if err := saveSpecs(rt, kept); err != nil {
			return nil, err
		}
		fw.Close(rt.P.ID, a.ID)
		pod.Broadcast("tunnels.changed", nil)
		return state(rt), nil
	})
	// The home page lists the open port forwards of every project and closes them (they stay
	// enabled: the project opens them again).
	h.HandleGlobal("tunnels.all", func(ctx context.Context, c plugins.Call) (any, error) {
		type view struct {
			forwards.State
			Name string `json:"name"`
			Host string `json:"host"`
		}
		out := []view{}
		for _, st := range fw.List() {
			v := view{State: st, Name: st.Project}
			if name, host, ok := pod.Project(st.Project); ok {
				v.Name, v.Host = name, host
			}
			out = append(out, v)
		}
		return out, nil
	})
	h.HandleGlobal("tunnels.close", func(ctx context.Context, c plugins.Call) (any, error) {
		a, err := bind[struct {
			Project string `json:"project"`
			ID      string `json:"id"`
		}](c.Params)
		if err != nil {
			return nil, err
		}
		fw.Close(a.Project, a.ID)
		return nil, nil
	})
	h.HandleGlobal("tunnels.closeAll", func(ctx context.Context, c plugins.Call) (any, error) {
		fw.CloseAll()
		return nil, nil
	})

	// The previews: global methods, a preview names its project (the window's by default).
	h.HandleGlobal("preview.open", func(ctx context.Context, c plugins.Call) (any, error) {
		sp, err := bind[preview.Spec](c.Params)
		if err != nil {
			return nil, err
		}
		if sp.Project == "" && c.Window != nil {
			sp.Project = c.Window.Project()
		}
		return open(ctx, pod, pv, sp)
	})
	h.HandleGlobal("preview.list", func(ctx context.Context, c plugins.Call) (any, error) {
		return pv.List(), nil
	})
	h.HandleGlobal("preview.public", func(ctx context.Context, c plugins.Call) (any, error) {
		a, err := bind[struct {
			ID     string `json:"id"`
			Public bool   `json:"public"`
		}](c.Params)
		if err != nil {
			return nil, err
		}
		return pv.SetPublic(a.ID, a.Public)
	})
	// preview.close stops the preview and the command it started.
	h.HandleGlobal("preview.close", func(ctx context.Context, c plugins.Call) (any, error) {
		a, err := bind[struct {
			ID string `json:"id"`
		}](c.Params)
		if err != nil {
			return nil, err
		}
		var st *preview.State
		for _, x := range pv.List() {
			if x.ID == a.ID {
				st = &x
			}
		}
		if st == nil {
			return nil, nil
		}
		pv.Close(a.ID)
		if rt := pod.OpenRuntime(st.Project); rt != nil && st.Console != "" && rt.Consoles.Close(st.Console) == nil {
			pod.Emit(st.Project, "console.closed", map[string]string{"id": st.Console})
		}
		return nil, nil
	})
}
