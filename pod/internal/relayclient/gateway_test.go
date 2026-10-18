package relayclient

// Gateway routing (milestone 3b): a browser tunneled to a gateway pod reaches a pod of its cluster
// that has no link of its own. Each channel of the end pod is a channel of the browser's tunnel
// labelled @<device key>/<label>: the gateway pipes it over the LAN link (/_lan/tunnel), the end
// pod runs the handshake with the browser's proof bound to the fingerprints of the browser's own
// tunnel, and checks the certificate the gateway vouches for against the list *it* holds.

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pion/webrtc/v4"

	"github.com/DrSmithFr/web-ide/pod/internal/identity"
	"github.com/DrSmithFr/web-ide/pod/internal/lan"
	"github.com/DrSmithFr/web-ide/pod/internal/tunnel"
)

// gateway is a gateway pod with a relay link, and a pod of its cluster with none: the two are
// linked by their LAN managers, the gateway is the browser's entry point.
type gateway struct {
	env   *testEnv
	gw    *lan.Manager
	end   *lan.Manager
	key   string // the device key of the end pod
	idB   *identity.PodIdentity
	token string // the token of the end pod (its own server)
}

// startGateway pairs two pods with the account of the test vectors, links them on the LAN, and
// gives the gateway its relay link (with the route to its peers) and the end pod none.
func startGateway(t *testing.T) (*gateway, *identity.Account) {
	t.Helper()
	a, err := identity.FromPhrase(vectorsPhrase)
	if err != nil {
		t.Fatal(err)
	}
	podA, _ := podIdentity(t, a, "alpha")
	podB, dirB := podIdentity(t, a, "bravo")

	// The end pod: a real server, its LAN port serving the bridged windows.
	sB, _ := buildPod(t)
	mB, err := lan.New(lan.Config{Dir: dirB, Addr: "127.0.0.1:0", Identity: podB, Name: "bravo", Version: "1",
		Handler: http.HandlerFunc(sB.ServeTrusted), ServeTunnel: sB.ServeTunnel, Tick: 50 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	mB.Start(ctx)

	// The gateway pod: the relay link, the LAN link, and the route to its peers.
	sA, _ := buildPod(t)
	mA, err := lan.New(lan.Config{Dir: t.TempDir(), Addr: "127.0.0.1:0", Identity: podA, Name: "alpha", Version: "1",
		Handler: http.HandlerFunc(sA.ServeTrusted), ServeTunnel: sA.ServeTunnel, Peers: []string{mB.Status().Listen}, Tick: 50 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	mA.Start(ctx)
	if !seePeer(mA, podB.ID()) {
		t.Fatal("the gateway does not see the end pod on its LAN")
	}

	env := startEnv(t, podA, lanRoute{mA})
	env.pod = sA
	return &gateway{env: env, gw: mA, end: mB, key: podB.ID(), idB: podB, token: podToken}, a
}

// podIdentity is a pod paired with the account a, with its own device key.
func podIdentity(t *testing.T, a *identity.Account, name string) (*identity.PodIdentity, string) {
	t.Helper()
	dir := t.TempDir()
	p, err := identity.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Pin(a.ID); err != nil {
		t.Fatal(err)
	}
	if err := p.SetCert(identity.NewCert(a, p.Key(), name, identity.KindPod)); err != nil {
		t.Fatal(err)
	}
	return p, dir
}

// lanRoute plays pod/internal/multipod.Route over the LAN link of a pod.
type lanRoute struct{ m *lan.Manager }

func (r lanRoute) Open(ctx context.Context, key, window, label string, via *identity.Cert, fpBrowser, fpPod string) (tunnel.MsgConn, error) {
	return r.m.OpenTunnel(ctx, key, window, label, via, fpBrowser, fpPod)
}

// seePeer reports whether a manager reached a pod of the account on its LAN.
func seePeer(m *lan.Manager, key string) bool {
	for i := 0; i < 200; i++ {
		for _, p := range m.Status().Peers {
			if p.ID == key && p.Online {
				return true
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	return false
}

// browserDevice is a browser of the account: its own key, its certificate.
func browserDevice(t *testing.T, a *identity.Account) (ed25519.PrivateKey, *identity.Cert) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	return priv, identity.NewCertForPub(a, pub, "browser", identity.KindBrowser)
}

// openRouted opens the rpc channel of another pod on this window and runs the handshake with it:
// the proof is bound to the fingerprints of the browser's own tunnel, and the certificate the end
// pod answers with must be that pod's.
func openRouted(t *testing.T, pc *webrtc.PeerConnection, key string, key_ ed25519.PrivateKey, cert *identity.Cert, account, fpB, fpP string) *tunnel.Framed {
	t.Helper()
	dc := openTestChannel(t, pc, RoutedLabel+key+"/rpc")
	podCert, err := tunnel.RunBrowserCert(dc, key_, cert, account, fpB, fpP, nonceHex())
	if err != nil {
		t.Fatalf("handshake with the end pod: %v", err)
	}
	if podCert.Key != key {
		t.Fatalf("the end pod is %s, wanted %s", podCert.Key, key)
	}
	return dc
}

func TestGatewayRouting(t *testing.T) {
	g, a := startGateway(t)
	key, cert := browserDevice(t, a)

	pc, rpc, fpB, fpP, err := connectBrowser(t, g.env, "gw-1", sigURL(g.env, "gw-1"))
	if err != nil {
		t.Fatal(err)
	}
	// The browser authenticates the gateway itself: hop by hop.
	if _, err := tunnel.RunBrowserCert(rpc, key, cert, a.ID, fpB, fpP, nonceHex()); err != nil {
		t.Fatalf("handshake with the gateway: %v", err)
	}

	routed := openRouted(t, pc, g.key, key, cert, a.ID, fpB, fpP)
	hello := readEnvelope(t, routed, 10*time.Second)
	if hello.Event != "hello" {
		t.Fatalf("expected the end pod hello, got %q", hello.Event)
	}

	// An rpc call on the end pod, through the gateway.
	dir := t.TempDir()
	res := rpcCall(t, routed, 1, "projects.create", map[string]any{"type": "local", "path": dir})
	if res.Error != nil {
		t.Fatalf("projects.create: %s", string(res.Error))
	}
	var proj struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(res.Result, &proj); err != nil || proj.ID == "" {
		t.Fatalf("projects.create result = %s", string(res.Result))
	}
	if r := rpcCall(t, routed, 2, "project.open", map[string]any{"id": proj.ID}); r.Error != nil {
		t.Fatalf("project.open: %s", string(r.Error))
	}

	// A 2 MB transfer on its own channel of the end pod: the framing of the DataChannel and the
	// whole frames of the LAN link must carry it byte for byte.
	xfer := openTestChannel(t, pc, RoutedLabel+g.key+"/xfer:1")
	body := bigBodyPattern()
	req, _ := json.Marshal(map[string]any{"id": 3, "method": "fs.write",
		"params": map[string]any{"path": "big.bin", "content": string(body)}})
	if err := xfer.Write(req); err != nil {
		t.Fatalf("xfer write: %v", err)
	}
	e := readEnvelope(t, xfer, 60*time.Second)
	if e.ID != 3 || len(e.Error) > 0 {
		t.Fatalf("fs.write over xfer: %s", string(e.Error))
	}
	got, err := os.ReadFile(filepath.Join(dir, "big.bin"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(body) {
		t.Fatalf("big.bin: %d bytes, want %d (mismatch)", len(got), len(body))
	}

	// A terminal of the end pod, on its own channel.
	cres := rpcCall(t, routed, 4, "console.create", map[string]any{"kind": "terminal", "command": []string{"cat"}, "cols": 80, "rows": 24})
	var cinfo struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(cres.Result, &cinfo); err != nil || cinfo.ID == "" {
		t.Fatalf("console.create result = %s", string(cres.Result))
	}
	term := openTestChannel(t, pc, RoutedLabel+g.key+"/term:"+cinfo.ID)
	in, _ := json.Marshal(map[string]any{"id": 0, "method": "console.input", "params": map[string]any{"id": cinfo.ID, "data": "through the gateway\r"}})
	if err := term.Write(in); err != nil {
		t.Fatalf("term write: %v", err)
	}
	tm := readEnvelope(t, term, 20*time.Second)
	if tm.Event != "console.output" {
		t.Fatalf("term answer = %s", string(tm.Result))
	}
	var out struct {
		ID   string `json:"id"`
		Data string `json:"data"`
	}
	if err := json.Unmarshal(tm.Data, &out); err != nil || out.ID != cinfo.ID {
		t.Fatalf("terminal output = %s", string(tm.Data))
	}

	// A fetch of the end pod's page, under the path the browser runs it at.
	httpCh := openTestChannel(t, pc, RoutedLabel+g.key+"/http:1")
	q, _ := json.Marshal(map[string]any{"path": "/project/x", "method": "GET", "base": "/pod/" + g.key})
	if err := httpCh.Write(q); err != nil {
		t.Fatalf("http write: %v", err)
	}
	head := readFrame(t, httpCh, 30*time.Second)
	var h struct {
		Status int `json:"status"`
	}
	if err := json.Unmarshal(head, &h); err != nil || h.Status != 200 {
		t.Fatalf("http head = %s", string(head))
	}
	var page []byte
	for {
		b := readFrame(t, httpCh, 30*time.Second)
		if len(b) == 0 {
			break
		}
		page = append(page, b...)
	}
	if !strings.Contains(string(page), `src="/pod/`+g.key+`/assets/app.js"`) {
		t.Fatalf("the page of the end pod does not run under its path: %s", string(page))
	}
}

// A browser revoked on the end pod is refused there, even though the gateway it comes through
// never heard about the revocation: the end pod checks the certificate the gateway vouches for
// against the list it holds itself.
func TestGatewayRevokedBrowser(t *testing.T) {
	g, a := startGateway(t)
	key, cert := browserDevice(t, a)

	pc, rpc, fpB, fpP, err := connectBrowser(t, g.env, "gw-2", sigURL(g.env, "gw-2"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tunnel.RunBrowserCert(rpc, key, cert, a.ID, fpB, fpP, nonceHex()); err != nil {
		t.Fatalf("handshake with the gateway: %v", err)
	}
	// The end pod revoked this browser; the gateway did not.
	list := &identity.Revocations{Seq: 1, Epoch: 1, Revoked: []string{cert.Key}, Created: time.Now().Unix()}
	if err := identity.SignRevocations(a.Priv, list); err != nil {
		t.Fatal(err)
	}
	if err := g.idB.ApplyRevocations(list); err != nil {
		t.Fatal(err)
	}

	dc := openTestChannel(t, pc, RoutedLabel+g.key+"/rpc")
	if _, err := tunnel.RunBrowserCert(dc, key, cert, a.ID, fpB, fpP, nonceHex()); err == nil {
		t.Fatal("the end pod accepted a browser it knows is revoked")
	} else if !strings.Contains(err.Error(), tunnel.CodeRevoked) {
		t.Fatalf("refusal = %v, want revoked", err)
	}
}

// A channel of a pod the gateway does not reach on its LAN is refused, not served locally.
func TestGatewayForeignPod(t *testing.T) {
	g, a := startGateway(t)
	key, cert := browserDevice(t, a)

	pc, rpc, fpB, fpP, err := connectBrowser(t, g.env, "gw-3", sigURL(g.env, "gw-3"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tunnel.RunBrowserCert(rpc, key, cert, a.ID, fpB, fpP, nonceHex()); err != nil {
		t.Fatalf("handshake with the gateway: %v", err)
	}
	// A device key of the account the gateway has no link to.
	_, foreign, _ := ed25519.GenerateKey(nil)
	dc := openTestChannel(t, pc, RoutedLabel+identity.KeyID(foreign)+"/rpc")
	if _, err := tunnel.RunBrowserCert(dc, key, cert, a.ID, fpB, fpP, nonceHex()); err == nil {
		t.Fatal("a channel of a pod the gateway does not reach was accepted")
	}
}
