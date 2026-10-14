// Package relayload drives a relay with synthetic traffic (#72): how many links, sessions and
// TURN allocations one relay holds, and how long each of them takes. It speaks the relay's own
// protocol over the network — HTTP, WebSocket, WebRTC — never its Go API, so the same tool runs
// against the hosted one.
//
// Three tiers, each measuring a different cost:
//
//	static  the pages, the Service Worker, the identity module: what TLS and HTTP cost
//	signal  the pod links and the browser links, offer/answer/candidate/bye, and the `site`
//	        messages that make the relay recompute its sites
//	turn    real tunnels through the embedded TURN server, with the tunnel handshake and a
//	        payload pumped through the DataChannel: allocations, relay ports, bandwidth
//
// The turn tier plays both ends of a tunnel in one process (a browser and a pod), because the
// relay only carries the signaling: what is measured is the relay, not a pod.
package relayload

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
)

// Config of one run.
type Config struct {
	RelayURL  string        // http(s):// of the relay, no trailing slash
	Tier      string        // "static", "signal", "turn"
	Pods      int           // pod links to register
	Sessions  int           // browser sessions to bring up and hold
	MB        int           // MB to pump through each tunnel (turn tier)
	Ramp      time.Duration // spreading the sessions up over this window
	Hold      time.Duration // keeping them open this long once they are up
	Workers   int           // static tier: concurrent requests
	ForceTurn bool          // turn tier: relay candidates only, no direct path
	Timeout   time.Duration // a session that does not come up within this window is a failure
}

// fill defaults the parts a run does not name.
func (c *Config) fill() {
	if c.Pods == 0 {
		c.Pods = 4
	}
	if c.Sessions == 0 {
		c.Sessions = 10
	}
	if c.Ramp == 0 {
		c.Ramp = 5 * time.Second
	}
	if c.Hold == 0 {
		c.Hold = 10 * time.Second
	}
	if c.Workers == 0 {
		c.Workers = 8
	}
	if c.Timeout == 0 {
		c.Timeout = 30 * time.Second
	}
	if c.Tier == "" {
		c.Tier = "signal"
	}
}

// Status is what the relay says about itself while a run goes on (/_relay/status, #72). The
// fields are read as they come: a relay older than the load counters simply gives none.
type Status struct {
	Version   string `json:"version"`
	Pods      int    `json:"pods"`
	Accounts  int    `json:"accounts"`
	Load      struct {
		Pods        int   `json:"pods"`
		Browsers    int   `json:"browsers"`
		Identity    int   `json:"identity"`
		Allocations int   `json:"turn"`
		TurnPorts   int   `json:"turnPorts"`
		RSS         int64 `json:"rss"`
		FDs         int   `json:"fds"`
		Goroutines  int   `json:"goroutines"`
	} `json:"load"`
}

// Report is one run: what came up, how long it took, what the relay reported while it happened.
type Report struct {
	Tier    string          `json:"tier"`
	Pods    int             `json:"pods"`
	Sessions int            `json:"sessions"`
	OK      int             `json:"ok"`
	Failed  int             `json:"failed"`
	Setup   []time.Duration `json:"setupMs"` // per session: dialed to tunnel (or link) up
	Bytes   int64           `json:"bytes"`
	Seconds float64         `json:"seconds"`
	Status  []Status        `json:"status"`
	Errors  []string        `json:"errors"`
}

// String is the run as a few lines: the time to bring a session up, what it moved, and the last
// word of the relay about itself.
func (r *Report) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "tier %s · %d pods · %d sessions: %d up, %d failed in %.1f s\n",
		r.Tier, r.Pods, r.Sessions, r.OK, r.Failed, r.Seconds)
	if n := len(r.Setup); n > 0 {
		s := append([]time.Duration(nil), r.Setup...)
		sort.Slice(s, func(i, j int) bool { return s[i] < s[j] })
		fmt.Fprintf(&b, "  session up: p50 %s  p95 %s  max %s\n",
			pct(s, 50), pct(s, 95), s[n-1])
	}
	if r.Bytes > 0 && r.Seconds > 0 {
		fmt.Fprintf(&b, "  through the tunnels: %.1f MB, %.2f Mbit/s\n",
			float64(r.Bytes)/(1<<20), float64(r.Bytes)*8/r.Seconds/1e6)
	}
	if len(r.Status) > 0 {
		s := r.Status[len(r.Status)-1]
		l := s.Load
		fmt.Fprintf(&b, "  relay: %s · links %d pods / %d browsers / %d identity · turn %d allocations of %d ports · rss %.0f MB · %d fds · %d goroutines\n",
			s.Version, l.Pods, l.Browsers, l.Identity, l.Allocations, l.TurnPorts,
			float64(l.RSS)/(1<<20), l.FDs, l.Goroutines)
	}
	for _, e := range r.Errors {
		fmt.Fprintf(&b, "  ! %s\n", e)
	}
	return b.String()
}

// pct is the p-th percentile of a sorted list.
func pct(sorted []time.Duration, p int) time.Duration {
	if len(sorted) == 0 {
		return 0
	}
	i := (len(sorted) - 1) * p / 100
	return sorted[i]
}

// Run drives one tier and reports what it took.
func Run(ctx context.Context, c Config) (*Report, error) {
	c.fill()
	rep := &Report{Tier: c.Tier, Pods: c.Pods, Sessions: c.Sessions}
	var mu sync.Mutex
	stop := context.CancelFunc(func() {})
	runCtx, cancel := context.WithCancel(ctx)
	stop = cancel
	defer stop()

	// The relay is read while it is pushed on: the samples are what a ramp is compared with.
	var samples []Status
	go func() {
		for {
			if s, err := readStatus(runCtx, c.RelayURL); err == nil {
				mu.Lock()
				samples = append(samples, s)
				mu.Unlock()
			}
			select {
			case <-runCtx.Done():
				return
			case <-time.After(time.Second):
			}
		}
	}()

	at := time.Now()
	var err error
	cfg := &c
	switch c.Tier {
	case "static":
		err = runStatic(runCtx, cfg, rep)
	case "signal":
		err = runSignal(runCtx, cfg, rep, &mu)
	case "turn":
		err = runTunnel(runCtx, cfg, rep, &mu)
	default:
		err = fmt.Errorf("unknown tier: %s", c.Tier)
	}
	_ = stop
	rep.Seconds = time.Since(at).Seconds()
	mu.Lock()
	rep.Status = samples
	mu.Unlock()
	if err != nil {
		return rep, err
	}
	return rep, nil
}

// readStatus asks the relay what it holds right now.
func readStatus(ctx context.Context, base string) (Status, error) {
	var s Status
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/_relay/status", nil)
	if err != nil {
		return s, err
	}
	r, err := http.DefaultClient.Do(req)
	if err != nil {
		return s, err
	}
	defer r.Body.Close()
	if err := json.NewDecoder(r.Body).Decode(&s); err != nil {
		return s, err
	}
	return s, nil
}

// note records one failure, keeping the list short.
func (r *Report) note(err error) {
	if len(r.Errors) < 20 {
		r.Errors = append(r.Errors, err.Error())
	}
	r.Failed++
}
