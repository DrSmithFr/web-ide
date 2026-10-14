package relayload

import (
	"context"
	"fmt"
	"net"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/DrSmithFr/web-ide/pod/internal/relay"
)

// testRelay starts a relay of its own (its store, its TURN server on 127.0.0.1) and serves it:
// the tool is driven exactly as it drives the hosted one, over the network.
func testRelay(t *testing.T) *httptest.Server {
	t.Helper()
	st, err := relay.OpenStore(filepath.Join(t.TempDir(), "relay.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	port := freePort(t)
	turn, err := relay.NewTurn(st, relay.TurnConfig{AdvertiseIP: "127.0.0.1", Port: port, MinPort: uint16(port + 1), MaxPort: uint16(port + 80)})
	if err != nil {
		t.Fatal("starting the TURN server:", err)
	}
	t.Cleanup(func() { turn.Close() })
	srv := relay.New(st, turn)
	srv.Version = "load-test"
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts
}

// freePort picks a port free for UDP and TCP both: the TURN control port is served on each, and
// an ephemeral port found by opening a UDP socket is often taken by a TCP one already.
func freePort(t *testing.T) int {
	t.Helper()
	for i := 0; i < 50; i++ {
		c, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1")})
		if err != nil {
			t.Fatal(err)
		}
		port := c.LocalAddr().(*net.UDPAddr).Port
		ln, terr := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
		c.Close()
		if terr != nil {
			continue
		}
		_ = ln.Close()
		return port
	}
	t.Fatal("no free UDP+TCP port found")
	return 0
}

// TestStaticTier: the pages of the relay are served, and the run reports what it read.
func TestStaticTier(t *testing.T) {
	ts := testRelay(t)
	rep, err := Run(context.Background(), Config{
		RelayURL: ts.URL, Tier: "static", Workers: 4, Hold: 2 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	if rep.OK == 0 || rep.Failed > 0 {
		t.Fatalf("static tier: %+v (%v)", rep, rep.Errors)
	}
	if rep.Bytes < 1<<10 {
		t.Fatalf("static tier read %d bytes", rep.Bytes)
	}
}

// TestSignalTier: the pod links and the browser links come up, the offer reaches the pod and the
// answer comes back, and the relay says it carries them.
func TestSignalTier(t *testing.T) {
	ts := testRelay(t)
	rep, err := Run(context.Background(), Config{
		RelayURL: ts.URL, Tier: "signal", Pods: 3, Sessions: 6, Ramp: 200 * time.Millisecond, Hold: 3 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	if rep.OK != 6 || rep.Failed != 0 {
		t.Fatalf("signal tier: %d up, %d failed (%v)", rep.OK, rep.Failed, rep.Errors)
	}
	// The relay reported the links while the run held them open.
	var seen bool
	for _, s := range rep.Status {
		if s.Load.Pods >= 3 && s.Load.Browsers >= 1 {
			seen = true
		}
	}
	if !seen {
		t.Fatalf("the relay never reported the links it carried: %+v", rep.Status)
	}
}

// TestTunnelTier: a real tunnel through the embedded TURN server, the token handshake on the
// channel, and a payload pushed through it.
func TestTunnelTier(t *testing.T) {
	ts := testRelay(t)
	rep, err := Run(context.Background(), Config{
		RelayURL: ts.URL, Tier: "turn", Pods: 1, Sessions: 2, MB: 1, ForceTurn: true,
		Ramp: 200 * time.Millisecond, Hold: 1 * time.Second, Timeout: 20 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	if rep.OK != 2 || rep.Failed != 0 {
		t.Fatalf("turn tier: %d up, %d failed (%v)", rep.OK, rep.Failed, rep.Errors)
	}
	if want := int64(2 << 20); rep.Bytes != want {
		t.Fatalf("turn tier pushed %d bytes, want %d", rep.Bytes, want)
	}
	var allocations int
	for _, s := range rep.Status {
		if s.Load.Allocations > allocations {
			allocations = s.Load.Allocations
		}
	}
	if allocations == 0 {
		t.Fatalf("no TURN allocation was reported by the relay: %s", rep.String())
	}
	fmt.Print(rep.String())
}
