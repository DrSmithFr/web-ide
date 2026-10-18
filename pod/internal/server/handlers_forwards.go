package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"time"

	"github.com/DrSmithFr/web-ide/pod/internal/forwards"
	"github.com/DrSmithFr/web-ide/pod/internal/i18n"
	"github.com/DrSmithFr/web-ide/pod/internal/runtime"
)

// ForwardIdle is how long the port forwards stay open once the last window of the pod has left.
var ForwardIdle = 5 * time.Minute

// openForwards opens the enabled port forwards of an SSH project that are not open yet.
func (s *Server) openForwards(rt *runtime.Runtime) {
	if rt.Local {
		return
	}
	for _, sp := range rt.ForwardSpecs() {
		if sp.Enabled && !s.Forwards.IsOpen(rt.P.ID, sp.ID) {
			_ = s.Forwards.Open(rt.P.ID, sp, rt.Dial) // the error shows in the state of the port forward
		}
	}
}

// windowsChanged closes every port forward a while after the last window of the pod has left.
// Called with s.mu held.
func (s *Server) windowsChanged() {
	if s.forwardIdle != nil {
		s.forwardIdle.Stop()
		s.forwardIdle = nil
	}
	if len(s.clients) == 0 {
		s.forwardIdle = time.AfterFunc(ForwardIdle, s.Forwards.CloseAll)
	}
}

// The methods and the event keep the name tunnels.* (the glossary says port forwards): a window
// opened before an update of the pod still calls them.
func (s *Server) registerForwards() {
	state := func(rt *runtime.Runtime) []forwards.State {
		if rt.Local {
			return []forwards.State{}
		}
		return s.Forwards.State(rt.P.ID, rt.ForwardSpecs())
	}
	s.handle("tunnels.get", withRuntime(func(ctx context.Context, c *Client, rt *runtime.Runtime, p json.RawMessage) (any, error) {
		return state(rt), nil
	}))
	// tunnels.save adds a port forward (no id) or replaces one, then opens or closes it.
	s.handle("tunnels.save", withRuntime(func(ctx context.Context, c *Client, rt *runtime.Runtime, p json.RawMessage) (any, error) {
		sp, err := bind[forwards.Spec](p)
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
			err = s.Forwards.Open(rt.P.ID, sp, rt.Dial)
		} else {
			s.Forwards.Close(rt.P.ID, sp.ID)
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
		s.Forwards.Close(rt.P.ID, a.ID)
		s.broadcast("tunnels.changed", nil, nil)
		return state(rt), nil
	}))
	// The home page lists the open port forwards of every project and closes them (they stay
	// enabled: the project opens them again).
	s.handle("tunnels.all", func(ctx context.Context, c *Client, p json.RawMessage) (any, error) {
		type view struct {
			forwards.State
			Name string `json:"name"`
			Host string `json:"host"`
		}
		out := []view{}
		for _, st := range s.Forwards.List() {
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
		s.Forwards.Close(a.Project, a.ID)
		return nil, nil
	})
	s.handle("tunnels.closeAll", func(ctx context.Context, c *Client, p json.RawMessage) (any, error) {
		s.Forwards.CloseAll()
		return nil, nil
	})
}
