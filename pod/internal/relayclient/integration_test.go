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
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/coder/websocket"
	"github.com/pion/webrtc/v4"

	"github.com/DrSmithFr/web-ide/pod/internal/config"
	"github.com/DrSmithFr/web-ide/pod/internal/desks"
	"github.com/DrSmithFr/web-ide/pod/internal/hfcache"
	"github.com/DrSmithFr/web-ide/pod/internal/identity"
	"github.com/DrSmithFr/web-ide/pod/internal/llm"
	"github.com/DrSmithFr/web-ide/pod/internal/projects"
	"github.com/DrSmithFr/web-ide/pod/internal/relay"
	"github.com/DrSmithFr/web-ide/pod/internal/server"
	"github.com/DrSmithFr/web-ide/pod/internal/settings"
	"github.com/DrSmithFr/web-ide/pod/internal/sshx"
	"github.com/DrSmithFr/web-ide/pod/internal/store"
	"github.com/DrSmithFr/web-ide/pod/internal/tunnel"
)

const (
	podToken = "secret-token-0123456789abcdef0123"
	bigBody  = 2 * 1024 * 1024 // >1 MB, exercises framing and DataChannel back-pressure
	// The phrase of the shared test vectors (pod/internal/identity/vectors.json), so the
	// account of this test is the one the browser code checks.
	vectorsPhrase = "lecture vapor excuse note pledge rough bundle start regular burden reveal theme banana pencil owner cube social job emotion member joy sting dash whisper"
)

// testEnv is a running relay plus a real pod wired to it.
type testEnv struct {
	httpSrv *httptest.Server
	st      *relay.Store
	cli     *Client
	podID   string
	secret  string
	token   string
	pod     *server.Server
}

// freeTurnPort picks a port free for UDP and TCP both: the control port of the embedded TURN is
// served on each, and an ephemeral port found by opening a UDP socket is often taken by a TCP one
// already — by a test binary of another package running in parallel.
func freeTurnPort(t *testing.T) int {
	t.Helper()
	for i := 0; i < 50; i++ {
		c, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
		if err != nil {
			t.Fatal("no UDP port:", err)
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

// startEnv starts a real relay (signaling + embedded TURN) and a real pod wired to it. The
// identity is the pod's own: nil keeps the pod unpaired (token mode).
func startEnv(t *testing.T, ident *identity.PodIdentity, route PeerRoute) *testEnv {
	t.Helper()
	// A port free for UDP and TCP both: the control port of the embedded TURN is served on each,
	// and a test binary of another package running in parallel takes ephemeral ports too.
	turnPort := freeTurnPort(t)

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

	// The pod. It registers itself on its link, with the secret it was given: a test that
	// registered it in the base beforehand would see it "online" before the link exists, and an
	// offer sent then reaches nothing.
	s, limits := buildPod(t)
	podID, secret := "test-pod", "test-secret"
	cli := New(Config{
		RelayURL:  strings.Replace(httpSrv.URL, "http", "ws", 1),
		PodID:     podID,
		Secret:    secret,
		Server:    s,
		Token:     podToken,
		Identity:  ident,
		Limits:    limits,
		Route:     route,
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
	return &testEnv{httpSrv: httpSrv, st: st, cli: cli, podID: podID, token: podToken, pod: s}
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
		Desks: desks.New(st), LLM: llm.New(st), Models: hfcache.New(st.Path("models", "hf")),
		Pool:    sshx.NewPool(sshx.NewHostKeys(st.Path("known_hosts"))),
		Static:  fstest.MapFS{"index.html": {Data: []byte(`<html><script src="./assets/app.js"></script>app</html>`)}},
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
	// A deadline on the read: a pod that never answers the offer is a failed test, not a test
	// that sits on the link until the runner gives up.
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	_, r, err := b.c.Read(ctx)
	cancel()
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

// nextAnswer waits for the answer of the pod, and turns the refusal the relay reports into an
// error: a pod that will not answer this offer is a refusal, not a message that never comes.
func (b *browserSig) nextAnswer(t *testing.T) (sigMsg, error) {
	for {
		m := b.next(t)
		switch m.Type {
		case "answer":
			return m, nil
		case "error":
			return sigMsg{}, fmt.Errorf("the pod refused the offer: %s", m.Message)
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

// sigURL is the browser's signaling URL of the relay for one session.
func sigURL(env *testEnv, session string) string {
	return strings.Replace(env.httpSrv.URL, "http", "ws", 1) + "/_relay/signal?pod=" + env.podID + "&session=" + session
}

// waitOnline waits for the relay to report a pod link again: a link that dropped is re-dialled
// a moment later, and an offer sent in the gap reached nothing.
func (env *testEnv) waitOnline(t *testing.T, podID string) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if env.st.OnlinePod(podID) {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("the pod %q never came back online", podID)
}

// connectBrowser signals through the relay (through url, so a test can put a rewriting proxy
// in front of it), builds a PeerConnection (TURN forced, like the e2e) and opens the "rpc"
// DataChannel. It returns the live framed channel and the two DTLS fingerprints the handshake
// binds: the browser's own, and the one the relay showed to the pod. A proxy that rewrites the
// offer makes the two disagree. It answers an error rather than failing the test: a peer whose
// fingerprint was rewritten may never reach the channel.
func connectBrowser(t *testing.T, env *testEnv, session, url string) (*webrtc.PeerConnection, *tunnel.Framed, string, string, error) {
	t.Helper()
	c, _, err := websocket.Dial(context.Background(), url, nil)
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
	failed := make(chan string, 1)
	rpcDC, err := pc.CreateDataChannel("rpc", nil)
	if err != nil {
		t.Fatal("browser rpc channel:", err)
	}
	rpcDC.OnOpen(func() { close(opened) })
	pc.OnICEConnectionStateChange(func(s webrtc.ICEConnectionState) {
		if s == webrtc.ICEConnectionStateFailed {
			select {
			case failed <- "the ICE connection failed":
			default:
			}
		}
	})

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
	fpB := tunnel.Fingerprint(pc.LocalDescription().SDP)
	sig.send(t, sigMsg{Type: "offer", Session: session, SDP: pc.LocalDescription().SDP})

	// The pod answers (with its candidates, TURN forced). A pod that refuses the offer says so on
	// the link: that is a refusal the caller can accept, not a message to wait for. A pod whose
	// link was gone a moment ago (it re-dials) is offered again, the way the bootstrap page does.
	var answer sigMsg
	for attempt := 0; ; attempt++ {
		answer, err = sig.nextAnswer(t)
		if err == nil {
			break
		}
		if !strings.Contains(err.Error(), "not reachable") || attempt >= 2 {
			return pc, nil, fpB, "", err
		}
		env.waitOnline(t, env.podID)
		sig.send(t, sigMsg{Type: "offer", Session: session, SDP: pc.LocalDescription().SDP})
	}
	if err := pc.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeAnswer, SDP: answer.SDP}); err != nil {
		t.Fatalf("browser SetRemote(answer): %v", err)
	}
	select {
	case <-opened:
	case why := <-failed:
		return pc, nil, fpB, tunnel.Fingerprint(answer.SDP), errors.New(why)
	case <-time.After(15 * time.Second):
		return pc, nil, fpB, tunnel.Fingerprint(answer.SDP), errors.New("the rpc DataChannel did not open")
	}
	return pc, tunnel.NewFramed(newDataChannelChunk(rpcDC), 8*1024*1024), fpB, tunnel.Fingerprint(answer.SDP), nil
}

// openBrowser plays the browser in the token mode: the token is proved inside the tunnel,
// bound to both DTLS fingerprints, and never reaches the relay.
func openBrowser(t *testing.T, env *testEnv, session, token string) (*webrtc.PeerConnection, *tunnel.Framed) {
	t.Helper()
	pc, rpc, fpB, fpP, err := connectBrowser(t, env, session, sigURL(env, session))
	if err != nil {
		t.Fatal(err)
	}
	if err := tunnel.RunBrowser(rpc, token, fpB, fpP, nonceHex()); err != nil {
		t.Fatalf("handshake over the tunnel: %v", err)
	}
	return pc, rpc
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
	env := startEnv(t, nil, nil)

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
	head := readFrame(t, httpCh, 30*time.Second)
	var h struct {
		Status  int               `json:"status"`
		Headers map[string]string `json:"headers"`
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

// readFrame reads one frame off a channel, with a deadline: a channel that stops answering is a
// failed test, not a test that sits there until the 10 minute timeout of the runner.
func readFrame(t *testing.T, ch tunnel.MsgConn, d time.Duration) []byte {
	t.Helper()
	type r struct {
		b   []byte
		err error
	}
	done := make(chan r, 1)
	go func() {
		b, err := ch.Read()
		done <- r{b, err}
	}()
	select {
	case res := <-done:
		if res.err != nil {
			t.Fatalf("channel read: %v", res.err)
		}
		return res.b
	case <-time.After(d):
		t.Fatalf("no frame on the channel within %s", d)
	}
	return nil
}

// TestHTTPChannelOrder: a static file bigger than one 64 KB chunk must arrive byte for
// byte, in order. The pod writes the body in chunks of 64 KB; each chunk must be copied out
// of the writer's buffer, else the caller (io.Copy, which reuses its buffer) overwrites the
// chunks still queued on the channel.
func TestHTTPChannelOrder(t *testing.T) {
	env := startEnv(t, nil, nil)
	pc, _ := openBrowser(t, env, "http-order", env.token)

	body := make([]byte, 300*1024)
	for i := range body {
		body[i] = byte('a' + (i % 26))
	}
	env.pod.Static = fstest.MapFS{"big.bin": {Data: body}}

	httpCh := openTestChannel(t, pc, "http:1")
	q, _ := json.Marshal(map[string]any{"path": "/big.bin", "method": "GET"})
	if err := httpCh.Write(q); err != nil {
		t.Fatal("http write:", err)
	}
	head := readFrame(t, httpCh, 30*time.Second)
	var h struct {
		Status  int               `json:"status"`
		Headers map[string]string `json:"headers"`
	}
	if err := json.Unmarshal(head, &h); err != nil || h.Status != 200 {
		t.Fatalf("head = %s", string(head))
	}
	var got []byte
	for {
		b := readFrame(t, httpCh, 30*time.Second)
		if len(b) == 0 {
			break
		}
		got = append(got, b...)
	}
	if len(got) != len(body) {
		t.Fatalf("got %d bytes, want %d", len(got), len(body))
	}
	if !bytes.Equal(got, body) {
		for i := range got {
			if got[i] != body[i] {
				t.Fatalf("first difference at %d (mod 16384 = %d)", i, i%16384)
			}
		}
	}
}

// swapFingerprint returns the SDP with another DTLS fingerprint: what a relay that rewrites
// the offer would show to the pod. The value is replaced, the rest of the SDP is untouched.
func swapFingerprint(sdp string) string {
	const p = "a=fingerprint:sha-256 "
	i := strings.Index(sdp, p)
	if i < 0 {
		return sdp
	}
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	hex := strings.ToUpper(fmt.Sprintf("%x", b))
	var parts []string
	for j := 0; j < 64; j += 2 {
		parts = append(parts, hex[j:j+2])
	}
	end := strings.Index(sdp[i:], "\n")
	if end < 0 {
		end = len(sdp) - i
	}
	return sdp[:i] + p + strings.Join(parts, ":") + sdp[i+end:]
}

// proxySig is a relay that rewrites the browser's offer: the browser and the pod do not see
// the same DTLS fingerprint. Everything else is carried as it is.
func proxySig(t *testing.T, env *testEnv, session string) string {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
		if err != nil {
			return
		}
		defer ws.CloseNow()
		up, _, err := websocket.Dial(ctx, strings.Replace(env.httpSrv.URL, "http", "ws", 1)+"/_relay/signal?pod="+env.podID+"&session="+session, nil)
		if err != nil {
			return
		}
		defer up.CloseNow()
		go func() {
			for {
				_, b, err := ws.Read(ctx)
				if err != nil {
					return
				}
				var m sigMsg
				if err := json.Unmarshal(b, &m); err == nil && m.Type == "offer" {
					m.SDP = swapFingerprint(m.SDP)
					b, _ = json.Marshal(m)
				}
				if err := up.Write(ctx, websocket.MessageText, b); err != nil {
					return
				}
			}
		}()
		for {
			_, b, err := up.Read(ctx)
			if err != nil {
				return
			}
			if err := ws.Write(ctx, websocket.MessageText, b); err != nil {
				return
			}
		}
	})
	ts := httptest.NewServer(handler)
	t.Cleanup(ts.Close)
	return strings.Replace(ts.URL, "http", "ws", 1) + "/?session=" + session
}

// TestIntegrationSwappedOffer: a paired pod and a paired browser, through a relay that
// rewrites the browser's offer. The same pair, straight to the relay, completes — so the
// refusal comes from the swapped offer, not from the certificates. Over real WebRTC the
// rewritten fingerprint breaks the DTLS association before the handshake can run; the proof
// itself, bound to the fingerprints, is what refuses when the two sides still agree on the
// bytes (see TestHandshakeCertSwappedFingerprint). Both are refusals.
func TestIntegrationSwappedOffer(t *testing.T) {
	a, err := identity.FromPhrase(vectorsPhrase)
	if err != nil {
		t.Fatal(err)
	}
	podID, err := identity.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := podID.Pin(a.ID); err != nil {
		t.Fatal(err)
	}
	// The certificate must be signed for the key the pod actually uses: its own device.key,
	// created on first run.
	if err := podID.SetCert(identity.NewCert(a, podID.Key(), "pod", identity.KindPod)); err != nil {
		t.Fatal(err)
	}
	env := startEnv(t, podID, nil)

	browserKey := identity.KeyFromSeed(identity.Derive(a.Seed, "browser device"))
	cert := identity.NewCert(a, browserKey, "browser", identity.KindBrowser)

	// The good pair first: the same browser, straight to the relay.
	pc, rpc, fpB, fpP, err := connectBrowser(t, env, "offer-good", sigURL(env, "offer-good"))
	if err != nil {
		t.Fatalf("the good pair must reach the channel: %v", err)
	}
	peer, err := tunnel.RunBrowserCert(rpc, browserKey, cert, a.ID, fpB, fpP, nonceHex())
	if err != nil {
		t.Fatalf("the good pair must complete: %v", err)
	}
	if peer == nil || peer.Key != podID.ID() {
		t.Fatalf("the pod must show its own certificate, got %+v", peer)
	}
	_ = pc.Close()

	// The same browser through a relay that rewrites the offer. The pod signs the binding
	// string over a fingerprint the browser never bound, so the proof does not match; or the
	// DTLS association itself breaks before the channel opens. Both are refusals.
	url := proxySig(t, env, "offer-swapped")
	_, rpc2, fpB2, fpP2, err := connectBrowser(t, env, "offer-swapped", url)
	if fpB2 == fpB {
		t.Fatal("the proxy did not change the fingerprint")
	}
	if err != nil {
		t.Logf("refused before the handshake: %v", err)
		return
	}
	if _, err := tunnel.RunBrowserCert(rpc2, browserKey, cert, a.ID, fpB2, fpP2, nonceHex()); !errors.Is(err, tunnel.ErrProof) {
		t.Fatalf("a relay that swaps the offer must be refused with %v, got %v", tunnel.ErrProof, err)
	}
	t.Log("refused by the proof bound to the fingerprints")
}
