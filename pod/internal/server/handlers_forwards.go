package server

import (
	"time"

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
