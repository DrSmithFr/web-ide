package power

import (
	"bytes"
	"crypto/ed25519"
	"net"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"antennae.space/ide/lib/identity"
)

// TestMagicPacket: six 0xFF then sixteen times the MAC, 102 bytes.
func TestMagicPacket(t *testing.T) {
	p, err := MagicPacket("01:23:45:67:89:ab")
	if err != nil {
		t.Fatal(err)
	}
	if len(p) != 102 || !bytes.Equal(p[:6], bytes.Repeat([]byte{0xff}, 6)) {
		t.Fatalf("header: %x", p)
	}
	mac := []byte{0x01, 0x23, 0x45, 0x67, 0x89, 0xab}
	if !bytes.Equal(p[6:], bytes.Repeat(mac, 16)) {
		t.Fatalf("body: %x", p[6:])
	}
	if _, err := MagicPacket("not a mac"); err == nil {
		t.Fatal("a bad MAC is refused")
	}
}

// TestWakeable: the loopback, a card that is down and the virtual ones are never published.
func TestWakeable(t *testing.T) {
	mac, _ := net.ParseMAC("02:00:00:00:00:01")
	up := net.FlagUp | net.FlagBroadcast
	cases := []struct {
		i    net.Interface
		want bool
	}{
		{net.Interface{Name: "enp3s0", Flags: up, HardwareAddr: mac}, true},
		{net.Interface{Name: "wlan0", Flags: up, HardwareAddr: mac}, true},
		{net.Interface{Name: "lo", Flags: net.FlagUp | net.FlagLoopback}, false},
		{net.Interface{Name: "eth1", Flags: 0, HardwareAddr: mac}, false},
		{net.Interface{Name: "docker0", Flags: up, HardwareAddr: mac}, false},
		{net.Interface{Name: "br-12ab", Flags: up, HardwareAddr: mac}, false},
		{net.Interface{Name: "veth9f", Flags: up, HardwareAddr: mac}, false},
		{net.Interface{Name: "virbr0", Flags: up, HardwareAddr: mac}, false},
		{net.Interface{Name: "tailscale0", Flags: up}, false},
		{net.Interface{Name: "tun0", Flags: up, HardwareAddr: mac}, false},
	}
	for _, c := range cases {
		if got := wakeable(c.i); got != c.want {
			t.Errorf("%s: %v, want %v", c.i.Name, got, c.want)
		}
	}
}

// TestReachable: a target is kept when this machine has an address in its network.
func TestReachable(t *testing.T) {
	_, home, _ := net.ParseCIDR("192.168.1.20/24")
	home.IP = net.ParseIP("192.168.1.20")
	got := reachableFrom([]Target{{MAC: "a", Net: "192.168.1.0/24"}, {MAC: "b", Net: "10.0.0.0/8"}, {MAC: "c", Net: "bad"}}, []*net.IPNet{home})
	if len(got) != 1 || got[0].MAC != "a" {
		t.Fatalf("only the card of the same network: %+v", got)
	}
	if b := broadcast(&net.IPNet{IP: net.ParseIP("192.168.1.0").To4(), Mask: net.CIDRMask(24, 32)}); b.String() != "192.168.1.255" {
		t.Fatalf("broadcast: %s", b)
	}
}

// TestSend: the packet reaches a listener of the network (the loopback, port of the test).
func TestSend(t *testing.T) {
	l, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4zero})
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	t.Setenv(WakePortEnv, strings.TrimPrefix(l.LocalAddr().String(), "0.0.0.0:"))
	if err := Send([]Target{{MAC: "02:00:00:00:00:0b", Net: "127.0.0.0/8"}}); err != nil {
		t.Fatal(err)
	}
	l.SetReadDeadline(time.Now().Add(2 * time.Second))
	buf := make([]byte, 200)
	n, _, err := l.ReadFromUDP(buf)
	if err != nil {
		t.Fatal("no packet:", err)
	}
	want, _ := MagicPacket("02:00:00:00:00:0b")
	if !bytes.Equal(buf[:n], want) {
		t.Fatalf("packet: %x", buf[:n])
	}
}

// TestRule: the rule names the user and the six logind actions, and refuses a name that would
// break out of the string.
func TestRule(t *testing.T) {
	r, err := Rule("john")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(r, `subject.user == "john"`) || !strings.Contains(r, "polkit.Result.YES") {
		t.Fatalf("rule: %s", r)
	}
	for _, a := range actions {
		if !strings.Contains(r, `"`+a+`"`) {
			t.Errorf("missing %s", a)
		}
	}
	for _, bad := range []string{`x"||true||"`, "", "Root User", "a;b"} {
		if _, err := Rule(bad); err == nil {
			t.Errorf("%q is refused", bad)
		}
	}
}

func TestParseWakeOn(t *testing.T) {
	if got := parseWakeOn("\tSupports Wake-on: pumbg\n\tWake-on: g\n"); got != "on (g)" {
		t.Fatal(got)
	}
	if got := parseWakeOn("\tSupports Wake-on: pumbg\n\tWake-on: d\n"); got != "off (d)" {
		t.Fatal(got)
	}
}

// handler is a paired pod, with the browser device of its account and a recorder of what runs.
type recorder struct {
	mu   sync.Mutex
	cmds [][]string
	envs [][]string
	sent []Target
}

func handler(t *testing.T) (*Handler, *identity.Account, ed25519.PrivateKey, *recorder) {
	t.Helper()
	a, err := identity.FromPhrase("lecture vapor excuse note pledge rough bundle start regular burden reveal theme banana pencil owner cube social job emotion member joy sting dash whisper")
	if err != nil {
		t.Fatal(err)
	}
	p, err := identity.Open(filepath.Join(t.TempDir(), "pod"))
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Pin(a.ID); err != nil {
		t.Fatal(err)
	}
	if err := p.SetCert(identity.NewCert(a, p.Key(), "this pod", identity.KindPod)); err != nil {
		t.Fatal(err)
	}
	rec := &recorder{}
	h := New(p, func(pod string) []Target {
		if pod == "sleeper" {
			return []Target{{MAC: "02:00:00:00:00:0b", Net: "127.0.0.0/8"}, {MAC: "02:00:00:00:00:0c", Net: "203.0.113.0/24"}}
		}
		return nil
	})
	h.Delay = 0
	h.Unit = func() bool { return true }
	h.Allowed = func() bool { return true }
	h.Start = func(cmd, env []string) error {
		rec.mu.Lock()
		rec.cmds, rec.envs = append(rec.cmds, cmd), append(rec.envs, env)
		rec.mu.Unlock()
		return nil
	}
	h.Send = func(ts []Target) error { rec.sent = append(rec.sent, ts...); return nil }
	return h, a, identity.KeyFromSeed(identity.Derive(a.Seed, "the browser")), rec
}

func signed(a *identity.Account, browser ed25519.PrivateKey, pod, action string, created time.Time) *identity.PowerRequest {
	r := &identity.PowerRequest{V: 1, Pod: pod, Action: action, Created: created.UnixMilli(), Cert: identity.NewCert(a, browser, "phone", identity.KindBrowser)}
	r.Sig = identity.SignPower(browser, r)
	return r
}

func (r *recorder) wait(t *testing.T, n int) [][]string {
	t.Helper()
	for i := 0; i < 100; i++ {
		r.mu.Lock()
		got := len(r.cmds)
		r.mu.Unlock()
		if got >= n {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([][]string{}, r.cmds...)
}

// TestRunActions: each action runs its command after the answer, the restart in a unit of its own.
func TestRunActions(t *testing.T) {
	h, a, browser, rec := handler(t)
	for _, action := range []string{identity.PowerShutdown, identity.PowerReboot, identity.PowerRestart} {
		if ans := h.Run(signed(a, browser, h.Own, action, time.Now())); ans.Status != StatusRunning {
			t.Fatalf("%s: %+v", action, ans)
		}
	}
	cmds := rec.wait(t, 3)
	if len(cmds) != 3 {
		t.Fatalf("three commands ran: %v", cmds)
	}
	all := ""
	for _, c := range cmds {
		all += strings.Join(c, " ") + "\n"
	}
	for _, want := range []string{"systemctl poweroff", "systemctl reboot", "systemd-run --user --unit=web-ide-restart-"} {
		if !strings.Contains(all, want) {
			t.Errorf("missing %q in %s", want, all)
		}
	}
}

// TestRunRefused: no rule, another pod, a replay, a bad action, a stale request, a pod that is not
// the unit.
func TestRunRefused(t *testing.T) {
	h, a, browser, rec := handler(t)
	h.Allowed = func() bool { return false }
	ans := h.Run(signed(a, browser, h.Own, identity.PowerShutdown, time.Now()))
	if ans.Status != StatusRefused || !strings.Contains(ans.Command, "allow-shutdown") {
		t.Fatalf("without the rule the answer gives the command: %+v", ans)
	}
	if ans := h.Run(signed(a, browser, h.Own, identity.PowerRestart, time.Now())); ans.Status != StatusRunning {
		t.Fatalf("the restart needs no rule: %+v", ans)
	}
	if ans := h.Run(signed(a, browser, "someone else", identity.PowerRestart, time.Now())); ans.Status != StatusRefused {
		t.Fatalf("another pod: %+v", ans)
	}
	if ans := h.Run(signed(a, browser, h.Own, "explode", time.Now())); ans.Status != StatusRefused {
		t.Fatalf("a bad action: %+v", ans)
	}
	if ans := h.Run(signed(a, browser, h.Own, identity.PowerRestart, time.Now().Add(-10*time.Minute))); ans.Status != StatusRefused {
		t.Fatalf("stale: %+v", ans)
	}
	r := signed(a, browser, h.Own, identity.PowerRestart, time.Now().Add(time.Millisecond))
	if ans := h.Run(r); ans.Status != StatusRunning {
		t.Fatalf("first: %+v", ans)
	}
	if ans := h.Run(r); ans.Status != StatusRefused {
		t.Fatalf("a replay: %+v", ans)
	}
	h.Unit = func() bool { return false }
	if ans := h.Run(signed(a, browser, h.Own, identity.PowerRestart, time.Now().Add(2*time.Millisecond))); ans.Status != StatusNotService {
		t.Fatalf("not a unit: %+v", ans)
	}
	if n := len(rec.wait(t, 2)); n != 2 {
		t.Fatalf("only the two good restarts ran: %d", n)
	}
}

// TestRunWake: the packet goes to the cards of the sleeping pod on a network of this machine; a pod
// of no shared network, and this pod itself, are refused.
func TestRunWake(t *testing.T) {
	h, a, browser, rec := handler(t)
	if !h.CanWake("sleeper") || h.CanWake("far") {
		t.Fatal("CanWake follows the shared networks")
	}
	if ans := h.Run(signed(a, browser, "sleeper", identity.PowerWake, time.Now())); ans.Status != StatusSent {
		t.Fatalf("wake: %+v", ans)
	}
	if len(rec.sent) != 1 || rec.sent[0].MAC != "02:00:00:00:00:0b" {
		t.Fatalf("only the card of the shared network: %+v", rec.sent)
	}
	if ans := h.Run(signed(a, browser, "far", identity.PowerWake, time.Now())); ans.Status != StatusRefused {
		t.Fatalf("no shared network: %+v", ans)
	}
	if ans := h.Run(signed(a, browser, h.Own, identity.PowerWake, time.Now())); ans.Status != StatusRefused {
		t.Fatalf("this pod is awake: %+v", ans)
	}
}

// TestAllowedEnv: the tests say whether the rule is there, whatever the machine has.
func TestAllowedEnv(t *testing.T) {
	t.Setenv(AllowedEnv, "1")
	if !Allowed() {
		t.Fatal("1: allowed")
	}
	t.Setenv(AllowedEnv, "0")
	if Allowed() {
		t.Fatal("0: not allowed, even with the rule installed")
	}
}
