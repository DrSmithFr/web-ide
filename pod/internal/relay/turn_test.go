package relay

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestTurnCreds(t *testing.T) {
	st, _ := OpenStore(filepath.Join(t.TempDir(), "relay.db"))
	defer st.Close()
	turn, err := NewTurn(st, TurnConfig{AdvertiseIP: "127.0.0.1", Port: 0})
	if err != nil {
		t.Fatal("starting the embedded TURN:", err)
	}
	defer turn.Close()

	// No pod: no credentials.
	if _, _, err := turn.Creds("nobody", time.Minute); err == nil {
		t.Fatal("an unknown pod must not get TURN credentials")
	}

	if err := st.Register("pod-a", "secret-1", ""); err != nil {
		t.Fatal(err)
	}
	user, pass, err := turn.Creds("pod-a", time.Minute)
	if err != nil {
		t.Fatal("an online pod must get TURN credentials:", err)
	}
	if user == "" || pass == "" {
		t.Fatal("credentials must be non-empty")
	}
	// The username encodes the pod id (short-lived TURN-REST credentials).
	if !strings.Contains(user, "pod-a") {
		t.Fatalf("username %q must encode the pod id", user)
	}

	// The TURN/STUN URLs advertise the control port.
	urls := turn.ICE()
	if len(urls) < 3 {
		t.Fatalf("expected STUN + TURN(udp,tcp) URLs, got %v", urls)
	}
	for _, u := range urls[:1] {
		if !strings.HasPrefix(u, "stun:") {
			t.Fatalf("first ICE URL must be STUN, got %q", u)
		}
	}
}
