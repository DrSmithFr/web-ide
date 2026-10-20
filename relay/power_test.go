package relay

import (
	"testing"
	"time"

	"github.com/DrSmithFr/web-ide/lib/identity"
)

// TestPowerCarried: a shutdown is carried to the pod it names with a hop id of the relay, and the
// answer comes back with the id of the one that asked.
func TestPowerCarried(t *testing.T) {
	ts := newRelay(t)
	a := testAccount(t)
	kB := identity.KeyID(podDevice(t, a, "B").key)
	aPod := registerPod(t, ts, "A", "s1", a.ID, podDevice(t, a, "A").cert, true, podDevice(t, a, "A").key, false)
	defer aPod.close()
	ready(t, aPod)
	bPod := registerPod(t, ts, "B", "s2", a.ID, podDevice(t, a, "B").cert, true, podDevice(t, a, "B").key, false)
	defer bPod.close()
	ready(t, bPod)

	req := powerReq(t, a, kB, identity.PowerShutdown)
	aPod.send(t, sigMsg{Type: "power", Device: kB, Req: "ask-1", Power: req})
	m := nextUntil(t, bPod, "power")
	if m.Power == nil || m.Power.Action != identity.PowerShutdown || m.Power.Sig != req.Sig || m.Req == "" || m.Req == "ask-1" {
		t.Fatalf("the pod named gets the request unchanged, under a hop id: %+v", m)
	}
	bPod.send(t, sigMsg{Type: "power.done", Req: m.Req, Status: "running"})
	if done := nextUntil(t, aPod, "power.done"); done.Status != "running" || done.Req != "ask-1" {
		t.Fatalf("the answer comes back: %+v", done)
	}

	aPod.send(t, sigMsg{Type: "power", Device: "not here", Req: "ask-2", Power: req})
	if done := nextUntil(t, aPod, "power.done"); done.Status != "refused" || done.Req != "ask-2" {
		t.Fatalf("an offline pod is refused by the relay: %+v", done)
	}
}

// TestWakeFanOut: a wake goes to every other pod of the account; the first "sent" is passed back,
// and a wake every pod refused comes back refused.
func TestWakeFanOut(t *testing.T) {
	ts := newRelay(t)
	a := testAccount(t)
	pods := map[string]*wsClient{}
	for _, n := range []string{"A", "B", "C"} {
		d := podDevice(t, a, n)
		p := registerPod(t, ts, n, "s"+n, a.ID, d.cert, true, d.key, false)
		defer p.close()
		ready(t, p)
		pods[n] = p
	}
	sleeper := identity.KeyID(podDevice(t, a, "Z").key)
	req := powerReq(t, a, sleeper, identity.PowerWake)

	pods["A"].send(t, sigMsg{Type: "power", Device: sleeper, Req: "wake-1", Power: req})
	mb := nextUntil(t, pods["B"], "power")
	mc := nextUntil(t, pods["C"], "power")
	if mb.Req == mc.Req || mb.Power.Action != identity.PowerWake {
		t.Fatalf("each pod gets a branch of its own: %+v %+v", mb, mc)
	}
	pods["B"].send(t, sigMsg{Type: "power.done", Req: mb.Req, Status: "refused", Message: "not my network"})
	pods["C"].send(t, sigMsg{Type: "power.done", Req: mc.Req, Status: "sent"})
	if done := nextUntil(t, pods["A"], "power.done"); done.Status != "sent" || done.Req != "wake-1" {
		t.Fatalf("the pod that sent the packet answers: %+v", done)
	}

	req2 := powerReq(t, a, sleeper, identity.PowerWake)
	pods["A"].send(t, sigMsg{Type: "power", Device: sleeper, Req: "wake-2", Power: req2})
	mb = nextUntil(t, pods["B"], "power")
	mc = nextUntil(t, pods["C"], "power")
	pods["B"].send(t, sigMsg{Type: "power.done", Req: mb.Req, Status: "refused"})
	pods["C"].send(t, sigMsg{Type: "power.done", Req: mc.Req, Status: "refused"})
	if done := nextUntil(t, pods["A"], "power.done"); done.Status != "refused" || done.Req != "wake-2" || done.Message == "" {
		t.Fatalf("no pod could: %+v", done)
	}
}

func powerReq(t *testing.T, a *identity.Account, pod, action string) *identity.PowerRequest {
	t.Helper()
	browser := identity.KeyFromSeed(identity.Derive(a.Seed, "the browser"))
	r := &identity.PowerRequest{V: 1, Pod: pod, Action: action, Created: time.Now().UnixMilli(), Cert: identity.NewCert(a, browser, "phone", identity.KindBrowser)}
	r.Sig = identity.SignPower(browser, r)
	return r
}
