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
	turn, _ := startTurn(t, st, TurnConfig{AdvertiseIP: "127.0.0.1"})
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
	turn, _ := startTurn(t, st, TurnConfig{AdvertiseIP: "127.0.0.1"})
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

// TestTurnQuota: over the monthly quota the credentials are refused (the direct paths stay
// open), a supporter is freed from it, and a relay with no -quota has no limit.
func TestTurnQuota(t *testing.T) {
	st, _ := OpenStore(filepath.Join(t.TempDir(), "relay.db"))
	defer st.Close()
	month := usageMonth(time.Now())
	if err := st.AddUsage("acct-over", month, 600, 500); err != nil {
		t.Fatal(err)
	}
	if err := st.AddUsage("acct-under", month, 100, 0); err != nil {
		t.Fatal(err)
	}

	turn, _ := startTurn(t, st, TurnConfig{
		AdvertiseIP: "127.0.0.1", Quota: 1000,
		Supporter: func(key string) bool { return key == "acct-supporter" },
	})
	defer turn.Close()
	for _, id := range []string{"acct-over", "acct-under", "acct-supporter"} {
		if err := st.Register(id, "secret-"+id, ""); err != nil {
			t.Fatal(err)
		}
	}

	if _, _, err := turn.Creds("acct-over", "acct-over", time.Hour); err != errQuota {
		t.Fatalf("an account over its quota must be refused: %v", err)
	}
	if _, _, err := turn.Creds("acct-under", "acct-under", time.Hour); err != nil {
		t.Fatalf("under the quota: %v", err)
	}
	if _, _, err := turn.Creds("acct-supporter", "acct-supporter", time.Hour); err != nil {
		t.Fatalf("a supporter is freed from the quota: %v", err)
	}

	// What the relay tells the pod and the page.
	stOver := turn.QuotaState("acct-over")
	if !stOver.Over || stOver.Used != 1100 || stOver.Limit != 1000 {
		t.Fatalf("quota state of an account over it: %+v", stOver)
	}
	if stUnder := turn.QuotaState("acct-under"); stUnder.Over {
		t.Fatalf("under the quota: %+v", stUnder)
	}
	if sup := turn.QuotaState("acct-supporter"); !sup.Supporter || sup.Over {
		t.Fatalf("a supporter: %+v", sup)
	}

	// No -quota (a self-hosted relay): the same bytes move freely.
	free, _ := startTurn(t, st, TurnConfig{AdvertiseIP: "127.0.0.1"})
	defer free.Close()
	if _, _, err := free.Creds("acct-over", "acct-over", time.Hour); err != nil {
		t.Fatalf("a self-hosted relay has no limit: %v", err)
	}
	if q := free.QuotaState("acct-over"); q.Over || q.Limit != 0 {
		t.Fatalf("no limit, no over: %+v", q)
	}
}

// TestTurnICEURLs: with a domain, the ICE URLs name it alone, over UDP and over TLS on the TLS
// port (each TURN URL is an allocation per end, #72); without one, they name the addresses of the
// machine, IPv4 and IPv6, over UDP and TCP.
func TestTurnICEURLs(t *testing.T) {
	addrs := []net.IP{net.ParseIP("5.196.7.104"), net.ParseIP("2001:41d0:404:200::a601")}
	urls := iceURLs(TurnConfig{
		AdvertiseIP: "5.196.7.104", AdvertiseIPv6: "2001:41d0:404:200::a601",
		Domain: "antennae.space", Port: 3478, TLSPort: 443, TLSListener: &net.TCPListener{},
	}, addrs)
	want := []string{
		"stun:antennae.space:3478",
		"turn:antennae.space:3478?transport=udp",
		"turns:antennae.space:443?transport=tcp",
	}
	if strings.Join(urls, " ") != strings.Join(want, " ") {
		t.Fatalf("with a domain, the ICE URLs must be %v, got %v", want, urls)
	}
	// A domain with no TLS side keeps TCP 3478 as the fallback of a network that blocks UDP.
	urls = iceURLs(TurnConfig{Domain: "antennae.space", Port: 3478}, addrs)
	if !hasURL(urls, "turn:antennae.space:3478?transport=tcp") || len(urls) != 3 {
		t.Fatalf("a domain without TLS falls back on TCP 3478, got %v", urls)
	}

	urls = iceURLs(TurnConfig{Port: 3478}, addrs)
	for _, w := range []string{
		"stun:5.196.7.104:3478",
		"turn:5.196.7.104:3478?transport=tcp",
		"turn:[2001:41d0:404:200::a601]:3478?transport=udp",
	} {
		if !hasURL(urls, w) {
			t.Fatalf("without a domain, the ICE URLs must contain %q, got %v", w, urls)
		}
	}

	st, _ := OpenStore(filepath.Join(t.TempDir(), "relay.db"))
	defer st.Close()
	plain, _ := startTurn(t, st, TurnConfig{AdvertiseIP: "127.0.0.1"})
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
	turn, port := startTurn(t, st, TurnConfig{AdvertiseIP: "127.0.0.1", AdvertiseIPv6: "::1"})
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

// startTurn starts the embedded TURN server of a test. A port only probed can be taken before the
// server binds it — by a test binary running in parallel, or by the server of the test before — so
// the start is retried on another port. It returns the server and the port it got.
func startTurn(t *testing.T, st *Store, cfg TurnConfig) (*Turn, int) {
	t.Helper()
	var last error
	for i := 0; i < 20; i++ {
		cfg.Port = freeTurnPort(t)
		turn, err := NewTurn(st, cfg)
		if err == nil {
			return turn, cfg.Port
		}
		last = err
	}
	t.Fatalf("starting the embedded TURN: %v", last)
	return nil, 0
}

// freeTurnPort picks a port free for UDP and TCP both: the control port of the embedded TURN is
// served on each, and an ephemeral port found by opening a UDP socket is often taken by a TCP one
// already.
func freeTurnPort(t *testing.T) int {
	t.Helper()
	for i := 0; i < 50; i++ {
		c, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
		if err != nil {
			t.Fatal(err)
		}
		port := c.LocalAddr().(*net.UDPAddr).Port
		ln, terr := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
		_ = c.Close()
		if terr != nil {
			continue
		}
		_ = ln.Close()
		return port
	}
	t.Fatal("no free UDP+TCP port found")
	return 0
}
