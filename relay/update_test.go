package relay

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/DrSmithFr/web-ide/lib/identity"
)

// TestUpdateCarried: the relay carries a signed update request to the online pod of its account the
// request names, and brings the answer back to the one that asked. It is a plain carrier: it checks
// who is online and of which account, the pod the request names decides what the request is worth.
func TestUpdateCarried(t *testing.T) {
	ts := newRelay(t)
	a := testAccount(t)
	kB := identity.KeyID(podDevice(t, a, "B").key)

	aPod := registerPod(t, ts, "A", "s1", a.ID, podDevice(t, a, "A").cert, true, podDevice(t, a, "A").key, false)
	defer aPod.close()
	ready(t, aPod)
	bPod := registerPod(t, ts, "B", "s2", a.ID, podDevice(t, a, "B").cert, true, podDevice(t, a, "B").key, false)
	defer bPod.close()
	ready(t, bPod)

	browser := identity.KeyFromSeed(identity.Derive(a.Seed, "the browser"))
	req := &identity.UpdateRequest{V: 1, Pod: kB, Version: "v1.11.0", Created: time.Now().UnixMilli(),
		Cert: identity.NewCert(a, browser, "phone", identity.KindBrowser)}
	req.Sig = identity.SignUpdate(browser, req)

	aPod.send(t, sigMsg{Type: "update", Device: kB, Req: "ask-1", Update: req})
	m := nextUntil(t, bPod, "update")
	if m.Update == nil || m.Update.Pod != kB || m.Update.Version != "v1.11.0" || m.Update.Sig != req.Sig {
		t.Fatalf("the pod named by the request gets it unchanged: %+v", m)
	}
	if m.Req == "" || m.Req == "ask-1" {
		t.Fatalf("the relay hands the hop an id of its own, so another pod cannot answer for it: %q", m.Req)
	}
	bPod.send(t, sigMsg{Type: "update.done", Req: m.Req, Status: "running"})
	done := nextUntil(t, aPod, "update.done")
	if done.Status != "running" || done.Req != "ask-1" {
		t.Fatalf("the answer comes back to the one that asked, with its own id: %+v", done)
	}
}

// TestUpdateNotCarried: a device that is not online at the relay, and a pod of another account, get
// a refusal — the request never travels.
func TestUpdateNotCarried(t *testing.T) {
	ts := newRelay(t)
	a := testAccount(t)
	kB := identity.KeyID(podDevice(t, a, "B").key)

	aPod := registerPod(t, ts, "A", "s1", a.ID, podDevice(t, a, "A").cert, true, podDevice(t, a, "A").key, false)
	defer aPod.close()
	ready(t, aPod)
	bPod := registerPod(t, ts, "B", "s2", a.ID, podDevice(t, a, "B").cert, true, podDevice(t, a, "B").key, false)
	defer bPod.close()
	ready(t, bPod)

	browser := identity.KeyFromSeed(identity.Derive(a.Seed, "the browser"))
	req := &identity.UpdateRequest{V: 1, Pod: kB, Version: "v1.11.0", Created: time.Now().UnixMilli(),
		Cert: identity.NewCert(a, browser, "phone", identity.KindBrowser)}
	req.Sig = identity.SignUpdate(browser, req)

	aPod.send(t, sigMsg{Type: "update", Device: "a device that is not here", Req: "ask-2", Update: req})
	done := nextUntil(t, aPod, "update.done")
	if done.Status != "refused" || done.Req != "ask-2" {
		t.Fatalf("an offline pod is refused by the relay: %+v", done)
	}

	other, err := identity.FromPhrase("abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon art")
	if err != nil {
		t.Fatal(err)
	}
	f := podDevice(t, other, "F")
	fPod := registerPod(t, ts, "F", "sf", other.ID, f.cert, true, f.key, false)
	defer fPod.close()
	ready(t, fPod)
	fPod.send(t, sigMsg{Type: "update", Device: kB, Req: "ask-3", Update: req})
	done = nextUntil(t, fPod, "update.done")
	if done.Status != "refused" || done.Req != "ask-3" {
		t.Fatalf("a pod of another account carries nothing to this account: %+v", done)
	}
	// B never saw a request.
	quiet := make(chan sigMsg, 4)
	go func() {
		for {
			_, b, err := bPod.c.Read(context.Background())
			if err != nil {
				return
			}
			var m sigMsg
			_ = json.Unmarshal(b, &m)
			quiet <- m
		}
	}()
	deadline := time.After(500 * time.Millisecond)
	for {
		select {
		case m := <-quiet:
			if m.Type == "update" {
				t.Fatalf("the request of another account reached B: %+v", m)
			}
		case <-deadline:
			return
		}
	}
}
