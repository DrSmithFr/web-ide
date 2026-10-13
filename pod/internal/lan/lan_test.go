package lan

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/DrSmithFr/web-ide/pod/internal/identity"
	"github.com/DrSmithFr/web-ide/pod/internal/update"
)

// account makes an account from a fixed entropy byte.
func account(t *testing.T, b byte) *identity.Account {
	t.Helper()
	e := make([]byte, 32)
	for i := range e {
		e[i] = b
	}
	phrase, err := identity.Generate(e)
	if err != nil {
		t.Fatal(err)
	}
	a, err := identity.FromPhrase(phrase)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

// podIdentity is a pod paired with the account a (nil: an unpaired pod).
func podIdentity(t *testing.T, a *identity.Account, name string) (*identity.PodIdentity, string) {
	t.Helper()
	dir := t.TempDir()
	p, err := identity.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if a != nil {
		if err := p.Pin(a.ID); err != nil {
			t.Fatal(err)
		}
		if err := p.SetCert(identity.NewCert(a, p.Key(), name, identity.KindPod)); err != nil {
			t.Fatal(err)
		}
	}
	return p, dir
}

// podHandler is what a pod serves to a trusted peer: its name, and a projects.list on /ws.
func podHandler(name string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/ws" {
			_, _ = io.WriteString(w, "pod "+name+" "+r.URL.Path+" "+r.Header.Get(BaseHeader))
			return
		}
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer c.CloseNow()
		for {
			_, b, err := c.Read(r.Context())
			if err != nil {
				return
			}
			var req struct {
				ID int `json:"id"`
			}
			_ = json.Unmarshal(b, &req)
			out, _ := json.Marshal(map[string]any{"id": req.ID, "result": []map[string]string{{"name": "project of " + name}}})
			_ = c.Write(r.Context(), websocket.MessageText, out)
		}
	})
}

type pod struct {
	m   *Manager
	id  *identity.PodIdentity
	dir string
}

func startPod(t *testing.T, a *identity.Account, name string, peers ...string) *pod {
	t.Helper()
	id, dir := podIdentity(t, a, name)
	m, err := New(Config{Dir: dir, Addr: "127.0.0.1:0", Identity: id, Name: name, Version: "test",
		Handler: podHandler(name), Peers: peers, Tick: 50 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	m.Start(ctx)
	return &pod{m: m, id: id, dir: dir}
}

// which names the word the page of the gateway must use for that peer.
func wantWord(name string) string {
	if name == "older" {
		return "older version"
	}
	return "newer version"
}

// startPodProto starts a pod of the account that speaks another multi-pod protocol (0: it tells
// none, the way a pod older than #60 does).
func startPodProto(t *testing.T, a *identity.Account, name string, proto int, peers ...string) *pod {
	t.Helper()
	id, dir := podIdentity(t, a, name)
	m, err := New(Config{Dir: dir, Addr: "127.0.0.1:0", Identity: id, Name: name, Version: "test", Protocol: proto,
		Handler: podHandler(name), Peers: peers, Tick: 50 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	m.Start(ctx)
	return &pod{m: m, id: id, dir: dir}
}

func (p *pod) addr() string { return p.m.Status().Listen }

// waitFor polls the status of a pod until cond holds.
func waitFor(t *testing.T, p *pod, what string, cond func(Status) bool) Status {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		st := p.m.Status()
		if cond(st) {
			return st
		}
		if time.Now().After(deadline) {
			b, _ := json.MarshalIndent(st, "", "  ")
			t.Fatalf("%s: %s", what, b)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func online(st Status, name string) *PeerView {
	for i := range st.Peers {
		if st.Peers[i].Name == name && st.Peers[i].Online {
			return &st.Peers[i]
		}
	}
	return nil
}

func failed(st Status, addr, code string) bool {
	for _, f := range st.Failed {
		if f.Addr == addr && f.Error == code {
			return true
		}
	}
	return false
}

func TestTwoPodsReachEachOther(t *testing.T) {
	a := account(t, 1)
	b := startPod(t, a, "bravo")
	al := startPod(t, a, "alpha", b.addr())

	st := waitFor(t, al, "alpha sees bravo with its projects", func(st Status) bool {
		p := online(st, "bravo")
		return p != nil && p.Path != "" && strings.Contains(string(p.Projects), "project of bravo")
	})
	// bravo was dialed: it dials back on the port alpha told.
	waitFor(t, b, "bravo sees alpha", func(st Status) bool { return online(st, "alpha") != nil })

	// alpha serves bravo under its path, WebSocket included.
	base := online(st, "bravo").Path
	gw := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { al.m.ServeGateway(w, r, "") }))
	defer gw.Close()
	res, err := http.Get(gw.URL + base + "/project/x")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if string(body) != "pod bravo /project/x "+base {
		t.Fatalf("gateway answered %q", body)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(gw.URL, "http")+base+"/ws", &websocket.DialOptions{
		HTTPHeader: http.Header{"Origin": {gw.URL}}})
	if err != nil {
		t.Fatal(err)
	}
	_ = c.Write(ctx, websocket.MessageText, []byte(`{"id":7,"method":"projects.list"}`))
	_, b2, err := c.Read(ctx)
	if err != nil || !strings.Contains(string(b2), "project of bravo") {
		t.Fatalf("websocket through the gateway: %s %v", b2, err)
	}
	c.CloseNow()

	// The page of bravo asks who serves it.
	res, err = http.Get(gw.URL + base + GatewayPath)
	if err != nil {
		t.Fatal(err)
	}
	body, _ = io.ReadAll(res.Body)
	res.Body.Close()
	if !strings.Contains(string(body), `"name":"alpha"`) {
		t.Fatalf("gateway path: %s", body)
	}
}

// TestUpdateOverTheLAN: a peer carries a signed update request to the pod it names over the control
// link; the target runs its own checks and answers before it restarts. A request signed by a device
// of another account is refused, and the command reaches the page instead.
func TestUpdateOverTheLAN(t *testing.T) {
	a := account(t, 1)
	marker := filepath.Join(t.TempDir(), "updated")
	t.Setenv(update.UpdateCmdEnv, "touch "+marker)

	id, dir := podIdentity(t, a, "bravo")
	handler := update.New(id)
	bravoMgr, err := New(Config{Dir: dir, Addr: "127.0.0.1:0", Identity: id, Name: "bravo", Version: "test", Protocol: 1,
		Handler: podHandler("bravo"), Tick: 50 * time.Millisecond, OnUpdate: handler.Run})
	if err != nil {
		t.Fatal(err)
	}
	bctx, bcancel := context.WithCancel(context.Background())
	t.Cleanup(bcancel)
	bravoMgr.Start(bctx)

	al := startPodProto(t, a, "alpha", 1, bravoMgr.Status().Listen)
	waitFor(t, al, "alpha sees bravo", func(st Status) bool { return online(st, "bravo") != nil })

	browser := identity.KeyFromSeed(identity.Derive(a.Seed, "the browser"))
	req := &identity.UpdateRequest{V: 1, Pod: id.ID(), Version: "v1.11.0", Created: time.Now().UnixMilli(),
		Cert: identity.NewCert(a, browser, "phone", identity.KindBrowser)}
	req.Sig = identity.SignUpdate(browser, req)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	ans, err := al.m.SendUpdate(ctx, identity.KeyID(id.Key()), req)
	if err != nil {
		t.Fatal("carrying the request:", err)
	}
	if ans.Status != update.StatusRunning {
		t.Fatalf("bravo ran the request of a device of its account: %+v", ans)
	}
	for i := 0; i < 100 && !exists(marker); i++ {
		time.Sleep(20 * time.Millisecond)
	}
	if !exists(marker) {
		t.Fatal("the install command ran on the pod the request named")
	}

	// A request signed by a device of another account never reaches the installer.
	foreign := account(t, 2)
	intruder := identity.KeyFromSeed(identity.Derive(foreign.Seed, "a browser of another account"))
	bad := &identity.UpdateRequest{V: 1, Pod: id.ID(), Version: "v1.11.0", Created: time.Now().UnixMilli(),
		Cert: identity.NewCert(foreign, intruder, "intruder", identity.KindBrowser)}
	bad.Sig = identity.SignUpdate(intruder, bad)
	ans, err = al.m.SendUpdate(ctx, identity.KeyID(id.Key()), bad)
	if err != nil {
		t.Fatal("carrying the request:", err)
	}
	if ans.Status != update.StatusRefused || ans.Message == "" {
		t.Fatalf("the target pod refuses a request it does not trust: %+v", ans)
	}
}

func exists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// TestProtocolMismatch: a peer that does not speak the multi-pod protocol of this pod is listed
// with its protocol, its page is not served under the gateway path (the page says what to update),
// and no channel of it is opened. A pod that tells no protocol at all is the older one.
func TestProtocolMismatch(t *testing.T) {
	a := account(t, 1)
	newer := startPodProto(t, a, "newer", 2)
	older := startPodProto(t, a, "older", 0)
	al := startPodProto(t, a, "alpha", 1, newer.addr(), older.addr())

	st := waitFor(t, al, "alpha sees both peers", func(st Status) bool {
		return online(st, "newer") != nil && online(st, "older") != nil
	})
	if p := online(st, "newer"); p.Protocol != 2 {
		t.Fatalf("the peer tells its protocol: %+v", p)
	}
	if p := online(st, "older"); p.Protocol != 0 {
		t.Fatalf("a peer that tells none counts as protocol 0: %+v", p)
	}

	gw := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { al.m.ServeGateway(w, r, "") }))
	defer gw.Close()
	for _, name := range []string{"newer", "older"} {
		base := online(st, name).Path
		res, err := http.Get(gw.URL + base + "/project/x")
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(res.Body)
		res.Body.Close()
		if res.StatusCode != http.StatusBadGateway || !strings.Contains(string(body), "Update this pod") {
			t.Fatalf("the gateway of %s answers the page that says what to update: %d %s", name, res.StatusCode, body)
		}
		if !strings.Contains(string(body), wantWord(name)) {
			t.Fatalf("the page names which pod is behind: %s", body)
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for _, p := range st.Peers {
		if _, err := al.m.OpenTunnel(ctx, p.ID, "window", "rpc", nil, "fb", "fp"); err == nil {
			t.Fatalf("a pod of another protocol is never routed to: %s", p.Name)
		}
	}
}

func TestForeignAndUnpairedPodsAreRefused(t *testing.T) {
	a := account(t, 1)
	foreign := startPod(t, account(t, 2), "foreign")
	// An unpaired pod opens no LAN port at all.
	unpaired := startPod(t, nil, "unpaired")
	if unpaired.addr() != "" {
		t.Fatalf("an unpaired pod listens on %s", unpaired.addr())
	}
	al := startPod(t, a, "alpha", foreign.addr())

	waitFor(t, al, "alpha refuses foreign", func(st Status) bool {
		return failed(st, foreign.addr(), "foreign") && len(st.Peers) == 0
	})
	if st := foreign.m.Status(); len(st.Peers) != 0 {
		t.Fatalf("foreign trusted alpha: %+v", st.Peers)
	}

	// Without a control link, the LAN port refuses the requests of a pod.
	cli := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{Certificates: []tls.Certificate{al.m.cert}, InsecureSkipVerify: true}}}
	res, err := cli.Get("https://" + foreign.addr() + "/anything")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("an untrusted request got %d", res.StatusCode)
	}
}

func TestRevocationSpreadsOverTheLAN(t *testing.T) {
	a := account(t, 1)
	c := startPod(t, a, "charlie")
	b := startPod(t, a, "bravo", c.addr())
	al := startPod(t, a, "alpha", b.addr(), c.addr())
	for _, p := range []*pod{al, b} {
		waitFor(t, p, "charlie online", func(st Status) bool { return online(st, "charlie") != nil })
	}
	waitFor(t, al, "bravo online", func(st Status) bool { return online(st, "bravo") != nil })

	// The pair command revokes charlie on alpha (another process writes identity.json).
	cli, err := identity.Open(al.dir)
	if err != nil {
		t.Fatal(err)
	}
	r := &identity.Revocations{Seq: 1, Epoch: 1, Revoked: []string{c.id.ID()}, Created: time.Now().Unix()}
	if err := identity.SignRevocations(a.Priv, r); err != nil {
		t.Fatal(err)
	}
	time.Sleep(20 * time.Millisecond) // a later modification time than the one alpha read
	if err := cli.ApplyRevocations(r); err != nil {
		t.Fatal(err)
	}

	waitFor(t, al, "alpha drops charlie", func(st Status) bool { return len(st.Peers) == 1 })
	// bravo got the list from alpha, with no relay.
	waitFor(t, b, "bravo drops charlie", func(st Status) bool { return len(st.Peers) == 1 })
	if _, _, rev := b.id.State(); rev == nil || rev.Seq != 1 {
		t.Fatalf("bravo holds %+v", rev)
	}
	waitFor(t, al, "charlie refused", func(st Status) bool { return failed(st, c.addr(), "revoked") })
	// bravo stays reached.
	time.Sleep(time.Second)
	if st := al.m.Status(); online(st, "bravo") == nil {
		b, _ := json.Marshal(st)
		t.Fatalf("alpha lost bravo: %s", b)
	}
}

// A machine in the middle shows its own TLS certificate to both pods and relays the bytes:
// the signatures bind both fingerprints, so the handshake fails on both sides.
func TestMachineInTheMiddleIsRefused(t *testing.T) {
	a := account(t, 1)
	b := startPod(t, a, "bravo")
	mitmCert, err := LoadCert(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ln, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{Certificates: []tls.Certificate{mitmCert}, ClientAuth: tls.RequireAnyClientCert})
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			in, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				out, err := tls.Dial("tcp", b.addr(), &tls.Config{Certificates: []tls.Certificate{mitmCert}, InsecureSkipVerify: true})
				if err != nil {
					in.Close()
					return
				}
				go func() { _, _ = io.Copy(out, in); out.Close() }()
				_, _ = io.Copy(in, out)
				in.Close()
			}()
		}
	}()
	al := startPod(t, a, "alpha", ln.Addr().String())
	waitFor(t, al, "the relayed handshake fails", func(st Status) bool {
		return failed(st, ln.Addr().String(), "proof") && len(st.Peers) == 0
	})
	if st := b.m.Status(); len(st.Peers) != 0 {
		t.Fatalf("bravo trusted the machine in the middle: %+v", st.Peers)
	}
}

// A pod paired while it runs (the pair command) opens its LAN port and is reached.
func TestPairedWhileRunning(t *testing.T) {
	a := account(t, 1)
	b := startPod(t, nil, "bravo")
	cli, err := identity.Open(b.dir)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(20 * time.Millisecond)
	if err := cli.Pin(a.ID); err != nil {
		t.Fatal(err)
	}
	if err := cli.SetCert(identity.NewCert(a, cli.Key(), "bravo", identity.KindPod)); err != nil {
		t.Fatal(err)
	}
	waitFor(t, b, "bravo listens once paired", func(st Status) bool { return st.Listen != "" && st.Paired })
	al := startPod(t, a, "alpha", b.addr())
	waitFor(t, al, "alpha reaches bravo", func(st Status) bool { return online(st, "bravo") != nil })
}

func TestManualAddresses(t *testing.T) {
	a := account(t, 1)
	b := startPod(t, a, "bravo")
	al := startPod(t, a, "alpha")
	if err := al.m.SetManual([]string{"not an address"}); err == nil {
		t.Fatal("a bad address was accepted")
	}
	if err := al.m.SetManual([]string{b.addr()}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, al, "bravo reached by its typed address", func(st Status) bool {
		return online(st, "bravo") != nil && len(st.Manual) == 1
	})
}

// fakeResolver is an mDNS network in memory.
type fakeResolver struct {
	mu  sync.Mutex
	net map[string]Found
}

func (f *fakeResolver) Announce(port int, r Record) (func(), error) {
	addr := net.JoinHostPort("127.0.0.1", itoa(port))
	f.mu.Lock()
	defer f.mu.Unlock()
	f.net[addr] = Found{Addr: addr, Pod: r.Pod, Account: r.Account}
	return func() {
		f.mu.Lock()
		defer f.mu.Unlock()
		delete(f.net, addr)
	}, nil
}

func (f *fakeResolver) Browse(context.Context) ([]Found, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []Found
	for _, v := range f.net {
		out = append(out, v)
	}
	return out, nil
}

// Discovery: the pods of the account find each other; a pod of another account is not even
// dialed, since its announcement carries another tag.
func TestDiscoveryFindsThePodsOfTheAccount(t *testing.T) {
	r := &fakeResolver{net: map[string]Found{}}
	a := account(t, 1)
	start := func(acc *identity.Account, name string) *pod {
		id, dir := podIdentity(t, acc, name)
		m, err := New(Config{Dir: dir, Addr: "127.0.0.1:0", Identity: id, Name: name, Handler: podHandler(name),
			Resolver: r, Tick: 50 * time.Millisecond})
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		t.Cleanup(cancel)
		m.Start(ctx)
		return &pod{m: m, id: id, dir: dir}
	}
	al := start(a, "alpha")
	b := start(a, "bravo")
	foreign := start(account(t, 2), "foreign")
	waitFor(t, al, "alpha finds bravo", func(st Status) bool { return online(st, "bravo") != nil })
	waitFor(t, b, "bravo finds alpha", func(st Status) bool { return online(st, "alpha") != nil })
	time.Sleep(300 * time.Millisecond)
	if st := al.m.Status(); len(st.Failed) != 0 || len(st.Peers) != 1 {
		t.Fatalf("alpha: %+v", st)
	}
	if st := foreign.m.Status(); len(st.Peers) != 0 {
		t.Fatalf("foreign: %+v", st.Peers)
	}
}

func itoa(n int) string { return strconv.Itoa(n) }

// The real mDNS responder answers the browse of the same machine (skipped where multicast
// is not available, as in some containers).
func TestMDNSAnnounceAndBrowse(t *testing.T) {
	r := MDNS{}
	tag := AccountTag("test-" + strconv.FormatInt(time.Now().UnixNano(), 36))
	stop, err := r.Announce(45678, Record{Pod: "abcdef123456", Account: tag})
	if err != nil {
		t.Skipf("no multicast: %v", err)
	}
	defer stop()
	for i := 0; i < 3; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		found, err := r.Browse(ctx)
		cancel()
		if err != nil {
			t.Skipf("no multicast: %v", err)
		}
		for _, f := range found {
			if f.Account == tag && f.Pod == "abcdef123456" && strings.HasSuffix(f.Addr, ":45678") {
				return
			}
		}
	}
	t.Skip("the announcement was not heard back (multicast loopback off)")
}
