package relay

import (
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"
)

// TestStatusCounters: the status tells anonymous counters only — the pods online and the
// accounts seen in the last 30 days — and nothing about who they are.
func TestStatusCounters(t *testing.T) {
	st, _ := OpenStore(filepath.Join(t.TempDir(), "relay.db"))
	t.Cleanup(func() { st.Close() })
	for _, id := range []string{"pod-a", "pod-b"} {
		if err := st.Register(id, "secret-"+id, id); err != nil {
			t.Fatal(err)
		}
	}
	st.Online("pod-a")
	st.Offline("pod-b")
	st.TouchAccount("account-1")
	st.TouchAccount("account-2")
	st.TouchAccount("account-1") // seen twice, counted once

	pods, accounts, err := st.Presence(time.Now().Add(-accountsSeenWindow))
	if err != nil {
		t.Fatal(err)
	}
	if pods != 1 || accounts != 2 {
		t.Fatalf("presence: %d pods, %d accounts", pods, accounts)
	}
	// An account last seen before the window is not a user of the relay anymore.
	if _, accounts, err := st.Presence(time.Now().Add(time.Hour)); err != nil || accounts != 0 {
		t.Fatalf("presence beyond the window: %d accounts, %v", accounts, err)
	}

	srv := New(st, nil)
	srv.Version = "v1.2.3"
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	r, err := ts.Client().Get(ts.URL + "/_relay/status")
	if err != nil {
		t.Fatal(err)
	}
	defer r.Body.Close()
	var got Status
	if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if !got.Up || got.Version != "v1.2.3" || got.Pods != 1 || got.Accounts != 2 {
		t.Fatalf("status: %+v", got)
	}
	if got.Uptime < 0 || got.StartedAt == 0 {
		t.Fatalf("uptime: %+v", got)
	}
}

// TestStatusLoad: the status carries what the relay holds right now — the links it serves and
// what the process costs — so a load test can read the same numbers it is pushing on.
func TestStatusLoad(t *testing.T) {
	ts := newRelay(t)
	pod := connectPod(t, ts, "pod-a", "secret-1", "laptop")
	defer pod.close()
	browser := connectBrowser(t, ts, "pod-a", "sA")
	defer browser.close()

	r, err := ts.Client().Get(ts.URL + "/_relay/status")
	if err != nil {
		t.Fatal(err)
	}
	defer r.Body.Close()
	var got Status
	if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	l := got.Load
	if l.Pods != 1 || l.Browsers != 1 {
		t.Fatalf("load: %+v (one pod link and one browser link expected)", l)
	}
	if l.Goroutines < 1 {
		t.Fatalf("goroutines: %d", l.Goroutines)
	}
	// No allocation yet, and a relay with no -turn-ports has no port range to report.
	if l.Allocations != 0 || l.TurnPorts != -1 {
		t.Fatalf("turn: %d allocations, %d ports", l.Allocations, l.TurnPorts)
	}
}

// TestTurnPortsRange: the size of the relay port range is what the status reports as the wall of
// the TURN server; without a range, any free port (-1).
func TestTurnPortsRange(t *testing.T) {
	st, _ := OpenStore(filepath.Join(t.TempDir(), "relay.db"))
	t.Cleanup(func() { st.Close() })
	turn, _ := startTurn(t, st, TurnConfig{AdvertiseIP: "127.0.0.1", MinPort: 49160, MaxPort: 49169})
	defer turn.Close()
	if alloc, ports := turn.Load(); alloc != 0 || ports != 10 {
		t.Fatalf("port range 49160-49169: %d allocations, %d ports", alloc, ports)
	}
}
