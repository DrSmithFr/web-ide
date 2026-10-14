package relay

import (
	"net"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	turnserver "github.com/pion/turn/v5"
)

func TestTurnCreds(t *testing.T) {
	st, _ := OpenStore(filepath.Join(t.TempDir(), "relay.db"))
	defer st.Close()
	turn, err := NewTurn(st, TurnConfig{AdvertiseIP: "127.0.0.1", Port: freeTurnPort(t)})
	if err != nil {
		t.Fatal("starting the embedded TURN:", err)
	}
	defer turn.Close()

	// No pod: no credentials.
	if _, _, err := turn.Creds("nobody", "", time.Minute); err == nil {
		t.Fatal("an unknown pod must not get TURN credentials")
	}

	if err := st.Register("pod-a", "secret-1", ""); err != nil {
		t.Fatal(err)
	}
	user, pass, err := turn.Creds("pod-a", "acct-1", time.Minute)
	if err != nil {
		t.Fatal("an online pod must get TURN credentials:", err)
	}
	if user == "" || pass == "" {
		t.Fatal("credentials must be non-empty")
	}
	// The username encodes the key the allocations are counted under (short-lived TURN-REST
	// credentials): <expiry>:<key>.
	if !strings.Contains(user, "acct-1") {
		t.Fatalf("username %q must encode the account key", user)
	}
	if user, _, err := turn.Creds("pod-a", "", time.Minute); err != nil || !strings.Contains(user, "pod-a") {
		t.Fatalf("an unpaired pod is counted under its own id: %q, %v", user, err)
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

// TestTurnUsageCounted: the relay socket of an allocation is wrapped with the byte counter of
// the key its credential names; the flush writes it to the base, aggregated per key and month.
func TestTurnUsageCounted(t *testing.T) {
	st, _ := OpenStore(filepath.Join(t.TempDir(), "relay.db"))
	defer st.Close()
	turn, err := NewTurn(st, TurnConfig{AdvertiseIP: "127.0.0.1", Port: freeTurnPort(t)})
	if err != nil {
		t.Fatal(err)
	}
	defer turn.Close()

	// The allocation path of the generator, with the credential key it counts under.
	gen, ok := turn.gens["4"].(*turnGenerator)
	if !ok {
		t.Fatal("the IPv4 generator is missing")
	}
	conn, _, err := gen.AllocatePacketConn(turnserver.AllocateListenerConfig{Network: "udp4", UserID: "acct-1"})
	if err != nil {
		t.Fatal("allocating a relay socket:", err)
	}
	defer conn.Close()
	peer := &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 65001}
	if _, err := conn.WriteTo([]byte("hello through TURN"), peer); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.WriteTo([]byte("12345"), peer); err != nil {
		t.Fatal(err)
	}

	turn.flushUsage()
	month := usageMonth(time.Now())
	got, err := st.TurnUsage("acct-1", month)
	if err != nil {
		t.Fatal(err)
	}
	if want := int64(len("hello through TURN") + len("12345")); got != want {
		t.Fatalf("the relay counted %d bytes for acct-1, expected %d", got, want)
	}
	if other, _ := st.TurnUsage("acct-2", month); other != 0 {
		t.Fatalf("another account must not be charged: %d", other)
	}

	// A second flush adds to the month rather than replacing it.
	turn.usage.count("acct-1").in.Add(7)
	turn.flushUsage()
	if got, _ := st.TurnUsage("acct-1", month); got != 23+7 {
		t.Fatalf("the month accumulates, got %d, expected %d", got, 23+7)
	}
}

// TestStoreUsageMonths: one row per key and month, the quota being monthly.
func TestStoreUsageMonths(t *testing.T) {
	st, _ := OpenStore(filepath.Join(t.TempDir(), "relay.db"))
	defer st.Close()
	if err := st.AddUsage("acct-1", "2026-10", 100, 50); err != nil {
		t.Fatal(err)
	}
	if err := st.AddUsage("acct-1", "2026-10", 1, 0); err != nil {
		t.Fatal(err)
	}
	if err := st.AddUsage("acct-1", "2026-11", 9, 9); err != nil {
		t.Fatal(err)
	}
	if got, _ := st.TurnUsage("acct-1", "2026-10"); got != 151 {
		t.Fatalf("October: %d, expected 151", got)
	}
	if got, _ := st.TurnUsage("acct-1", "2026-11"); got != 18 {
		t.Fatalf("November: %d, expected 18", got)
	}
	if got, _ := st.TurnUsage("nobody", "2026-10"); got != 0 {
		t.Fatalf("an account with no row: %d, expected 0", got)
	}
}

// TestTurnICEURLs: with a domain, the ICE URLs name it (turns: on the TLS port, the scheme of
// TURN over TLS); without one, they name the addresses of the machine, IPv4 and IPv6.
func TestTurnICEURLs(t *testing.T) {
	urls := iceURLs(TurnConfig{
		AdvertiseIP: "5.196.7.104", AdvertiseIPv6: "2001:41d0:404:200::a601",
		Domain: "antennae.space", Port: 3478, TLSPort: 443, TLSListener: &net.TCPListener{},
	}, []net.IP{net.ParseIP("5.196.7.104"), net.ParseIP("2001:41d0:404:200::a601")})
	want := []string{
		"stun:antennae.space:3478",
		"turn:antennae.space:3478?transport=udp",
		"turn:antennae.space:3478?transport=tcp",
		"turns:antennae.space:443?transport=tcp",
		"stun:5.196.7.104:3478",
		"turn:[2001:41d0:404:200::a601]:3478?transport=udp",
		"turns:[2001:41d0:404:200::a601]:443?transport=tcp",
	}
	for _, w := range want {
		if !hasURL(urls, w) {
			t.Fatalf("the ICE URLs must contain %q, got %v", w, urls)
		}
	}

	st, _ := OpenStore(filepath.Join(t.TempDir(), "relay.db"))
	defer st.Close()
	plain, err := NewTurn(st, TurnConfig{AdvertiseIP: "127.0.0.1", Port: freeTurnPort(t)})
	if err != nil {
		t.Fatal(err)
	}
	defer plain.Close()
	for _, u := range plain.ICE() {
		if strings.HasPrefix(u, "turns:") {
			t.Fatalf("no TLS side: no turns: URL, got %q", u)
		}
	}
}

func hasURL(urls []string, want string) bool {
	for _, u := range urls {
		if u == want {
			return true
		}
	}
	return false
}

// TestTurnDualStack: a relay with a global IPv6 binds its control ports on both addresses, so
// an IPv6 client reaches STUN and TURN there too.
func TestTurnDualStack(t *testing.T) {
	st, _ := OpenStore(filepath.Join(t.TempDir(), "relay.db"))
	defer st.Close()
	port := freeTurnPort(t)
	turn, err := NewTurn(st, TurnConfig{AdvertiseIP: "127.0.0.1", AdvertiseIPv6: "::1", Port: port})
	if err != nil {
		t.Fatal("the TURN server must bind IPv4 and IPv6:", err)
	}
	defer turn.Close()
	if len(turn.udp) != 2 || len(turn.tcp) != 2 {
		t.Fatalf("one UDP and one TCP listener per address expected, got %d udp, %d tcp", len(turn.udp), len(turn.tcp))
	}
	for _, want := range []string{"stun:127.0.0.1:" + strconv.Itoa(port), "stun:[::1]:" + strconv.Itoa(port)} {
		if !hasURL(turn.ICE(), want) {
			t.Fatalf("the ICE URLs must contain %q, got %v", want, turn.ICE())
		}
	}
	c, err := net.Dial("tcp", "[::1]:"+strconv.Itoa(port))
	if err != nil {
		t.Fatal("the TURN control port must answer on IPv6:", err)
	}
	c.Close()
}

// freeTurnPort asks the kernel for a free UDP port: the default 3478 may be taken by a relay
// running on the machine, or by the tests of another worktree.
func freeTurnPort(t *testing.T) int {
	t.Helper()
	c, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	port := c.LocalAddr().(*net.UDPAddr).Port
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	return port
}
