package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"time"

	"github.com/DrSmithFr/web-ide/pod/internal/i18n"
	"github.com/DrSmithFr/web-ide/pod/internal/runtime"
	"github.com/DrSmithFr/web-ide/pod/internal/tunnels"
)

// TunnelIdle is how long the tunnels stay open once the last window of the pod has left.
var TunnelIdle = 5 * time.Minute

// openTunnels opens the enabled tunnels of an SSH project that are not open yet.
func (s *Server) openTunnels(rt *runtime.Runtime) {
	if rt.Local {
		return
	}
	for _, sp := range rt.TunnelSpecs() {
		if sp.Enabled && !s.Tunnels.IsOpen(rt.P.ID, sp.ID) {
			_ = s.Tunnels.Open(rt.P.ID, sp, rt.Dial) // the error shows in the state of the tunnel
		}
	}
}

// windowsChanged closes every tunnel a while after the last window of the pod has left.
// Called with s.mu held.
func (s *Server) windowsChanged() {
	if s.tunnelIdle != nil {
		s.tunnelIdle.Stop()
		s.tunnelIdle = nil
	}
	if len(s.clients) == 0 {
		s.tunnelIdle = time.AfterFunc(TunnelIdle, s.Tunnels.CloseAll)
	}
}

func (s *Server) registerTunnels() {
	state := func(rt *runtime.Runtime) []tunnels.State {
		if rt.Local {
			return []tunnels.State{}
		}
		return s.Tunnels.State(rt.P.ID, rt.TunnelSpecs())
	}
	s.handle("tunnels.get", withRuntime(func(ctx context.Context, c *Client, rt *runtime.Runtime, p json.RawMessage) (any, error) {
		return state(rt), nil
	}))
	// tunnels.save adds a tunnel (no id) or replaces one, then opens or closes it.
	s.handle("tunnels.save", withRuntime(func(ctx context.Context, c *Client, rt *runtime.Runtime, p json.RawMessage) (any, error) {
		sp, err := bind[tunnels.Spec](p)
		if err != nil {
			return nil, err
		}
		if rt.Local {
			return nil, i18n.New("tunnels are for SSH projects")
		}
		if err := sp.Check(); err != nil {
			return nil, err
		}
		list := rt.TunnelSpecs()
		found := false
		for i := range list {
			if list[i].ID == sp.ID && sp.ID != "" {
				list[i], found = sp, true
			}
		}
		if !found {
			if sp.ID != "" {
				return nil, i18n.New("tunnel not found")
			}
			b := make([]byte, 4)
			_, _ = rand.Read(b)
			sp.ID = hex.EncodeToString(b)
			list = append(list, sp)
		}
		if err := rt.SaveTunnelSpecs(list); err != nil {
			return nil, err
		}
		if sp.Enabled {
			err = s.Tunnels.Open(rt.P.ID, sp, rt.Dial)
		} else {
			s.Tunnels.Close(rt.P.ID, sp.ID)
		}
		s.broadcast("tunnels.changed", nil, nil)
		return state(rt), err
	}))
	s.handle("tunnels.delete", withRuntime(func(ctx context.Context, c *Client, rt *runtime.Runtime, p json.RawMessage) (any, error) {
		a, err := bind[struct {
			ID string `json:"id"`
		}](p)
		if err != nil {
			return nil, err
		}
		list := rt.TunnelSpecs()
		kept := list[:0]
		for _, sp := range list {
			if sp.ID != a.ID {
				kept = append(kept, sp)
			}
		}
		if err := rt.SaveTunnelSpecs(kept); err != nil {
			return nil, err
		}
		s.Tunnels.Close(rt.P.ID, a.ID)
		s.broadcast("tunnels.changed", nil, nil)
		return state(rt), nil
	}))
	// The home page lists the open tunnels of every project and closes them (they stay
	// enabled: the project opens them again).
	s.handle("tunnels.all", func(ctx context.Context, c *Client, p json.RawMessage) (any, error) {
		type view struct {
			tunnels.State
			Name string `json:"name"`
			Host string `json:"host"`
		}
		out := []view{}
		for _, st := range s.Tunnels.List() {
			v := view{State: st, Name: st.Project}
			if pr, ok := s.Projects.Get(st.Project); ok {
				v.Name = pr.Name()
				if pr.SSH != nil {
					v.Host = pr.SSH.Host
				}
			}
			out = append(out, v)
		}
		return out, nil
	})
	s.handle("tunnels.close", func(ctx context.Context, c *Client, p json.RawMessage) (any, error) {
		a, err := bind[struct {
			Project string `json:"project"`
			ID      string `json:"id"`
		}](p)
		if err != nil {
			return nil, err
		}
		s.Tunnels.Close(a.Project, a.ID)
		return nil, nil
	})
	s.handle("tunnels.closeAll", func(ctx context.Context, c *Client, p json.RawMessage) (any, error) {
		s.Tunnels.CloseAll()
		return nil, nil
	})
}
