// Package previews is the plugin of the port forwards (local ports reaching an address seen from
// the SSH host of a project) and of the app previews (a web app of a project served on a
// temporary URL). Their managers are the pod's: they outlive the windows, and the HTTP server
// serves the previews; the plugin holds their methods, named tunnels.* and preview.* as before.
package previews

import (
	"context"
	"crypto/rand"
	_ "embed"
	"encoding/hex"
	"encoding/json"

	"github.com/DrSmithFr/web-ide/pod/internal/forwards"
	"github.com/DrSmithFr/web-ide/pod/internal/i18n"
	"github.com/DrSmithFr/web-ide/pod/internal/plugins"
	"github.com/DrSmithFr/web-ide/pod/internal/preview"
	"github.com/DrSmithFr/web-ide/pod/internal/runtime"
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

func (p *Plugin) Register(h plugins.Host) {
	pod := h.Pod()
	fw := pod.Forwards()
	state := func(rt *runtime.Runtime) []forwards.State {
		if rt.Local {
			return []forwards.State{}
		}
		return fw.State(rt.P.ID, rt.ForwardSpecs())
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
		list := rt.ForwardSpecs()
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
		if err := rt.SaveForwardSpecs(list); err != nil {
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
		list := rt.ForwardSpecs()
		kept := list[:0]
		for _, sp := range list {
			if sp.ID != a.ID {
				kept = append(kept, sp)
			}
		}
		if err := rt.SaveForwardSpecs(kept); err != nil {
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
	pv := pod.Previews()
	h.HandleGlobal("preview.open", func(ctx context.Context, c plugins.Call) (any, error) {
		sp, err := bind[preview.Spec](c.Params)
		if err != nil {
			return nil, err
		}
		if sp.Project == "" && c.Window != nil {
			sp.Project = c.Window.Project()
		}
		return open(ctx, pod, sp)
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
