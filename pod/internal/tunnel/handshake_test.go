package tunnel

import (
	"encoding/json"
	"errors"
	"io"
	"sync"
	"testing"
	"time"
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

// browser performs the browser side of the handshake and reports the outcome.
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

func TestHandshakeGood(t *testing.T) {
	token, fpB, fpP := "tok-0123456789abcdef", "AABBCCDD", "11223344"
	p := newPipe()
	h := &Handshake{Token: token, FPBrowser: fpB, FPPod: fpP}
	errCh := make(chan error, 1)
	go func() { errCh <- h.Run(p.a) }()

	nonceB, nonceP, proof, ready, versionErr := p.browser(t, token, Version, fpB, fpP)
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
	p.browser(t, "WRONG-TOKEN", Version, fpB, fpP)
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
	p.browser(t, token, Version, "DEADBEEF", fpP)
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
	r.now = func() time.Time { return time.Now().Add(time.Hour + time.Second) }
	if !r.Allow() {
		t.Fatal("after the window the limit must reset")
	}
}
