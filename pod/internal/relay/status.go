package relay

import (
	"encoding/json"
	"net/http"
	"time"
)

// The status of the relay (milestone 5): what it takes to tell a relay is alive and roughly how
// much it carries. Nothing per account, nothing a person: the counters are the pods online and
// the accounts seen in the last 30 days, which is what the presence of the pods already gives
// the relay. No telemetry leaves the pod nor the page for it.
type Status struct {
	Up        bool   `json:"up"`
	Version   string `json:"version"`
	Uptime    int64  `json:"uptime"` // seconds
	Pods      int    `json:"pods"`   // pods online now
	Accounts  int    `json:"accounts"`
	StartedAt int64  `json:"startedAt"`
}

// accountsSeenWindow is how far back an account counts as seen: an account whose pods were
// online in the last 30 days is a user of the relay, one older than that is not counted.
const accountsSeenWindow = 30 * 24 * time.Hour

// serveStatus answers the status of the relay, for the uptime check of the hosted one and for
// anyone wondering whether it is up.
func (s *Server) serveStatus(w http.ResponseWriter, r *http.Request) {
	st := Status{Up: true, Version: s.Version, StartedAt: s.startedAt.Unix(), Uptime: int64(time.Since(s.startedAt).Seconds())}
	if pods, accounts, err := s.store.Presence(time.Now().Add(-accountsSeenWindow)); err == nil {
		st.Pods, st.Accounts = pods, accounts
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(st)
}
