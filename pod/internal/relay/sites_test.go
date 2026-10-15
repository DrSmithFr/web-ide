package relay

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/DrSmithFr/web-ide/pod/internal/identity"
)

// testPod is a device key of the test account and the certificate signed for it.
type testPod struct {
	key  ed25519.PrivateKey
	cert *identity.Cert
}

func podDevice(t *testing.T, a *identity.Account, name string) testPod {
	t.Helper()
	key := identity.KeyFromSeed(identity.Derive(a.Seed, "the pod "+name))
	return testPod{key: key, cert: identity.NewCert(a, key, name, identity.KindPod)}
}

// host is what the relay sees as the origin of the link (r.Host), the string a proof covers.
func host(ts *httptest.Server) string {
	u := ts.URL
	if len(u) > 7 && u[:7] == "http://" {
		return u[7:]
	}
	return u
}

// registerPod opens a pod link and registers it. With prove, it answers the challenge of the
// proof of possession with the given key (its own, or another one to fake a copied certificate).
func registerPod(t *testing.T, ts *httptest.Server, id, secret string, account string, cert *identity.Cert, prove bool, signWith ed25519.PrivateKey, preferred bool) *wsClient {
	t.Helper()
	return registerPodProto(t, ts, id, secret, account, cert, prove, signWith, preferred, 0)
}

// registerPodProto registers a pod that tells the multi-pod protocol proto (0: it tells none, the
// way a pod older than #60 does).
func registerPodProto(t *testing.T, ts *httptest.Server, id, secret string, account string, cert *identity.Cert, prove bool, signWith ed25519.PrivateKey, preferred bool, proto int) *wsClient {
	t.Helper()
	c, _, err := websocket.Dial(context.Background(), wsURL(ts, "/_relay/pod"), nil)
	if err != nil {
		t.Fatal("dialing the pod link:", err)
	}
	pc := &wsClient{c}
	pc.send(t, sigMsg{Type: "register", ID: id, Secret: secret, Name: id, Account: account,
		Cert: cert, Prove: prove, Version: "1.10.0", Build: "build-" + id, Protocol: proto, Preferred: preferred})
	if prove {
		m := pc.next(t)
		if m.Type != "challenge" || m.Nonce == "" {
			t.Fatalf("the relay must send a nonce, got %q (%s)", m.Type, m.Message)
		}
		pc.send(t, sigMsg{Type: "proof", Nonce: m.Nonce,
			Sig: identity.Sign(signWith, []byte(identity.RelayBinding(host(ts), m.Nonce)))})
	}
	return pc
}

// ready waits for the ready message of a pod link.
func ready(t *testing.T, pc *wsClient) sigMsg {
	t.Helper()
	m := pc.next(t)
	if m.Type != "ready" {
		t.Fatalf("expected ready, got %q %q", m.Type, m.Message)
	}
	return m
}

// nextUntil reads until a message of this type comes, and fails if the relay answers an error.
func nextUntil(t *testing.T, pc *wsClient, typ string) sigMsg {
	t.Helper()
	for {
		m := pc.next(t)
		if m.Type == typ {
			return m
		}
		if m.Type == "error" {
			t.Fatalf("the relay refused the pod: %q", m.Message)
		}
	}
}

// nextIdUntil reads an identity link until a message of this type comes.
func nextIdUntil(t *testing.T, c *wsClient, typ string) idMsg {
	t.Helper()
	for {
		m := c.nextId(t)
		if m.Type == typ {
			return m
		}
		if m.Type == "error" {
			t.Fatalf("the relay refused the link: %q", m.Message)
		}
	}
}

// podsUntil reads a pod link until its pods list answers `want` (the relay pushes the presence
// whenever a pod arrives, leaves, or reports its peers, so the list seen first may be the older
// one).
func podsUntil(t *testing.T, pc *wsClient, want func([]PodInfo) bool) []PodInfo {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		if time.Now().After(deadline) {
			t.Fatal("the pods list never reached what the test waited for")
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		_, b, err := pc.c.Read(ctx)
		cancel()
		if err != nil {
			t.Fatalf("reading the pod link: %v", err)
		}
		var m sigMsg
		if json.Unmarshal(b, &m) != nil {
			continue
		}
		if m.Type == "error" {
			t.Fatalf("the relay refused the pod: %q", m.Message)
		}
		if m.Type == "pods" && want(m.Pods) {
			return m.Pods
		}
	}
}

// gatewayUntil reads a pod link until the gateway message answers `want`.
func gatewayUntil(t *testing.T, pc *wsClient, want func(sigMsg) bool) sigMsg {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		if time.Now().After(deadline) {
			t.Fatal("the relay never pushed the gateway the test waited for")
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		_, b, err := pc.c.Read(ctx)
		cancel()
		if err != nil {
			t.Fatalf("reading the pod link: %v", err)
		}
		var m sigMsg
		if json.Unmarshal(b, &m) != nil {
			continue
		}
		if m.Type == "error" {
			t.Fatalf("the relay refused the pod: %q", m.Message)
		}
		if m.Type == "gateway" && want(m) {
			return m
		}
	}
}

// TestProofOfPossession: the relay sends a nonce the device signs with the key its certificate
// names. A copied certificate — the same certificate, another private key — is refused.
func TestProofOfPossession(t *testing.T) {
	ts := newRelay(t)
	a := testAccount(t)
	p := podDevice(t, a, "laptop")

	pc := registerPod(t, ts, "laptop", "s3cret", a.ID, p.cert, true, p.key, false)
	defer pc.close()
	ready(t, pc)
	m := nextUntil(t, pc, "pods")
	if len(m.Pods) != 1 || m.Pods[0].Key != identity.KeyID(p.key) || !m.Pods[0].Gateway {
		t.Fatalf("a proven pod is present and the gateway of its own site: %+v", m.Pods)
	}

	// The same certificate, presented by a device that does not hold the private key.
	stealer := identity.KeyFromSeed(identity.Derive(a.Seed, "not the pod key"))
	bad := registerPod(t, ts, "stolen", "other", a.ID, p.cert, true, stealer, false)
	defer bad.close()
	if m := bad.next(t); m.Type != "error" {
		t.Fatalf("a copied certificate must be refused, got %q %q", m.Type, m.Message)
	}
}

// TestProofOfPossessionIdentityLink: the admin link proves possession the same way; without the
// proof it keeps working (an old browser) but gets no pods list.
func TestProofOfPossessionIdentityLink(t *testing.T) {
	ts := newRelay(t)
	a := testAccount(t)
	dev := identity.KeyFromSeed(identity.DeviceSeed(a.Seed))
	cert := identity.NewCert(a, dev, "phone", identity.KindBrowser)

	c := connectIdentityProved(t, ts, a.ID, cert, dev)
	defer c.close()
	if m := c.nextId(t); m.Type != "ready" {
		t.Fatalf("the admin must be ready, got %q %q", m.Type, m.Message)
	}

	// A pod of the account: the admin sees it in its pods list.
	p := podDevice(t, a, "laptop")
	pc := registerPod(t, ts, "laptop", "s3cret", a.ID, p.cert, true, p.key, false)
	defer pc.close()
	ready(t, pc)
	m := nextIdUntil(t, c, "pods")
	for len(m.Pods) == 0 {
		m = nextIdUntil(t, c, "pods")
	}
	if len(m.Pods) != 1 || m.Pods[0].PodID != "laptop" || m.Pods[0].Name != "laptop" {
		t.Fatalf("the pods list must carry the online pod: %+v", m.Pods)
	}
}

// connectIdentityProved opens an identity link and answers the challenge of the proof.
func connectIdentityProved(t *testing.T, ts *httptest.Server, account string, cert *identity.Cert, key ed25519.PrivateKey) *wsClient {
	t.Helper()
	c, _, err := websocket.Dial(context.Background(), wsURL(ts, "/_relay/identity"), nil)
	if err != nil {
		t.Fatal("dialing the identity link:", err)
	}
	ci := &wsClient{c}
	ci.sendId(t, idMsg{Type: "register", Account: account, Cert: cert, Prove: true})
	m := ci.nextId(t)
	if m.Type != "challenge" || m.Nonce == "" {
		t.Fatalf("the relay must send a nonce, got %q (%s)", m.Type, m.Message)
	}
	ci.sendId(t, idMsg{Type: "proof", Nonce: m.Nonce,
		Sig: identity.Sign(key, []byte(identity.RelayBinding(host(ts), m.Nonce)))})
	return ci
}

// TestIdentityLinkNoProof: a link that does not announce the proof keeps the M2 behaviour and is
// told nothing about the pods.
func TestIdentityLinkNoProof(t *testing.T) {
	ts := newRelay(t)
	a := testAccount(t)
	c := connectIdentity(t, ts, a.ID, nil)
	defer c.close()
	if m := c.nextId(t); m.Type != "ready" {
		t.Fatalf("expected ready, got %q", m.Type)
	}
	c.sendId(t, idMsg{Type: "devices"})
	if m := c.nextId(t); m.Type != "devices" {
		t.Fatalf("expected the device list, got %q", m.Type)
	}
	quiet := make(chan sigMsg, 1)
	pc := registerPod(t, ts, "laptop", "s3cret", a.ID, podDevice(t, a, "laptop").cert, true, podDevice(t, a, "laptop").key, false)
	defer pc.close()
	ready(t, pc)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
		defer cancel()
		if _, b, err := c.c.Read(ctx); err == nil {
			var m sigMsg
			_ = json.Unmarshal(b, &m)
			quiet <- m
		}
	}()
	select {
	case m := <-quiet:
		t.Fatalf("a link without the proof must not get the presence of the account, got %q", m.Type)
	case <-time.After(300 * time.Millisecond):
	}
}

// TestSiteElection: the pods that report each other form one site with one gateway — the oldest
// online, the preferred one whatever its age. A pod that reports nothing is a site of its own.
func TestSiteElection(t *testing.T) {
	ts := newRelay(t)
	a := testAccount(t)
	kA, kC := identity.KeyID(podDevice(t, a, "A").key), identity.KeyID(podDevice(t, a, "C").key)

	aPod := registerPod(t, ts, "A", "s1", a.ID, podDevice(t, a, "A").cert, true, podDevice(t, a, "A").key, false)
	defer aPod.close()
	ready(t, aPod)
	bPod := registerPod(t, ts, "B", "s2", a.ID, podDevice(t, a, "B").cert, true, podDevice(t, a, "B").key, false)
	defer bPod.close()
	ready(t, bPod)
	cPod := registerPod(t, ts, "C", "s3", a.ID, podDevice(t, a, "C").cert, true, podDevice(t, a, "C").key, false)
	defer cPod.close()
	ready(t, cPod)

	kB := identity.KeyID(podDevice(t, a, "B").key)
	aPod.send(t, sigMsg{Type: "site", Peers: []string{kB}})
	pods := podsUntil(t, aPod, func(p []PodInfo) bool {
		var sa, sb string
		for _, x := range p {
			switch x.PodID {
			case "A":
				sa = x.Site
			case "B":
				sb = x.Site
			}
		}
		return len(p) == 3 && sa != "" && sa == sb
	})
	site := kA
	if kB < site {
		site = kB // A and B form a site, C is alone: the site id is the smallest key of each
	}
	if len(pods) != 3 {
		t.Fatalf("three pods online: %+v", pods)
	}
	var abGateway string
	for _, p := range pods {
		// The build of each pod's front end reaches the browsers: their Service Worker keeps the
		// page of a pod under it (#73).
		if p.Build != "build-"+p.PodID {
			t.Fatalf("the pods list carries the build of each pod: %+v", p)
		}
		if p.PodID == "C" {
			if p.Site != kC || !p.Gateway {
				t.Fatalf("C is a site of its own, its own gateway: %+v", p)
			}
			continue
		}
		if p.Site != site {
			t.Fatalf("A and B must share the site %s, got %s", site, p.Site)
		}
		if p.Gateway {
			abGateway = p.Key
		}
	}
	if abGateway != kA {
		t.Fatalf("the gateway must be the pod online the longest (A), got %s", abGateway)
	}
	g := gatewayUntil(t, bPod, func(m sigMsg) bool { return m.Gateway == kA && m.Site == site })
	if g.Site != site {
		t.Fatalf("the gateway must be pushed to the pods of the site: %+v", g)
	}
}

// TestSitePreferredGateway: a pod marked preferred is the entry point of its site, even when it
// came online after the others.
func TestSitePreferredGateway(t *testing.T) {
	ts := newRelay(t)
	a := testAccount(t)
	kB := identity.KeyID(podDevice(t, a, "B").key)

	aPod := registerPod(t, ts, "A", "s1", a.ID, podDevice(t, a, "A").cert, true, podDevice(t, a, "A").key, false)
	defer aPod.close()
	ready(t, aPod)
	bPod := registerPod(t, ts, "B", "s2", a.ID, podDevice(t, a, "B").cert, true, podDevice(t, a, "B").key, true)
	defer bPod.close()
	ready(t, bPod)
	aPod.send(t, sigMsg{Type: "site", Peers: []string{kB}})

	pods := podsUntil(t, aPod, func(p []PodInfo) bool {
		if len(p) != 2 {
			return false
		}
		for _, x := range p {
			if x.PodID == "A" && x.Gateway {
				return false
			}
		}
		return true
	})
	for _, p := range pods {
		if p.PodID == "B" && !p.Gateway {
			t.Fatalf("the preferred pod is the gateway: %+v", p)
		}
		if p.PodID == "A" && p.Gateway {
			t.Fatalf("the preferred pod takes the role from the oldest: %+v", p)
		}
	}
}

// TestSiteGraceFailover: a gateway whose link drops without saying bye keeps the role for the
// grace, then the site moves; a pod that says bye moves it at once.
func TestSiteGraceFailover(t *testing.T) {
	siteGrace = 300 * time.Millisecond
	t.Cleanup(func() { siteGrace = 10 * time.Second })
	ts := newRelay(t)
	a := testAccount(t)
	kA, kB := identity.KeyID(podDevice(t, a, "A").key), identity.KeyID(podDevice(t, a, "B").key)

	aPod := registerPod(t, ts, "A", "s1", a.ID, podDevice(t, a, "A").cert, true, podDevice(t, a, "A").key, false)
	ready(t, aPod)
	// A reports its LAN peer before B registers: the site forms as B comes up, so B is never
	// pushed as the gateway of a one-pod site — a message about the state before the site would
	// otherwise sit on the link and be read as if it were about the drop watched here.
	aPod.send(t, sigMsg{Type: "site", Peers: []string{kB}})
	bPod := registerPod(t, ts, "B", "s2", a.ID, podDevice(t, a, "B").cert, true, podDevice(t, a, "B").key, false)
	defer bPod.close()
	ready(t, bPod)
	// Wait until the site is really there, with A (the oldest link) as its gateway.
	gatewayUntil(t, bPod, func(m sigMsg) bool { return m.Gateway == kA })

	// A drops without a bye: B keeps seeing A as the gateway until the grace ends. The window
	// watched is half of the grace: the relay starts its own clock when it notices the drop, a
	// hair after this timer, and a move seen right at its end would be a tie between the two.
	grace := time.After(siteGrace / 2)
	aPod.close()
	quiet := make(chan sigMsg, 8)
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
	// Nothing moves during the grace.
loop:
	for {
		select {
		case m := <-quiet:
			if m.Type == "gateway" && m.Gateway == kB {
				t.Fatalf("a gateway that dropped without a bye keeps the role during the grace: %+v", m)
			}
		case <-grace:
			break loop
		}
	}
	// When it ends, the site elects the pod still online.
	end := time.After(3 * time.Second)
	for {
		select {
		case m := <-quiet:
			if m.Type == "gateway" && m.Gateway == kB {
				return
			}
		case <-end:
			t.Fatal("the gateway role must move when the grace ends")
		}
	}
}

// TestSiteProtocolGateway: a pod of another multi-pod protocol is listed with its version and is
// never elected gateway — a browser entering through it could not route the pods of its site. The
// protocol of the site is the one its pods mostly run.
func TestSiteProtocolGateway(t *testing.T) {
	ts := newRelay(t)
	a := testAccount(t)
	kA, kB := identity.KeyID(podDevice(t, a, "A").key), identity.KeyID(podDevice(t, a, "B").key)
	kC := identity.KeyID(podDevice(t, a, "C").key)

	aPod := registerPodProto(t, ts, "A", "s1", a.ID, podDevice(t, a, "A").cert, true, podDevice(t, a, "A").key, false, 1)
	defer aPod.close()
	ready(t, aPod)
	bPod := registerPodProto(t, ts, "B", "s2", a.ID, podDevice(t, a, "B").cert, true, podDevice(t, a, "B").key, false, 1)
	defer bPod.close()
	ready(t, bPod)
	// C speaks a newer protocol and is marked preferred: without the rule it would be the gateway.
	cPod := registerPodProto(t, ts, "C", "s3", a.ID, podDevice(t, a, "C").cert, true, podDevice(t, a, "C").key, true, 2)
	defer cPod.close()
	ready(t, cPod)
	for _, p := range []*wsClient{aPod, bPod} {
		p.send(t, sigMsg{Type: "site", Peers: []string{kA, kB, kC}})
	}

	pods := podsUntil(t, aPod, func(p []PodInfo) bool {
		var sa, sb, sc string
		for _, x := range p {
			switch x.PodID {
			case "A":
				sa = x.Site
			case "B":
				sb = x.Site
			case "C":
				sc = x.Site
			}
		}
		return len(p) == 3 && sa != "" && sa == sb && sa == sc
	})
	for _, p := range pods {
		switch p.PodID {
		case "C":
			if p.Gateway {
				t.Fatalf("a pod of another protocol is never the gateway of the site: %+v", p)
			}
			if p.Protocol != 2 || p.Mismatch != "newer" {
				t.Fatalf("the pod of another protocol is listed with its protocol and what it means: %+v", p)
			}
		default:
			if p.Protocol != 1 || p.Mismatch != "" {
				t.Fatalf("the pods of the protocol of the site carry no mismatch: %+v", p)
			}
		}
	}
	var gateway string
	for _, p := range pods {
		if p.Gateway {
			gateway = p.PodID
		}
	}
	if gateway != "A" {
		t.Fatalf("the gateway must be a pod of the protocol of the site (A), got %s", gateway)
	}
}

// TestSiteProtocolTie: when the pods of a site split evenly, the highest protocol wins — the site
// runs the newest build the majority can agree on, and the older pods are the ones to update.
func TestSiteProtocolTie(t *testing.T) {
	ts := newRelay(t)
	a := testAccount(t)
	kB := identity.KeyID(podDevice(t, a, "B").key)

	aPod := registerPodProto(t, ts, "A", "s1", a.ID, podDevice(t, a, "A").cert, true, podDevice(t, a, "A").key, false, 1)
	defer aPod.close()
	ready(t, aPod)
	bPod := registerPodProto(t, ts, "B", "s2", a.ID, podDevice(t, a, "B").cert, true, podDevice(t, a, "B").key, false, 2)
	defer bPod.close()
	ready(t, bPod)
	aPod.send(t, sigMsg{Type: "site", Peers: []string{kB}})

	pods := podsUntil(t, aPod, func(p []PodInfo) bool {
		if len(p) != 2 {
			return false
		}
		for _, x := range p {
			if x.Mismatch != "" {
				return true
			}
		}
		return false
	})
	for _, p := range pods {
		if p.PodID == "B" && (!p.Gateway || p.Mismatch != "") {
			t.Fatalf("the highest protocol wins the tie, B runs it and is the gateway: %+v", p)
		}
		if p.PodID == "A" && (p.Gateway || p.Mismatch != "outdated") {
			t.Fatalf("A runs the older protocol: it is listed as outdated and kept out of the role: %+v", p)
		}
	}
}

// TestSiteForeignEdge: an edge a pod reports to a key that is not a device of the account online
// here joins nothing — the relay does not trust what a pod says about the network.
func TestSiteForeignEdge(t *testing.T) {
	ts := newRelay(t)
	a := testAccount(t)
	other, err := identity.FromPhrase("abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon art")
	if err != nil {
		t.Fatal(err)
	}
	foreign := podDevice(t, other, "F")

	aPod := registerPod(t, ts, "A", "s1", a.ID, podDevice(t, a, "A").cert, true, podDevice(t, a, "A").key, false)
	defer aPod.close()
	ready(t, aPod)
	fPod := registerPod(t, ts, "F", "sf", other.ID, foreign.cert, true, foreign.key, false)
	defer fPod.close()
	ready(t, fPod)

	aPod.send(t, sigMsg{Type: "site", Peers: []string{identity.KeyID(foreign.key)}})
	time.Sleep(200 * time.Millisecond)
	aPod.send(t, sigMsg{Type: "site", Peers: []string{}})
	m := nextUntil(t, aPod, "pods")
	if len(m.Pods) != 1 || m.Pods[0].Site != identity.KeyID(podDevice(t, a, "A").key) {
		t.Fatalf("A must stay a site of its own: %+v", m.Pods)
	}
}

// TestIdentityLinkSignal: a page signals its tunnel on its identity link (#75), and opens the
// session before it proved itself: the relay carries the signaling while it waits for the proof,
// and the proof still lands. An offline pod is said so on the session.
func TestIdentityLinkSignal(t *testing.T) {
	ts := newRelay(t)
	a := testAccount(t)
	p := podDevice(t, a, "laptop")
	pod := registerPod(t, ts, "laptop", "s3cret", a.ID, p.cert, true, p.key, false)
	defer pod.close()
	ready(t, pod)

	dev := identity.KeyFromSeed(identity.DeviceSeed(a.Seed))
	cert := identity.NewCert(a, dev, "phone", identity.KindBrowser)
	conn, _, err := websocket.Dial(context.Background(), wsURL(ts, "/_relay/identity"), nil)
	if err != nil {
		t.Fatal(err)
	}
	c := &wsClient{conn}
	defer c.close()
	c.sendId(t, idMsg{Type: "register", Account: a.ID, Cert: cert, Prove: true})
	// The tunnel is opened at once, before the challenge is even read.
	c.send(t, sigMsg{Type: "signal", Pod: "laptop", Session: "s1"})
	c.send(t, sigMsg{Type: "offer", Session: "s1", SDP: "v=0 offer"})
	c.send(t, sigMsg{Type: "signal", Pod: "nobody", Session: "s2"})

	var nonce string
	var ice, offline bool
	for nonce == "" || !ice || !offline {
		m := c.next(t)
		switch {
		case m.Type == "challenge":
			nonce = m.Nonce
		case m.Type == "ice" && m.Session == "s1":
			ice = true
		case m.Type == "error" && m.Session == "s2" && m.Message == "offline":
			offline = true
		default:
			t.Fatalf("unexpected %q (%s) on the link", m.Type, m.Message)
		}
	}
	if o := nextUntil(t, pod, "offer"); o.Session != "s1" || o.SDP != "v=0 offer" {
		t.Fatalf("the pod gets the offer of the session: %+v", o)
	}
	c.sendId(t, idMsg{Type: "proof", Nonce: nonce, Sig: identity.Sign(dev, []byte(identity.RelayBinding(host(ts), nonce)))})
	if m := nextIdUntil(t, c, "ready"); m.Account != a.ID {
		t.Fatalf("the link proved itself after its signaling: %+v", m)
	}
	// The pod answers and trickles: both reach the page on its identity link, with the session.
	pod.send(t, sigMsg{Type: "answer", Session: "s1", SDP: "v=0 answer"})
	pod.send(t, sigMsg{Type: "candidate", Session: "s1", Candidate: []byte(`"{\"candidate\":\"c\"}"`)})
	var answer, cand bool
	for !answer || !cand {
		m := c.next(t)
		if m.Session != "s1" {
			continue // the pods list of the account
		}
		answer = answer || (m.Type == "answer" && m.SDP == "v=0 answer")
		cand = cand || m.Type == "candidate"
	}
}

// TestIdentityLinkEarlySession: a page that knows its pod names it in the URL of its identity
// link (#75): the ICE servers of the session are the first thing the link says, before anything
// is sent on it.
func TestIdentityLinkEarlySession(t *testing.T) {
	ts := newRelay(t)
	a := testAccount(t)
	p := podDevice(t, a, "laptop")
	pod := registerPod(t, ts, "laptop", "s3cret", a.ID, p.cert, true, p.key, false)
	defer pod.close()
	ready(t, pod)
	conn, _, err := websocket.Dial(context.Background(), wsURL(ts, "/_relay/identity?pod=laptop&session=s9"), nil)
	if err != nil {
		t.Fatal(err)
	}
	c := &wsClient{conn}
	defer c.close()
	if m := c.next(t); m.Type != "ice" || m.Session != "s9" {
		t.Fatalf("the link opens with the ICE servers of its session, got %q %q", m.Type, m.Session)
	}
	c.send(t, sigMsg{Type: "offer", Session: "s9", SDP: "v=0 early"})
	if o := nextUntil(t, pod, "offer"); o.Session != "s9" || o.SDP != "v=0 early" {
		t.Fatalf("the offer of the early session reaches the pod: %+v", o)
	}
}
