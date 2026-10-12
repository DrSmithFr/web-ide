package tunnel

import (
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/DrSmithFr/web-ide/pod/internal/identity"
)

// pipe is an in-memory whole-message MsgConn for both ends of a tunnel.
type pipe struct{ a, b *half }

type half struct {
	in  chan []byte
	out *half
	c   chan struct{}
	oc  sync.Once
}

func (h *half) Read() ([]byte, error) {
	select {
	case b, ok := <-h.in:
		if !ok {
			return nil, io.EOF
		}
		return b, nil
	case <-h.c:
		return nil, io.EOF
	}
}

func (h *half) read(d time.Duration) []byte {
	select {
	case b, ok := <-h.in:
		if !ok {
			return nil
		}
		return b
	case <-h.c:
		return nil
	case <-time.After(d):
		return nil
	}
}

func (h *half) Write(b []byte) error {
	select {
	case h.out.in <- b:
		return nil
	case <-h.c:
		return io.EOF
	}
}

func (h *half) Close() error { h.oc.Do(func() { close(h.c) }); return nil }

func newPipe() *pipe {
	h1 := &half{in: make(chan []byte, 64), c: make(chan struct{})}
	h2 := &half{in: make(chan []byte, 64), c: make(chan struct{})}
	h1.out, h2.out = h2, h1
	return &pipe{a: h1, b: h2}
}

type testEnv struct {
	ID     int64           `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
}

func (p *pipe) send(m string, params any) {
	raw, _ := json.Marshal(params)
	p.b.out.in <- mustEnveloped(m, raw)
}

func mustEnveloped(m string, raw json.RawMessage) []byte {
	b, _ := json.Marshal(testEnv{Method: m, Params: raw})
	return b
}

// browser performs the browser side of the token mode and reports the outcome.
func (p *pipe) browser(t *testing.T, token, version, fpB, fpP string) (nonceB, nonceP, podProof string, ready, versionErr bool) {
	t.Helper()
	nonceB = nonce()
	p.send("$hello", map[string]string{"protocol": Protocol, "version": version, "nonce": nonceB})

	b := p.b.read(2 * time.Second)
	var env testEnv
	if b != nil {
		_ = json.Unmarshal(b, &env)
		var pod struct {
			Nonce string `json:"nonce"`
			Proof string `json:"proof"`
			Error string `json:"error"`
		}
		_ = json.Unmarshal(env.Params, &pod)
		nonceP, podProof, versionErr = pod.Nonce, pod.Proof, pod.Error != ""
	}
	if versionErr {
		return
	}
	p.send("$auth", map[string]string{"proof": MAC(RoleBrowser, token, fpB, fpP, nonceB, nonceP)})
	if b = p.b.read(2 * time.Second); b != nil {
		var readyEnv testEnv
		_ = json.Unmarshal(b, &readyEnv)
		ready = readyEnv.Method == "$ready"
	}
	return
}

// certBrowser performs the browser side of the cert mode: it shows its certificate, reads
// the pod's hello (its nonce, its certificate and its signature), checks that signature
// against the pinned account, then signs the binding string with its device key. It reports
// the error code the pod answered, the pod certificate and whether $ready came.
func (p *pipe) certBrowser(t *testing.T, key ed25519.PrivateKey, cert *identity.Cert, account, fpB, fpP string) (code string, podCert *identity.Cert, ready bool) {
	t.Helper()
	nonceB := nonce()
	p.send("$hello", helloParams{Protocol: Protocol, Version: Version, Nonce: nonceB, Cert: cert})

	b := p.b.read(2 * time.Second)
	var env testEnv
	if b == nil {
		return
	}
	_ = json.Unmarshal(b, &env)
	var pod helloParams
	_ = json.Unmarshal(env.Params, &pod)
	if pod.Error != "" {
		return pod.Error, nil, false
	}
	podCert = pod.Cert
	// The browser checks the pod certificate against the pinned account, then its
	// signature against the key that certificate names.
	if err := identity.VerifyCert(podCert, account, nil); err != nil {
		return "pod certificate: " + err.Error(), nil, false
	}
	if !identity.Verify(podCert.Key, pod.Sig, []byte(Binding(RolePod, fpB, fpP, nonceB, pod.Nonce))) {
		return "pod signature does not match", nil, false
	}
	p.send("$auth", authParams{Sig: identity.Sign(key, []byte(Binding(RoleBrowser, fpB, fpP, nonceB, pod.Nonce)))})
	if b = p.b.read(2 * time.Second); b != nil {
		var readyEnv testEnv
		_ = json.Unmarshal(b, &readyEnv)
		ready = readyEnv.Method == "$ready"
	}
	return "", podCert, ready
}

func TestHandshakeGood(t *testing.T) {
	token, fpB, fpP := "tok-0123456789abcdef", "AABBCCDD", "11223344"
	p := newPipe()
	h := &Handshake{Token: token, FPBrowser: fpB, FPPod: fpP}
	errCh := make(chan error, 1)
	go func() { errCh <- h.Run(p.a) }()

	nonceB, nonceP, proof, ready, versionErr := p.browser(t, token, VersionToken, fpB, fpP)
	if versionErr || !ready {
		t.Fatalf("handshake not ready (versionErr=%v ready=%v)", versionErr, ready)
	}
	if want := MAC(RolePod, token, fpB, fpP, nonceB, nonceP); proof != want {
		t.Fatalf("pod proof = %q, want %q", proof, want)
	}
	if err := <-errCh; err != nil {
		t.Fatalf("handshake returned %v", err)
	}
}

func TestHandshakeWrongToken(t *testing.T) {
	token, fpB, fpP := "tok-0123456789abcdef", "AABBCCDD", "11223344"
	p := newPipe()
	errCh := make(chan error, 1)
	go func() { errCh <- (&Handshake{Token: token, FPBrowser: fpB, FPPod: fpP}).Run(p.a) }()
	p.browser(t, "WRONG-TOKEN", VersionToken, fpB, fpP)
	if err := <-errCh; !errors.Is(err, ErrProof) {
		t.Fatalf("wrong token must give ErrProof, got %v", err)
	}
}

func TestHandshakeSwappedFingerprint(t *testing.T) {
	token, fpB, fpP := "tok-0123456789abcdef", "AABBCCDD", "11223344"
	p := newPipe()
	errCh := make(chan error, 1)
	go func() { errCh <- (&Handshake{Token: token, FPBrowser: fpB, FPPod: fpP}).Run(p.a) }()
	// The relay swapped the SDP: the browser binds a fingerprint the pod does not see.
	p.browser(t, token, VersionToken, "DEADBEEF", fpP)
	if err := <-errCh; !errors.Is(err, ErrProof) {
		t.Fatalf("a swapped fingerprint must give ErrProof, got %v", err)
	}
}

func TestHandshakeVersionMismatch(t *testing.T) {
	p := newPipe()
	errCh := make(chan error, 1)
	go func() { errCh <- (&Handshake{Token: "tok", FPBrowser: "AABB", FPPod: "CCDD"}).Run(p.a) }()
	_, _, _, _, versionErr := p.browser(t, "tok", "0", "AABB", "CCDD")
	if !versionErr {
		t.Fatal("expected the version hello")
	}
	if err := <-errCh; !errors.Is(err, ErrVersion) {
		t.Fatalf("version mismatch must give ErrVersion, got %v", err)
	}
}

func TestRateLimiter(t *testing.T) {
	r := NewRateLimiter(2, time.Hour)
	if !r.Allow() || !r.Allow() {
		t.Fatal("the first two hits must be allowed")
	}
	if r.Allow() {
		t.Fatal("the third hit within the limit must be refused")
	}
	if !r.Locked() {
		t.Fatal("after the limit the rate limiter must report locked")
	}
	r.now = func() time.Time { return time.Now().Add(time.Hour + time.Second) }
	if !r.Allow() {
		t.Fatal("after the window the limit must reset")
	}
}

// TestHandshakeBothSides: the pod (Run) and the browser (RunBrowser) complete the
// exchange together, bound to both fingerprints and the token.
func TestHandshakeBothSides(t *testing.T) {
	p := newPipe()
	go func() {
		_ = RunBrowser(p.b, "tok", "BROWSER-FP", "POD-FP", "NONCE-B")
	}()
	h := &Handshake{Token: "tok", FPBrowser: "BROWSER-FP", FPPod: "POD-FP"}
	if err := h.Run(p.a); err != nil {
		t.Fatal("the handshake must complete with the right token and fingerprints:", err)
	}
}

// TestHandshakeDeadline: a browser that never answers $hello is dropped with ErrDeadline.
func TestHandshakeDeadline(t *testing.T) {
	p := newPipe()
	h := &Handshake{Token: "tok", FPBrowser: "AABB", FPPod: "CCDD", Deadline: 100 * time.Millisecond}
	errCh := make(chan error, 1)
	go func() { errCh <- h.Run(p.a) }()
	// The browser sends nothing: the deadline must fire.
	select {
	case err := <-errCh:
		if !errors.Is(err, ErrDeadline) {
			t.Fatalf("a stalled handshake must give ErrDeadline, got %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the deadline did not fire")
	}
}

// TestHandshakeLockedRefusesBefore: once the limit is reached, Run refuses before any
// proof is verified (ErrLimit, even though no $hello/$auth is exchanged).
func TestHandshakeLockedRefusesBefore(t *testing.T) {
	r := NewRateLimiter(1, time.Hour)
	_ = r.Allow() // exhaust the limit
	p := newPipe()
	h := &Handshake{Token: "tok", FPBrowser: "A", FPPod: "B", Limits: r, Deadline: 2 * time.Second}
	errCh := make(chan error, 1)
	go func() { errCh <- h.Run(p.a) }()
	select {
	case err := <-errCh:
		if !errors.Is(err, ErrLimit) {
			t.Fatalf("a locked pod must refuse before any proof, got %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run must return ErrLimit at once, not block")
	}
}

// pairedPod is a pod identity with an account pinned and its own certificate, for the cert
// mode. The phrase is the one of the shared vectors.
func pairedPod(t *testing.T) (*identity.PodIdentity, *identity.Account) {
	t.Helper()
	a, err := identity.FromPhrase("lecture vapor excuse note pledge rough bundle start regular burden reveal theme banana pencil owner cube social job emotion member joy sting dash whisper")
	if err != nil {
		t.Fatal(err)
	}
	p, err := identity.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Pin(a.ID); err != nil {
		t.Fatal(err)
	}
	if err := p.SetCert(identity.NewCert(a, p.Key(), "test pod", identity.KindPod)); err != nil {
		t.Fatal(err)
	}
	return p, a
}

// browserDevice is a browser device key and the certificate the account signed for it.
func browserDevice(t *testing.T, a *identity.Account) (ed25519.PrivateKey, *identity.Cert) {
	t.Helper()
	key := identity.KeyFromSeed(identity.Derive(a.Seed, identity.InfoDevice))
	return key, identity.NewCert(a, key, "test browser", identity.KindBrowser)
}

// TestHandshakeCertGood: both sides prove their device certificate signed by the pinned
// account key, and sign the binding string with their device key.
func TestHandshakeCertGood(t *testing.T) {
	pod, account := pairedPod(t)
	key, cert := browserDevice(t, account)
	fpB, fpP := "AABBCCDD", "11223344"
	p := newPipe()
	h := &Handshake{Identity: pod, FPBrowser: fpB, FPPod: fpP}
	errCh := make(chan error, 1)
	go func() { errCh <- h.Run(p.a) }()

	code, podCert, ready := p.certBrowser(t, key, cert, account.ID, fpB, fpP)
	if code != "" || !ready {
		t.Fatalf("the cert handshake must complete (code=%q ready=%v)", code, ready)
	}
	if podCert == nil || podCert.Key != pod.ID() {
		t.Fatalf("the pod must answer with its own certificate: %+v", podCert)
	}
	if h.PeerCert == nil || h.PeerCert.Key != identity.KeyID(key) {
		t.Fatalf("the pod must have read the browser certificate: %+v", h.PeerCert)
	}
	if err := <-errCh; err != nil {
		t.Fatalf("handshake returned %v", err)
	}
}

// TestHandshakeCertUnpaired: a paired browser meets a pod that has no account. The pod
// answers "unpaired" so the browser can fall back to the token mode.
func TestHandshakeCertUnpaired(t *testing.T) {
	a, err := identity.FromPhrase("lecture vapor excuse note pledge rough bundle start regular burden reveal theme banana pencil owner cube social job emotion member joy sting dash whisper")
	if err != nil {
		t.Fatal(err)
	}
	pod, err := identity.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	key, cert := browserDevice(t, a)
	p := newPipe()
	h := &Handshake{Identity: pod, FPBrowser: "A", FPPod: "B"}
	errCh := make(chan error, 1)
	go func() { errCh <- h.Run(p.a) }()
	code, _, ready := p.certBrowser(t, key, cert, a.ID, "A", "B")
	if code != CodeUnpaired || ready {
		t.Fatalf("an unpaired pod must answer %q, got %q (ready=%v)", CodeUnpaired, code, ready)
	}
	if err := <-errCh; !errors.Is(err, ErrUnpaired) {
		t.Fatalf("unpaired must give ErrUnpaired, got %v", err)
	}
}

// TestHandshakeCertForeignAccount: a browser of another account, with a certificate signed
// by its own account key, is refused — the pinned key is the only guard.
func TestHandshakeCertForeignAccount(t *testing.T) {
	pod, _ := pairedPod(t)
	other, err := identity.FromPhrase("abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon about")
	if err != nil {
		t.Fatal(err)
	}
	key := identity.KeyFromSeed(identity.Derive(other.Seed, identity.InfoDevice))
	foreign := identity.NewCert(other, key, "other browser", identity.KindBrowser)
	p := newPipe()
	h := &Handshake{Identity: pod, FPBrowser: "A", FPPod: "B"}
	errCh := make(chan error, 1)
	go func() { errCh <- h.Run(p.a) }()
	code, _, _ := p.certBrowser(t, key, foreign, other.ID, "A", "B")
	if code != CodeForeign {
		t.Fatalf("a certificate of another account must answer %q, got %q", CodeForeign, code)
	}
	if err := <-errCh; !errors.Is(err, identity.ErrForeignAccount) {
		t.Fatalf("foreign account must give ErrForeignAccount, got %v", err)
	}
}

// TestHandshakeCertRevoked: a device on the signed revocation list is refused.
func TestHandshakeCertRevoked(t *testing.T) {
	pod, account := pairedPod(t)
	key, cert := browserDevice(t, account)
	r := &identity.Revocations{Account: account.ID, Seq: 1, Epoch: 1, Revoked: []string{cert.Key}, Created: 1766721600}
	if err := identity.SignRevocations(account.Priv, r); err != nil {
		t.Fatal(err)
	}
	if err := pod.ApplyRevocations(r); err != nil {
		t.Fatal(err)
	}
	p := newPipe()
	h := &Handshake{Identity: pod, FPBrowser: "A", FPPod: "B"}
	errCh := make(chan error, 1)
	go func() { errCh <- h.Run(p.a) }()
	code, _, _ := p.certBrowser(t, key, cert, account.ID, "A", "B")
	if code != CodeRevoked {
		t.Fatalf("a revoked device must answer %q, got %q", CodeRevoked, code)
	}
	if err := <-errCh; !errors.Is(err, identity.ErrRevoked) {
		t.Fatalf("a revoked device must give ErrRevoked, got %v", err)
	}
}

// TestHandshakeCertSwappedFingerprint: a relay that swaps the offer changes the fingerprint
// the browser binds, so the signature the pod checks no longer matches.
func TestHandshakeCertSwappedFingerprint(t *testing.T) {
	pod, account := pairedPod(t)
	key, cert := browserDevice(t, account)
	p := newPipe()
	h := &Handshake{Identity: pod, FPBrowser: "AABBCCDD", FPPod: "11223344"}
	errCh := make(chan error, 1)
	go func() { errCh <- h.Run(p.a) }()
	// The browser signs with the fingerprint the relay gave it, not the one the pod sees.
	nonceB := nonce()
	p.send("$hello", helloParams{Protocol: Protocol, Version: Version, Nonce: nonceB, Cert: cert})
	b := p.b.read(2 * time.Second)
	var env testEnv
	if b == nil {
		t.Fatal("the pod must answer the hello")
	}
	_ = json.Unmarshal(b, &env)
	var podHello helloParams
	_ = json.Unmarshal(env.Params, &podHello)
	if podHello.Error != "" {
		t.Fatalf("the pod must accept the certificate, got %q", podHello.Error)
	}
	p.send("$auth", authParams{Sig: identity.Sign(key, []byte(Binding(RoleBrowser, "DEADBEEF", "11223344", nonceB, podHello.Nonce)))})
	if err := <-errCh; !errors.Is(err, ErrProof) {
		t.Fatalf("a swapped fingerprint must give ErrProof, got %v", err)
	}
}

// TestHandshakeTokenRefusedOncePaired: a paired pod refuses the M1 token proof through the
// relay — its peers must show a certificate.
func TestHandshakeTokenRefusedOncePaired(t *testing.T) {
	pod, _ := pairedPod(t)
	p := newPipe()
	h := &Handshake{Identity: pod, Token: "tok-0123456789abcdef", FPBrowser: "A", FPPod: "B"}
	errCh := make(chan error, 1)
	go func() { errCh <- h.Run(p.a) }()

	nonceB := nonce()
	p.send("$hello", helloParams{Protocol: Protocol, Version: VersionToken, Nonce: nonceB})
	b := p.b.read(2 * time.Second)
	var env testEnv
	if b == nil {
		t.Fatal("the pod must answer the hello")
	}
	_ = json.Unmarshal(b, &env)
	var podHello helloParams
	_ = json.Unmarshal(env.Params, &podHello)
	if podHello.Error != CodeCert {
		t.Fatalf("a paired pod must answer %q to the token mode, got %q", CodeCert, podHello.Error)
	}
	if err := <-errCh; !errors.Is(err, ErrCertRequired) {
		t.Fatalf("the token mode on a paired pod must give ErrCertRequired, got %v", err)
	}
}

// TestHandshakeCertBothSides: Run and RunBrowserCert complete the exchange together.
func TestHandshakeCertBothSides(t *testing.T) {
	pod, account := pairedPod(t)
	key, cert := browserDevice(t, account)
	p := newPipe()
	go func() {
		podCert, err := RunBrowserCert(p.b, key, cert, account.ID, "BROWSER-FP", "POD-FP", "NONCE-B")
		if err != nil {
			t.Errorf("RunBrowserCert: %v", err)
			return
		}
		if podCert == nil || podCert.Key != pod.ID() {
			t.Errorf("the browser must read the pod certificate: %+v", podCert)
		}
	}()
	h := &Handshake{Identity: pod, FPBrowser: "BROWSER-FP", FPPod: "POD-FP"}
	if err := h.Run(p.a); err != nil {
		t.Fatal("the cert handshake must complete:", err)
	}
}
