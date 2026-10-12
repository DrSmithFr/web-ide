package pair

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/DrSmithFr/web-ide/pod/internal/identity"
	"github.com/DrSmithFr/web-ide/pod/internal/relay"
)

// The phrase of the shared vectors, so the ids checked here are the ones of vectors.json.
const phrase = "lecture vapor excuse note pledge rough bundle start regular burden reveal theme banana pencil owner cube social job emotion member joy sting dash whisper"

// wordsOf reads the words of the numbered rows, without their numbers.
func wordsOf(shown string) []string {
	var w []string
	for _, f := range strings.Fields(shown) {
		if !strings.HasSuffix(f, ")") {
			w = append(w, f)
		}
	}
	return w
}

func env(t *testing.T) (*Env, *bytes.Buffer) {
	t.Helper()
	out := &bytes.Buffer{}
	return &Env{Dir: t.TempDir(), In: strings.NewReader(""), Out: out, Positions: func() []int { return []int{1, 12, 24} }, Timeout: 10 * time.Second}, out
}

// TestCreate shows the phrase once, asks three words back, then stores only the device key,
// the pinned account id and its own certificate — never the phrase or the account key.
func TestCreate(t *testing.T) {
	// The entropy of the phrase of the shared vectors, so the ids checked here are the ones
	// of vectors.json.
	e, out := env(t)
	e.Entropy, _ = hex.DecodeString("7f1e2d3c4b5a69788796a5b4c3d2e1f00123456789abcdef0123456789abcdef")
	e.In = strings.NewReader("lecture\ntheme\nwhisper\n") // the words of positions 1, 12, 24
	if err := Create(e); err != nil {
		t.Fatalf("create: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) < 9 {
		t.Fatalf("unexpected output: %q", out.String())
	}
	// The phrase is shown as eight numbered rows of three words.
	shown := wordsOf(strings.Join(lines[1:9], " "))
	if strings.Join(shown, " ") != phrase {
		t.Fatalf("the phrase shown must be the one of the entropy, got %q", strings.Join(lines[1:9], " | "))
	}
	if err := identity.Validate(strings.Join(shown, " ")); err != nil {
		t.Fatalf("the shown phrase must validate: %v", err)
	}
	if !strings.Contains(out.String(), "frog mass clean vital") {
		t.Fatalf("the account fingerprint must be shown: %q", out.String())
	}

	// What is on disk: the device key (0600) and identity.json, and no phrase anywhere.
	st, err := os.Stat(filepath.Join(e.Dir, "device.key"))
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0o600 {
		t.Fatalf("device.key must be 0600, got %o", st.Mode().Perm())
	}
	entries, _ := os.ReadDir(e.Dir)
	for _, en := range entries {
		b, _ := os.ReadFile(filepath.Join(e.Dir, en.Name()))
		for _, w := range shown {
			if bytes.Contains(b, []byte(w)) {
				t.Fatalf("%s holds a word of the phrase", en.Name())
			}
		}
	}

	// The account derived from the shown phrase must be the one pinned.
	a, err := identity.FromPhrase(strings.Join(shown, " "))
	if err != nil {
		t.Fatal(err)
	}
	p, err := identity.Open(e.Dir)
	if err != nil {
		t.Fatal(err)
	}
	if !p.Paired() || p.Account != a.ID {
		t.Fatalf("the pinned account must be the one of the phrase: %q %q", p.Account, a.ID)
	}
	if p.Cert == nil || p.Cert.Kind != identity.KindPod || identity.VerifyCert(p.Cert, p.Account, nil) != nil {
		t.Fatalf("the certificate of the pod must verify: %+v", p.Cert)
	}
}

// TestCreateNumbered: the phrase is shown with each word numbered, eight rows of three, and the
// check asks its three words back in the order they are numbered (the positions given by the
// test are not sorted).
func TestCreateNumbered(t *testing.T) {
	e, out := env(t)
	e.Entropy, _ = hex.DecodeString("7f1e2d3c4b5a69788796a5b4c3d2e1f00123456789abcdef0123456789abcdef") // the phrase of the vectors
	e.Positions = func() []int { return []int{22, 5, 14} }
	e.In = strings.NewReader("pledge\npencil\nsting\n") // words 5, 14, 22 of the vectors phrase, typed in order
	if err := Create(e); err != nil {
		t.Fatalf("create: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	shown := strings.Join(wordsOf(strings.Join(lines[1:9], " ")), " ")
	if shown != phrase {
		t.Fatalf("the numbered rows must hold the whole phrase, got %q", strings.Join(lines[1:9], " | "))
	}
	for i, l := range lines[1:9] {
		for j := 0; j < 3; j++ {
			want := fmt.Sprintf("%2d) %-9s", i*3+j+1, strings.Fields(phrase)[i*3+j])
			if !strings.Contains(l, want) {
				t.Fatalf("row %d must show word %d numbered (%q in %q)", i+1, i*3+j+1, want, l)
			}
		}
	}
	if !strings.Contains(out.String(), "Confirm — type word 5, 14, 22:") {
		t.Fatalf("the check must list its words in order: %q", out.String())
	}
}

// TestCreateWrongWord: a wrong answer to the check means the phrase was not written down —
// nothing is stored.
func TestCreateWrongWord(t *testing.T) {
	e, _ := env(t)
	// The first word answered is wrong; the check reads from In.
	e.In = strings.NewReader("wrong-word\n")
	err := Create(e)
	if err == nil {
		t.Fatal("a wrong word must be refused")
	}
	if _, err := os.Stat(filepath.Join(e.Dir, "identity.json")); err == nil {
		t.Fatal("nothing must be stored when the check fails")
	}
}

// TestRestore rebuilds the identity from a typed phrase: the same account as the vectors,
// and the device key derived from the phrase (the key of an admin device).
func TestRestore(t *testing.T) {
	e, _ := env(t)
	e.In = strings.NewReader(phrase + "\n")
	if err := Restore(e); err != nil {
		t.Fatalf("restore: %v", err)
	}
	p, err := identity.Open(e.Dir)
	if err != nil {
		t.Fatal(err)
	}
	if p.Account != "zTFqKvpAtSeuHqtYNDGohuqw5giUVa4Zx0uHH0wYrFY" {
		t.Fatalf("account = %q, want the one of the vectors", p.Account)
	}
	if p.ID() != "S6ZXtI0OcDa4t1QXvRf_DWwpA2DJKuRqdJKGe8UveQs" {
		t.Fatalf("device = %q, want the one of the vectors", p.ID())
	}
	if err := identity.VerifyCert(p.Cert, p.Account, nil); err != nil {
		t.Fatalf("the certificate must verify: %v", err)
	}
	// The phrase is not stored.
	b, _ := os.ReadFile(filepath.Join(e.Dir, "identity.json"))
	if bytes.Contains(b, []byte("lecture")) {
		t.Fatal("identity.json must not hold the phrase")
	}
}

// TestRestoreBadPhrase: a phrase whose checksum does not match is refused, nothing stored.
func TestRestoreBadPhrase(t *testing.T) {
	e, _ := env(t)
	e.In = strings.NewReader(strings.Join(strings.Fields(phrase)[:23], " ") + " art\n")
	if err := Restore(e); err == nil {
		t.Fatal("a bad checksum must be refused")
	}
	if _, err := os.Stat(filepath.Join(e.Dir, "identity.json")); err == nil {
		t.Fatal("nothing must be stored for a bad phrase")
	}
}

// TestStatus: an unpaired pod says so; after pairing it shows the account, the certificate
// and the revocation list. It works without a relay.
func TestStatus(t *testing.T) {
	e, out := env(t)
	if err := Status(e); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "not paired") {
		t.Fatalf("an unpaired pod must say so: %q", out.String())
	}

	a, err := identity.FromPhrase(phrase)
	if err != nil {
		t.Fatal(err)
	}
	e.In = strings.NewReader(phrase + "\n")
	if err := Restore(e); err != nil {
		t.Fatal(err)
	}
	p, err := identity.Open(e.Dir)
	if err != nil {
		t.Fatal(err)
	}
	r := &identity.Revocations{Account: a.ID, Seq: 1, Epoch: 1, Revoked: []string{p.ID()}, Created: time.Now().Unix()}
	if err := identity.SignRevocations(a.Priv, r); err != nil {
		t.Fatal(err)
	}
	if err := p.ApplyRevocations(r); err != nil {
		t.Fatal(err)
	}
	if err := Status(e); err != nil {
		t.Fatal(err)
	}
	s := out.String()
	if !strings.Contains(s, a.ID) || !strings.Contains(s, "frog mass clean vital") {
		t.Fatalf("status must show the account and its fingerprint: %q", s)
	}
	if !strings.Contains(s, "seq 1, epoch 1, 1 device") {
		t.Fatalf("status must show the revocation list: %q", s)
	}
}

// TestJoin: the pod posts its key, gets a code, prints it with the QR and the fingerprint,
// and waits for an admin to approve. The certificate it gets back must verify against the
// account it names — the relay cannot sign one.
func TestJoin(t *testing.T) {
	st, err := relay.OpenStore(filepath.Join(t.TempDir(), "relay.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	// A free port: Port 0 would mean 3478, which the relay package tests bind too (packages
	// run in parallel).
	turn, err := relay.NewTurn(st, relay.TurnConfig{AdvertiseIP: "127.0.0.1", Port: freeTurnPort(t)})
	if err != nil {
		t.Fatal("starting the embedded TURN:", err)
	}
	defer turn.Close()
	ts := httptest.NewServer(relay.New(st, turn).Handler())
	defer ts.Close()

	a, err := identity.FromPhrase(phrase)
	if err != nil {
		t.Fatal(err)
	}

	e, out := env(t)
	e.RelayURL = "ws" + ts.URL[4:]
	e.Name = "laptop"

	done := make(chan error, 1)
	go func() { done <- Join(e) }()

	code := waitCode(t, out)
	if len(code) != 8 {
		t.Fatalf("the code must be 8 characters, got %q", code)
	}
	if !strings.Contains(out.String(), "#join="+code) {
		t.Fatalf("the invite link must be printed: %q", out.String())
	}
	// The device key of a pod is its own (random), so the fingerprint comes from the output.
	fp := strings.TrimSpace(strings.Split(strings.Split(out.String(), "Fingerprint of this device: ")[1], "\n")[0])
	if len(strings.Fields(fp)) != 4 {
		t.Fatalf("the 4-word fingerprint must be printed: %q", out.String())
	}

	// An admin reads the code and signs the certificate for the key it read.
	ws, _, err := websocket.Dial(context.Background(), wsURL(ts, "/_relay/identity"), nil)
	if err != nil {
		t.Fatal("dialing the admin link:", err)
	}
	defer ws.Close(websocket.StatusNormalClosure, "")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	sendMsg(t, ctx, ws, map[string]any{"type": "register", "account": a.ID})
	if m := recvMsg(t, ctx, ws); m["type"] != "ready" {
		t.Fatalf("the admin must be ready, got %q", m["type"])
	}
	sendMsg(t, ctx, ws, map[string]any{"type": "pairGet", "code": code, "account": a.ID})
	m := recvMsg(t, ctx, ws)
	if m["type"] != "pair" || m["pairing"] == nil {
		t.Fatalf("the admin must read the posted key, got %q", m["type"])
	}
	posted := m["pairing"].(map[string]any)["key"].(string)
	// The admin signs for the key it read. The fingerprint the device showed must match:
	// that is the guard against a relay that swapped the key.
	if identity.Fingerprint(posted) != fp {
		t.Fatalf("the relay swapped the key: %q vs %q", posted, fp)
	}
	pub, err := identity.B64d(posted)
	if err != nil {
		t.Fatal(err)
	}
	cert := identity.NewCertForPub(a, pub, "laptop", identity.KindPod)
	sendMsg(t, ctx, ws, map[string]any{"type": "pairApprove", "code": code, "cert": cert})
	if m := recvMsg(t, ctx, ws); m["type"] != "pair" {
		t.Fatalf("the approval must come back, got %q %q", m["type"], m["message"])
	}

	if err := <-done; err != nil {
		t.Fatalf("join: %v", err)
	}
	p, err := identity.Open(e.Dir)
	if err != nil {
		t.Fatal(err)
	}
	if !p.Paired() || p.Account != a.ID {
		t.Fatalf("the pod must be paired with the account: %q", p.Account)
	}
	if err := identity.VerifyCert(p.Cert, a.ID, nil); err != nil {
		t.Fatalf("the certificate the pod got must verify: %v", err)
	}
	if !strings.Contains(out.String(), a.ID) {
		t.Fatalf("join must print the account it is paired with: %q", out.String())
	}
}

// TestJoinOffline: without a relay, join says it needs one; create, restore and status work
// offline (LAN discovery, ticket #50, builds on that).
func TestJoinOffline(t *testing.T) {
	e, _ := env(t)
	if err := Join(e); err == nil {
		t.Fatal("join without a relay must be refused")
	}
	if err := Restore(e); err == nil {
		t.Fatal("restore needs a phrase")
	}
}

func waitCode(t *testing.T, out *bytes.Buffer) string {
	t.Helper()
	re := regexp.MustCompile(`Pairing code: ([A-Z2-7]+)`)
	for i := 0; i < 100; i++ {
		if m := re.FindStringSubmatch(out.String()); m != nil {
			return m[1]
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("no pairing code in %q", out.String())
	return ""
}

// freeTurnPort asks the kernel for a free UDP port, so the embedded TURN of this test does
// not collide with the one the relay package tests bind on 3478.
func freeTurnPort(t *testing.T) int {
	t.Helper()
	c, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	port := c.LocalAddr().(*net.UDPAddr).Port
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	return port
}

func wsURL(ts *httptest.Server, path string) string {
	return "ws" + ts.URL[4:] + path
}

func sendMsg(t *testing.T, ctx context.Context, ws *websocket.Conn, m map[string]any) {
	b, _ := json.Marshal(m)
	if err := ws.Write(ctx, websocket.MessageText, b); err != nil {
		t.Fatalf("writing the identity link: %v", err)
	}
}

func recvMsg(t *testing.T, ctx context.Context, ws *websocket.Conn) map[string]any {
	_, b, err := ws.Read(ctx)
	if err != nil {
		t.Fatalf("reading the identity link: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("unmarshal the identity link: %v", err)
	}
	return m
}
