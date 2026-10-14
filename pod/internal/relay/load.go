package relay

import (
	"os"
	"runtime"
	"strconv"
	"strings"
)

// The load of the relay (#72): what it holds at this moment, read off its own state. It is
// anonymous like the rest of the status — counts of links and allocations, never who they are —
// and it is what a load test compares with the time it took to get there.
type Load struct {
	Pods        int   `json:"pods"`      // pod links registered
	Browsers    int   `json:"browsers"`  // browser signaling links
	Identity    int   `json:"identity"`  // pairing and admin links
	Allocations int   `json:"turn"`      // TURN allocations relaying right now
	TurnPorts   int   `json:"turnPorts"` // relay ports the TURN range offers (-1: any free port)
	RSS         int64 `json:"rss"`       // bytes of resident memory of the process
	FDs         int   `json:"fds"`       // open file descriptors (a link, a socket, a file)
	Goroutines  int   `json:"goroutines"`
}

// load counts what this relay is carrying. The TURN part is nil on a relay started without one.
func (s *Server) load() Load {
	s.mu.Lock()
	l := Load{Pods: len(s.pods), Browsers: len(s.browsers), Identity: len(s.links)}
	s.mu.Unlock()
	l.TurnPorts = -1
	if s.turn != nil {
		l.Allocations, l.TurnPorts = s.turn.Load()
	}
	l.RSS = procRSS()
	l.FDs = procFDs()
	l.Goroutines = runtime.NumGoroutine()
	return l
}

// Load counts the allocations the TURN server is relaying and the size of the relay port range it
// was given (-1 when it has none: any free port). The range is the wall a load test runs into:
// one allocation takes one relay port, so -turn-ports 49160-49999 holds 840 of them.
func (t *Turn) Load() (allocations, ports int) {
	t.mu.Lock()
	allocations = t.allocs
	t.mu.Unlock()
	ports = -1
	for _, g := range t.gens {
		tg, ok := g.(*turnGenerator)
		if !ok || tg.minPort == 0 || tg.maxPort < tg.minPort {
			continue
		}
		if n := int(tg.maxPort - tg.minPort + 1); ports < 0 || n < ports {
			ports = n
		}
	}
	return allocations, ports
}

// procRSS is the resident memory of the process (Linux; 0 where /proc is not).
func procRSS() int64 {
	data, err := os.ReadFile("/proc/self/status")
	if err != nil {
		return 0
	}
	for _, line := range strings.Split(string(data), "\n") {
		if !strings.HasPrefix(line, "VmRSS:") {
			continue
		}
		f := strings.Fields(strings.TrimPrefix(line, "VmRSS:"))
		if len(f) == 0 {
			continue
		}
		kb, err := strconv.ParseInt(f[0], 10, 64)
		if err != nil {
			return 0
		}
		return kb * 1024
	}
	return 0
}

// procFDs counts the open file descriptors of the process (Linux; 0 where /proc is not).
func procFDs() int {
	entries, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		return 0
	}
	return len(entries)
}
