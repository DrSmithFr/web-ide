package lan

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/DrSmithFr/web-ide/pod/internal/identity"
)

// A pod joins with no relay: it waits on its LAN port, alpha finds it at the address typed,
// the Devices panel (the local identity link) lists the request and approves it.
func TestJoinOverTheLAN(t *testing.T) {
	a := account(t, 1)
	al := startPod(t, a, "alpha")
	panel := httptest.NewServer(http.HandlerFunc(al.m.ServeIdentity))
	defer panel.Close()

	joiner, dir := podIdentity(t, nil, "")
	port := freePort(t)
	type result struct {
		cert *identity.Cert
		err  error
	}
	done := make(chan result, 1)
	listening := make(chan string, 1)
	go func() {
		c, err := WaitJoin(context.Background(), JoinConfig{Dir: dir, Addr: "127.0.0.1:" + itoa(port), Identity: joiner, Name: "foxtrot",
			Timeout: 20 * time.Second, OnListen: func(code string, _ int) { listening <- code }})
		done <- result{c, err}
	}()
	code := <-listening
	if err := al.m.SetManual([]string{"127.0.0.1:" + itoa(port)}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for len(al.m.Joins()) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("alpha did not find the joining pod")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if j := al.m.Joins()[0]; j.Code != code || j.Key != joiner.ID() || j.Name != "foxtrot" {
		t.Fatalf("request: %+v", j)
	}
	if st := al.m.Status(); len(st.Failed) != 0 {
		t.Fatalf("a joining pod is not a failed address: %+v", st.Failed)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ws, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(panel.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ws.CloseNow()
	say := func(m idMsg) idMsg {
		b, _ := json.Marshal(m)
		_ = ws.Write(ctx, websocket.MessageText, b)
		_, r, err := ws.Read(ctx)
		if err != nil {
			t.Fatal(err)
		}
		var out idMsg
		_ = json.Unmarshal(r, &out)
		return out
	}
	if m := say(idMsg{Type: "register", Account: a.ID}); m.Type != "ready" {
		t.Fatalf("register: %+v", m)
	}
	if m := say(idMsg{Type: "pairings"}); len(m.Pairings) != 1 || m.Pairings[0].Code != code {
		t.Fatalf("pairings: %+v", m)
	}
	// A certificate for another key is refused before it leaves.
	other := identity.NewCert(a, identity.KeyFromSeed(make([]byte, 32)), "x", identity.KindPod)
	if m := say(idMsg{Type: "pairApprove", Code: code, Cert: other}); m.Type != "error" {
		t.Fatalf("a certificate for another key: %+v", m)
	}
	pub, _ := identity.B64d(joiner.ID())
	cert := identity.NewCertForPub(a, pub, "foxtrot", identity.KindPod)
	if m := say(idMsg{Type: "pairApprove", Code: code, Cert: cert}); m.Type != "pair" || m.Pairing == nil || !m.Pairing.Used {
		t.Fatalf("approve: %+v", m)
	}
	r := <-done
	if r.err != nil || r.cert == nil || r.cert.Key != joiner.ID() || r.cert.Account != a.ID {
		t.Fatalf("the joining pod got %+v %v", r.cert, r.err)
	}
	if m := say(idMsg{Type: "devices"}); len(m.Devices) != 1 {
		t.Fatalf("devices: %+v", m)
	}
	// Inviting a phone needs the relay.
	if m := say(idMsg{Type: "pair"}); m.Type != "error" {
		t.Fatalf("pair: %+v", m)
	}
}

func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}
