package tunnel

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"time"
)

// The tunnel protocol of the rpc channel: the browser and the pod prove the pod token to
// each other, bound to both DTLS fingerprints and two nonces, so a relay that swaps the
// SDP cannot forge the proof. The token itself never travels over the wire.
const (
	Protocol = "web-ide-rpc"
	Version  = "1"
)

// Errors of the handshake.
var (
	ErrVersion = errors.New("tunnel: protocol or version mismatch")
	ErrProof   = errors.New("tunnel: the token proof does not match")
	ErrLimit   = errors.New("tunnel: too many failed proofs, try later")
)

// Role names used in the binding string.
const (
	RolePod     = "pod"
	RoleBrowser = "browser"
)

// MAC returns the hex HMAC-SHA256 that binds the token to both DTLS fingerprints and the
// two nonces, for the given role. The canonical order is always browser, then pod, so both
// sides bind the same bytes:  role|fpBrowser|fpPod|nonceBrowser|noncePod.
func MAC(role, token, fpBrowser, fpPod, nonceBrowser, noncePod string) string {
	msg := role + "|" + fpBrowser + "|" + fpPod + "|" + nonceBrowser + "|" + noncePod
	m := hmac.New(sha256.New, []byte(token))
	m.Write([]byte(msg))
	return hex.EncodeToString(m.Sum(nil))
}

// Fingerprint extracts the DTLS fingerprint from an SDP and canonicalizes it (uppercase,
// no colons) so both sides bind the same bytes. It reads the a=fingerprint line.
func Fingerprint(sdp string) string {
	for _, line := range strings.Split(sdp, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "a=fingerprint:") {
			continue
		}
		v := strings.TrimSpace(strings.TrimPrefix(line, "a=fingerprint:"))
		// v is "sha-256 AA:BB:CC:..." ; keep the hex part.
		if i := strings.IndexByte(v, ' '); i >= 0 {
			v = v[i+1:]
		}
		return strings.ToUpper(strings.ReplaceAll(v, ":", ""))
	}
	return ""
}

type envelope struct {
	ID     int64           `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
}

type helloParams struct {
	Protocol string `json:"protocol"`
	Version  string `json:"version"`
	Nonce    string `json:"nonce,omitempty"`
	Proof    string `json:"proof,omitempty"`
	Error    string `json:"error,omitempty"`
}

type authParams struct {
	Proof string `json:"proof"`
}

func nonce() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// Handshake drives the pod side of the exchange over the rpc channel. The relayclient
// builds it with the DTLS fingerprints from the SDP, runs it over the DataChannel, then
// hands the channel to the pod to serve the normal protocol.
type Handshake struct {
	Token     string
	FPBrowser string
	FPPod     string
	Limits    *RateLimiter
	// Deadline bounds the $hello/$auth reads; 0 = 10 s.
	Deadline time.Duration
}

// deadline returns the bounded window for the handshake reads.
func (h *Handshake) deadline() time.Duration {
	if h.Deadline > 0 {
		return h.Deadline
	}
	return 10 * time.Second
}

// Run performs: read the browser $hello, answer with the pod proof, read the browser
// $auth, verify it, answer $ready. The whole exchange has a 10 s deadline: a browser
// that stalls it is dropped. On a bad proof it rate-limits and returns ErrProof (or
// ErrLimit once the limit is reached); once the limit is reached it refuses before
// verifying any proof at all.
func (h *Handshake) Run(conn MsgConn) error {
	if h.Limits != nil && h.Limits.Locked() {
		return ErrLimit
	}
	// 1. browser -> $hello{protocol, version, nonce}
	var e envelope
	b, err := h.read(conn)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(b, &e); err != nil || e.Method != "$hello" {
		return ErrProof
	}
	var hi helloParams
	_ = json.Unmarshal(e.Params, &hi)
	if hi.Protocol != Protocol || hi.Version != Version {
		_ = h.send(conn, "$hello", helloParams{Error: "version"})
		return ErrVersion
	}
	nonceP := nonce()
	proof := MAC(RolePod, h.Token, h.FPBrowser, h.FPPod, hi.Nonce, nonceP)
	// 2. pod -> $hello{protocol, version, nonce, proof}
	_ = h.send(conn, "$hello", helloParams{Protocol: Protocol, Version: Version, Nonce: nonceP, Proof: proof})

	// 3. browser -> $auth{proof}
	var a envelope
	b, err = h.read(conn)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(b, &a); err != nil || a.Method != "$auth" {
		return ErrProof
	}
	var ai authParams
	_ = json.Unmarshal(a.Params, &ai)
	expected := MAC(RoleBrowser, h.Token, h.FPBrowser, h.FPPod, hi.Nonce, nonceP)
	if !equalMac(ai.Proof, expected) {
		if h.Limits != nil && !h.Limits.Allow() {
			return ErrLimit
		}
		return ErrProof
	}
	// 4. pod -> $ready
	return h.send(conn, "$ready", map[string]any{"ok": true})
}

// ErrDeadline is returned when the browser does not answer within the handshake window.
var ErrDeadline = errors.New("tunnel: the handshake timed out")

// read returns the next message, or ErrDeadline if it takes longer than the handshake
// window. It runs the (blocking) Read in a goroutine so the deadline can cut it.
func (h *Handshake) read(conn MsgConn) ([]byte, error) {
	type r struct{ b []byte; err error }
	ch := make(chan r, 1)
	go func() {
		b, err := conn.Read()
		ch <- r{b, err}
	}()
	select {
	case res := <-ch:
		return res.b, res.err
	case <-time.After(h.deadline()):
		_ = conn.Close() // unblock the reader goroutine
		return nil, ErrDeadline
	}
}

func (h *Handshake) send(conn MsgConn, method string, params any) error {
	data, _ := json.Marshal(envelope{Method: method, Params: mustMarshal(params)})
	return conn.Write(data)
}

func mustMarshal(v any) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}

// equalMac compares two MACs in constant time.
func equalMac(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

// RateLimiter allows at most N hits per window and reports when a hit is refused.
type RateLimiter struct {
	mu     sync.Mutex
	limit  int
	window time.Duration
	now    func() time.Time
	hits   []time.Time
}

// NewRateLimiter allows at most limit hits within window.
func NewRateLimiter(limit int, window time.Duration) *RateLimiter {
	return &RateLimiter{limit: limit, window: window, now: time.Now}
}

// Allow records a hit and reports whether it is within the limit. A hit over the limit
// is not recorded, so the window does not grow while the limit is reached.
func (r *RateLimiter) Allow() bool {
	if r == nil {
		return true
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	now := r.now()
	cut := now.Add(-r.window)
	var fresh []time.Time
	for _, t := range r.hits {
		if t.After(cut) {
			fresh = append(fresh, t)
		}
	}
	r.hits = fresh
	if len(r.hits) >= r.limit {
		return false
	}
	r.hits = append(r.hits, now)
	return true
}

// Locked reports whether the limit is already reached: the handshake refuses new
// browsers before verifying any proof, to make a lockout cheap.
func (r *RateLimiter) Locked() bool {
	if r == nil {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	now := r.now()
	cut := now.Add(-r.window)
	n := 0
	for _, t := range r.hits {
		if t.After(cut) {
			n++
		}
	}
	return n >= r.limit
}
