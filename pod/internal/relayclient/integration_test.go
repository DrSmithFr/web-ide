package relayclient

// Integration test: a real relay (signaling + embedded TURN) and a real pod
// (server.Server + this relayclient) are driven by a Pion PeerConnection that plays the
// browser. The whole path is exercised over WebRTC DataChannels through the embedded
// TURN: the channel-binding handshake (the token proved in the tunnel, bound to both
// DTLS fingerprints), the rpc protocol, and a >1 MB transfer on the xfer channel that
// must traverse the 16 KB framing intact.

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"net"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/coder/websocket"
	"github.com/pion/webrtc/v4"

	"github.com/DrSmithFr/web-ide/pod/internal/config"
	"github.com/DrSmithFr/web-ide/pod/internal/hfcache"
	"github.com/DrSmithFr/web-ide/pod/internal/llm"
	"github.com/DrSmithFr/web-ide/pod/internal/projects"
	"github.com/DrSmithFr/web-ide/pod/internal/relay"
	"github.com/DrSmithFr/web-ide/pod/internal/server"
	"github.com/DrSmithFr/web-ide/pod/internal/sessions"
	"github.com/DrSmithFr/web-ide/pod/internal/settings"
	"github.com/DrSmithFr/web-ide/pod/internal/sshx"
	"github.com/DrSmithFr/web-ide/pod/internal/store"
	"github.com/DrSmithFr/web-ide/pod/internal/tunnel"
)

const (
	podToken = "secret-token-0123456789abcdef0123"
	bigBody  = 2 * 1024 * 1024 // >1 MB, exercises framing and DataChannel back-pressure
)

// testEnv is a running relay plus a real pod wired to it.
type testEnv struct {
	httpSrv *httptest.Server
	podID   string
	secret  string
	token   string
}

// startEnv starts a real relay (signaling + embedded TURN) and a real pod wired to it.
func startEnv(t *testing.T) *testEnv {
	t.Helper()
	// A free UDP port for STUN/TURN.
	u, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1")})
	if err != nil {
		t.Fatal("no UDP port:", err)
	}
	turnPort := u.LocalAddr().(*net.UDPAddr).Port
	u.Close()

	st, err := relay.OpenStore(filepath.Join(t.TempDir(), "relay.db"))
	if err != nil {
		t.Fatal("relay store:", err)
	}
	turn, err := relay.NewTurn(st, relay.TurnConfig{AdvertiseIP: "127.0.0.1", Port: turnPort})
	if err != nil {
		t.Fatal("turn:", err)
	}
	rsrv := relay.New(st, turn)
	httpSrv := httptest.NewServer(rsrv.Handler())
	runCtx, cancel := context.WithCancel(context.Background())
	t.Cleanup(func() {
		cancel() // the pod link drops; the relay's servePod handler marks the pod offline
		// (a SQLite commit on its own goroutine, not waited by httpSrv.Close).
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) && st.OnlinePod("test-pod") {
			time.Sleep(20 * time.Millisecond)
		}
		httpSrv.Close() // after the offline commit, so no journal file races the RemoveAll
		turn.Close()
		st.Close()
	})

	// The pod.
	s, limits := buildPod(t)
	podID, secret := "test-pod", "test-secret"
	if err := st.Register(podID, secret, "test"); err != nil {
		t.Fatal("register pod:", err)
	}
	cli := New(Config{
		RelayURL:  strings.Replace(httpSrv.URL, "http", "ws", 1),
		PodID:     podID,
		Secret:    secret,
		Server:    s,
		Token:     podToken,
		Limits:    limits,
		ForceTurn: true,
		OnChannel: func(label string, conn tunnel.MsgConn) { onTestChannel(t, label, conn) },
	})
	go cli.Run(runCtx)

	// The pod must be registered and online before a browser can signal to it.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if st.OnlinePod(podID) {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !st.OnlinePod(podID) {
		t.Fatal("the pod did not register with the relay")
	}
	return &testEnv{httpSrv: httpSrv, podID: podID, token: podToken}
}

// buildPod builds a real server.Server in a temp dir, the way main.go does.
func buildPod(t *testing.T) (*server.Server, *tunnel.RateLimiter) {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal("store:", err)
	}
	cfg, _ := config.Load(st)
	reg, _ := projects.Load(st)
	sets, _ := settings.Load(st)
	s := &server.Server{
		Cfg: cfg, Store: st, Token: podToken, Projects: reg, Settings: sets,
		Sessions: sessions.New(st), LLM: llm.New(st), Models: hfcache.New(st.Path("models", "hf")),
		Pool:    sshx.NewPool(sshx.NewHostKeys(st.Path("known_hosts"))),
		Static:  fstest.MapFS{"index.html": {Data: []byte("<html>app</html>")}},
		Version: "1",
	}
	s.Init()
	t.Cleanup(func() { s.Shutdown() })
	return s, tunnel.NewRateLimiter(5, 5*time.Minute)
}

// xferDone signals when the pod has read back the big xfer transfer.
var xferDone = make(chan []byte, 1)

// onTestChannel is the pod's handler for the non-"rpc" channels. For "xfer:1" it reads
// the whole >1 MB message back and hands it to the test.
func onTestChannel(t *testing.T, label string, conn tunnel.MsgConn) {
	if label != "xfer:1" {
		return
	}
	b, err := conn.Read()
	if err != nil {
		t.Errorf("xfer:1 read: %v", err)
		return
	}
	xferDone <- b
}

// browserSig is the browser's signaling link to the relay, in one session of a pod.
type browserSig struct {
	t *testing.T
	c *websocket.Conn
}

func (b *browserSig) next(t *testing.T) sigMsg {
	t.Helper()
	_, r, err := b.c.Read(context.Background())
	if err != nil {
		t.Fatalf("reading signaling: %v", err)
	}
	var m sigMsg
	if err := json.Unmarshal(r, &m); err != nil {
		t.Fatalf("unmarshal signaling: %v", err)
	}
	return m
}

// nextType reads signaling messages until it finds the wanted type.
func (b *browserSig) nextType(t *testing.T, want string) sigMsg {
	for {
		m := b.next(t)
		if m.Type == want {
			return m
		}
	}
}

func (b *browserSig) send(t *testing.T, m sigMsg) {
	t.Helper()
	r, _ := json.Marshal(m)
	if err := b.c.Write(context.Background(), websocket.MessageText, r); err != nil {
		t.Fatalf("writing signaling: %v", err)
	}
}

// openBrowser plays the browser: it signals through the relay, builds a PeerConnection
// (TURN forced, like the e2e), creates the "rpc" DataChannel, completes the handshake,
// and returns the live rpc channel plus the PeerConnection.
func openBrowser(t *testing.T, env *testEnv, session, token string) (*webrtc.PeerConnection, *tunnel.Framed) {
	t.Helper()
	u := strings.Replace(env.httpSrv.URL, "http", "ws", 1)
	u += "/_relay/signal?pod=" + env.podID + "&session=" + session
	c, _, err := websocket.Dial(context.Background(), u, nil)
	if err != nil {
		t.Fatal("dialing the browser link:", err)
	}
	t.Cleanup(func() { _ = c.CloseNow() })
	sig := &browserSig{t: t, c: c}

	// The relay sends fresh TURN credentials for this session.
	ice := sig.nextType(t, "ice")

	cfg := webrtc.Configuration{
		ICEServers:         []webrtc.ICEServer{{URLs: ice.URLs, Username: ice.Username, Credential: ice.Credential}},
		ICETransportPolicy: webrtc.ICETransportPolicyRelay, // force TURN, like the e2e
	}
	pc, err := webrtc.NewPeerConnection(cfg)
	if err != nil {
		t.Fatal("browser PeerConnection:", err)
	}
	t.Cleanup(func() { _ = pc.Close() })
	opened := make(chan struct{})
	rpcDC, err := pc.CreateDataChannel("rpc", nil)
	if err != nil {
		t.Fatal("browser rpc channel:", err)
	}
	rpcDC.OnOpen(func() { close(opened) })

	// Non-trickle offer: wait for the browser's candidates, then send the full SDP.
	offer, err := pc.CreateOffer(nil)
	if err != nil {
		t.Fatal("browser offer:", err)
	}
	gather := webrtc.GatheringCompletePromise(pc)
	if err := pc.SetLocalDescription(offer); err != nil {
		t.Fatal("browser SetLocal(offer):", err)
	}
	<-gather
	sig.send(t, sigMsg{Type: "offer", Session: session, SDP: pc.LocalDescription().SDP})

	// The pod answers (with its candidates, TURN forced).
	answer := sig.nextType(t, "answer")
	if err := pc.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeAnswer, SDP: answer.SDP}); err != nil {
		t.Fatalf("browser SetRemote(answer): %v", err)
	}
	select {
	case <-opened:
	case <-time.After(15 * time.Second):
		t.Fatal("the rpc DataChannel did not open")
	}

	// The channel-binding handshake: the token is proved here, bound to both DTLS
	// fingerprints, and never reaches the relay.
	rpcConn := tunnel.NewFramed(newDataChannelChunk(rpcDC), 8*1024*1024)
	nonceB := nonceHex()
	if err := tunnel.RunBrowser(rpcConn, token,
		tunnel.Fingerprint(pc.LocalDescription().SDP), tunnel.Fingerprint(answer.SDP), nonceB); err != nil {
		t.Fatalf("handshake over the tunnel: %v", err)
	}
	return pc, rpcConn
}

// nonceHex returns a random 32-hex-char nonce (the browser nonce of the handshake).
func nonceHex() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return fmt.Sprintf("%x", b)
}

// TestIntegrationRelay: a Pion peer plays the browser and opens a real tunnel to a real
// pod through a real relay (TURN forced). It checks the handshake (the right token), the
// rpc protocol (workspace.get), and a >1 MB transfer on the xfer channel that must
// traverse the 16 KB framing intact.
func TestIntegrationRelay(t *testing.T) {
	env := startEnv(t)

	pc, rpc := openBrowser(t, env, "int-1", env.token)

	// The pod pushes a "hello" event (the client id) as soon as the tunnel is up.
	hello := readEnvelope(t, rpc, 10*time.Second)
	if hello.Event != "hello" {
		t.Fatalf("expected the pod hello event, got %q", hello.Event)
	}

	// rpc: the JSON protocol, unchanged, over the DataChannel.
	res := rpcCall(t, rpc, 1, "workspace.get", map[string]any{})
	if res.Error != nil {
		t.Fatalf("workspace.get over the tunnel: %v", res.Error)
	}
	var ws map[string]string
	if err := json.Unmarshal(res.Result, &ws); err != nil || ws["version"] == "" {
		t.Fatalf("workspace.get result = %s", string(res.Result))
	}

	// xfer: a >1 MB message on its own channel, through the 16 KB framing, must arrive
	// byte-for-byte. The browser writes it; the pod's OnChannel reads it back.
	body := bigBodyPattern()
	xfer := tunnel.NewFramed(newDataChannelChunk(createXferChannel(t, pc)), 8*1024*1024)
	if err := xfer.Write(body); err != nil {
		t.Fatalf("xfer write: %v", err)
	}
	select {
	case got := <-xferDone:
		if !bytes.Equal(got, body) {
			t.Fatalf("xfer: %d bytes, want %d (mismatch)", len(got), len(body))
		}
	case <-time.After(30 * time.Second):
		t.Fatal("the xfer transfer did not arrive")
	}
}

// bigBodyPattern builds a 2 MB body whose bytes depend on the offset, so a corrupt or
// truncated reassembly is detected.
func bigBodyPattern() []byte {
	b := make([]byte, bigBody)
	for i := range b {
		b[i] = byte(i%251) + 1
	}
	return b
}

func createXferChannel(t *testing.T, pc *webrtc.PeerConnection) *webrtc.DataChannel {
	t.Helper()
	dc, err := pc.CreateDataChannel("xfer:1", nil)
	if err != nil {
		t.Fatalf("browser xfer channel: %v", err)
	}
	opened := make(chan struct{})
	dc.OnOpen(func() { close(opened) })
	select {
	case <-opened:
	case <-time.After(10 * time.Second):
		t.Fatal("the xfer DataChannel did not open")
	}
	return dc
}

// envelope is one message of the tunnel JSON protocol: a response (id, result, error) or
// an event (event, data). The server pushes both over the same channel.
type envelope struct {
	ID     int64           `json:"id"`
	Event  string          `json:"event,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  json.RawMessage `json:"error,omitempty"`
}

// rpcCall sends one request over the rpc channel and returns the matching response,
// skipping events and other responses.
func rpcCall(t *testing.T, rpc *tunnel.Framed, id int64, method string, params any) envelope {
	t.Helper()
	data, _ := json.Marshal(map[string]any{"id": id, "method": method, "params": params})
	if err := rpc.Write(data); err != nil {
		t.Fatalf("rpc write %s: %v", method, err)
	}
	deadline := time.After(20 * time.Second)
	for {
		select {
		case <-deadline:
			t.Fatalf("%s: timeout", method)
		default:
		}
		e := readEnvelope(t, rpc, 20*time.Second)
		if e.ID == id {
			if len(e.Error) > 0 {
				t.Fatalf("%s: %s", method, string(e.Error))
			}
			return e
		}
		// An event (the hello, pushes) or another response: not ours, keep reading.
	}
}

// readEnvelope reads one protocol message off the rpc channel (with a deadline).
func readEnvelope(t *testing.T, rpc *tunnel.Framed, d time.Duration) envelope {
	t.Helper()
	type r struct {
		b   []byte
		err error
	}
	ch := make(chan r, 1)
	go func() {
		b, err := rpc.Read()
		ch <- r{b, err}
	}()
	select {
	case res := <-ch:
		if res.err != nil {
			t.Fatalf("rpc read: %v", res.err)
		}
		var e envelope
		if err := json.Unmarshal(res.b, &e); err != nil {
			t.Fatalf("rpc unmarshal: %v", err)
		}
		return e
	case <-time.After(d):
		t.Fatalf("no rpc message within %s", d)
	}
	return envelope{}
}
