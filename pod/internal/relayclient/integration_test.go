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
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/http/httptest"
	"os"
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

// openTestChannel opens one channel from the browser side and waits for it.
func openTestChannel(t *testing.T, pc *webrtc.PeerConnection, label string) *tunnel.Framed {
	t.Helper()
	dc, err := pc.CreateDataChannel(label, nil)
	if err != nil {
		t.Fatalf("browser %s channel: %v", label, err)
	}
	opened := make(chan struct{})
	dc.OnOpen(func() { close(opened) })
	select {
	case <-opened:
	case <-time.After(10 * time.Second):
		t.Fatalf("the %s DataChannel did not open", label)
	}
	return tunnel.NewFramed(newDataChannelChunk(dc), 8*1024*1024)
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
// rpc protocol (workspace.get), a >1 MB request on its own xfer channel, the terminal
// input and output on its term channel, and an HTTP fetch on an http channel.
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

	// xfer: a >1 MB request on its own channel, through the 16 KB framing, must reach the
	// pod and its answer come back on the same channel.
	dir := t.TempDir()
	open := rpcCall(t, rpc, 2, "projects.create", map[string]any{"type": "local", "path": dir})
	var proj struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(open.Result, &proj); err != nil || proj.ID == "" {
		t.Fatalf("projects.create result = %s", string(open.Result))
	}
	rpcCall(t, rpc, 3, "project.open", map[string]any{"id": proj.ID})
	xfer := openTestChannel(t, pc, "xfer:1")
	body := bigBodyPattern()
	r, err := json.Marshal(map[string]any{"id": 4, "method": "fs.write",
		"params": map[string]any{"path": "big.bin", "content": string(body)}})
	if err != nil {
		t.Fatal(err)
	}
	if err := xfer.Write(r); err != nil {
		t.Fatalf("xfer write: %v", err)
	}
	e := readEnvelope(t, xfer, 30*time.Second)
	if e.ID != 4 {
		t.Fatalf("xfer answer = %s", string(e.Result))
	}
	if len(e.Error) > 0 {
		t.Fatalf("fs.write over xfer: %s", string(e.Error))
	}
	// The file on disk is the whole body, byte for byte.
	got, err := os.ReadFile(filepath.Join(dir, "big.bin"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, body) {
		t.Fatalf("big.bin: %d bytes, want %d (mismatch)", len(got), len(body))
	}

	// term:<id>: the input goes on the channel of the terminal, its output comes back on
	// it, so a long scrollback never delays the rpc channel.
	cres := rpcCall(t, rpc, 5, "console.create", map[string]any{"kind": "terminal", "command": []string{"cat"}, "cols": 80, "rows": 24})
	var cinfo struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(cres.Result, &cinfo); err != nil || cinfo.ID == "" {
		t.Fatalf("console.create result = %s", string(cres.Result))
	}
	term := openTestChannel(t, pc, "term:"+cinfo.ID)
	n, _ := json.Marshal(map[string]any{"id": 0, "method": "console.input", "params": map[string]any{"id": cinfo.ID, "data": "hello\r"}})
	if err := term.Write(n); err != nil {
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
	b, err := base64.StdEncoding.DecodeString(out.Data)
	if err != nil || !strings.Contains(string(b), "hello") {
		t.Fatalf("terminal echo = %q", out.Data)
	}

	// http:<n>: one fetch of the app, answered by the pod through the channel: the head as
	// JSON, then the body as raw chunks.
	httpCh := openTestChannel(t, pc, "http:1")
	q, _ := json.Marshal(map[string]any{"path": "/", "method": "GET"})
	if err := httpCh.Write(q); err != nil {
		t.Fatalf("http write: %v", err)
	}
	head, err := httpCh.Read()
	if err != nil {
		t.Fatalf("http head read: %v", err)
	}
	var h struct {
		Status  int               `json:"status"`
		Headers map[string]string `json:"headers"`
	}
	if err := json.Unmarshal(head, &h); err != nil || h.Status != 200 {
		t.Fatalf("http head = %s", string(head))
	}
	var page []byte
	for {
		b, err := httpCh.Read()
		if err != nil {
			t.Fatalf("http body read: %v", err)
		}
		if len(b) == 0 {
			break
		}
		page = append(page, b...)
	}
	if !strings.Contains(string(page), "app") {
		t.Fatalf("http body = %q", string(page))
	}
}



// bigBodyPattern builds a 2 MB body whose bytes depend on the offset, so a corrupt or
// truncated reassembly is detected. It stays printable ASCII so it survives JSON.
func bigBodyPattern() []byte {
	b := make([]byte, bigBody)
	for i := range b {
		b[i] = byte('a' + i%26)
	}
	return b
}



// envelope is one message of the tunnel JSON protocol: a response (id, result, error) or
// an event (event, data). The server pushes both over the same channel.
type envelope struct {
	ID     int64           `json:"id"`
	Event  string          `json:"event,omitempty"`
	Data   json.RawMessage `json:"data,omitempty"`
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
