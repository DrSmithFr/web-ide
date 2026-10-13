package tunnel

import (
	"crypto/ed25519"
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

	"github.com/DrSmithFr/web-ide/pod/internal/identity"
)

// The tunnel protocol of the rpc channel. Both sides prove who they are by signing a
// binding string built from both DTLS fingerprints and two nonces, so a relay that swaps
// the SDP cannot forge the proof. Two modes:
//   - version 2 (cert mode): each side signs with its device key and checks the other's
//     certificate against the pinned account key and the revocation list;
//   - version 1 (token mode): the M1 HMAC of the pod token, kept for a pod that is not
//     paired. A paired pod refuses it.
const (
	Protocol     = "web-ide-rpc"
	Version      = "2"
	VersionToken = "1"
)

// Codes of the handshake errors, sent as $hello{error}. The page shows its own text for
// each one.
const (
	CodeVersion  = "version"
	CodeUnpaired = "unpaired" // the pod has no account: the browser falls back to the token
	CodeCert     = "cert"     // a paired pod refuses the token mode, or the peer gave no cert
	CodeForeign  = "foreign"  // the certificate was not signed by the pinned account
	CodeRevoked  = "revoked"
	CodeProof    = "proof"
)

// Errors of the handshake.
var (
	ErrVersion      = errors.New("tunnel: protocol or version mismatch")
	ErrProof        = errors.New("tunnel: the proof does not match")
	ErrLimit        = errors.New("tunnel: too many failed proofs, try later")
	ErrUnpaired     = errors.New("tunnel: the pod is not paired with an account")
	ErrCertRequired = errors.New("tunnel: a paired pod refuses the token mode")
)

// Role names used in the binding string.
const (
	RolePod     = "pod"
	RoleBrowser = "browser"
)

// Binding returns the bytes each side signs: role|fpBrowser|fpPod|nonceBrowser|noncePod.
// The canonical order is always browser, then pod, so both sides bind the same bytes.
func Binding(role, fpBrowser, fpPod, nonceBrowser, noncePod string) string {
	return role + "|" + fpBrowser + "|" + fpPod + "|" + nonceBrowser + "|" + noncePod
}

// MAC returns the hex HMAC-SHA256 that binds the token to both DTLS fingerprints and the
// two nonces, for the given role (the token mode).
func MAC(role, token, fpBrowser, fpPod, nonceBrowser, noncePod string) string {
	m := hmac.New(sha256.New, []byte(token))
	m.Write([]byte(Binding(role, fpBrowser, fpPod, nonceBrowser, noncePod)))
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
	Protocol string         `json:"protocol"`
	Version  string         `json:"version"`
	Nonce    string         `json:"nonce,omitempty"`
	Proof    string         `json:"proof,omitempty"` // token mode
	Cert     *identity.Cert `json:"cert,omitempty"`  // cert mode
	Sig      string         `json:"sig,omitempty"`   // cert mode
	// Via: the certificate of a browser the peer showing this $hello vouches for — a pod that
	// reached another pod through this one. The pod reading it checks it against its own list.
	Via   *identity.Cert `json:"via,omitempty"`
	Error string         `json:"error,omitempty"`
}

type authParams struct {
	Proof string `json:"proof,omitempty"` // token mode
	Sig   string `json:"sig,omitempty"`   // cert mode
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
	Token     string                // the pod token, for the token mode
	Identity  *identity.PodIdentity // the paired pod: its device key, its pinned account
	FPBrowser string
	FPPod     string
	Limits    *RateLimiter
	// Deadline bounds the $hello/$auth reads; 0 = 10 s.
	Deadline time.Duration
	// PeerCert is the certificate the peer showed (nil in token mode), set by Run.
	PeerCert *identity.Cert
	// Via is the certificate of the browser a peer vouches for, when the peer sent one.
	Via *identity.Cert
}

// deadline returns the bounded window for the handshake reads.
func (h *Handshake) deadline() time.Duration {
	if h.Deadline > 0 {
		return h.Deadline
	}
	return 10 * time.Second
}

// Run performs the exchange: read the browser $hello, answer with the pod proof, read the
// browser $auth, verify it, answer $ready. The whole exchange has a 10 s deadline: a
// browser that stalls it is dropped. On a bad proof it rate-limits and returns ErrProof
// (or ErrLimit once the limit is reached); once the limit is reached it refuses before
// verifying any proof at all.
func (h *Handshake) Run(conn MsgConn) error {
	if h.Limits != nil && h.Limits.Locked() {
		return ErrLimit
	}
	// 1. browser -> $hello{protocol, version, nonce, [cert]}
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
	switch hi.Version {
	case VersionToken:
		return h.runToken(conn, hi)
	case Version:
		return h.runCert(conn, hi)
	default:
		_ = h.sendError(conn, CodeVersion)
		return ErrVersion
	}
}

// runToken is the M1 mode: the browser proves the pod token with an HMAC bound to both
// fingerprints. A paired pod refuses it — its peers must show a certificate.
func (h *Handshake) runToken(conn MsgConn, hi helloParams) error {
	if h.Identity != nil && h.Identity.Paired() {
		_ = h.sendError(conn, CodeCert)
		return ErrCertRequired
	}
	nonceP := nonce()
	proof := MAC(RolePod, h.Token, h.FPBrowser, h.FPPod, hi.Nonce, nonceP)
	// pod -> $hello{protocol, version, nonce, proof}
	if err := h.send(conn, "$hello", helloParams{Protocol: Protocol, Version: VersionToken, Nonce: nonceP, Proof: proof}); err != nil {
		return err
	}
	// browser -> $auth{proof}
	var a envelope
	b, err := h.read(conn)
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
		if !h.count() {
			return ErrLimit
		}
		return ErrProof
	}
	// pod -> $ready
	return h.send(conn, "$ready", map[string]any{"ok": true})
}

// runCert is the identity mode: each side signs the binding string with its device key and
// checks the other's certificate against the pinned account key and the revocation list.
// The pod never trusts the relay to say who is who.
func (h *Handshake) runCert(conn MsgConn, hi helloParams) error {
	if h.Identity == nil || !h.Identity.Paired() {
		// The browser is paired but this pod is not: it falls back to the token mode.
		_ = h.sendError(conn, CodeUnpaired)
		return ErrUnpaired
	}
	if hi.Cert == nil {
		_ = h.sendError(conn, CodeCert)
		return identity.ErrNoCert
	}
	// A peer that vouches for a browser (a gateway): the browser it names is checked here,
	// against the list this pod holds, not the one the gateway was told with.
	if hi.Via != nil {
		if err := h.Identity.PeerError(hi.Via); err != nil {
			if !h.count() {
				return ErrLimit
			}
			_ = h.sendError(conn, code(err))
			return err
		}
		h.Via = hi.Via
	}
	if err := h.Identity.PeerError(hi.Cert); err != nil {
		if !h.count() {
			return ErrLimit
		}
		_ = h.sendError(conn, code(err))
		return err
	}
	h.PeerCert = hi.Cert
	nonceP := nonce()
	sig := identity.Sign(h.Identity.Key(), []byte(Binding(RolePod, h.FPBrowser, h.FPPod, hi.Nonce, nonceP)))
	_, own, _ := h.Identity.State()
	// pod -> $hello{protocol, version, nonce, cert, sig}
	if err := h.send(conn, "$hello", helloParams{Protocol: Protocol, Version: Version, Nonce: nonceP, Cert: own, Sig: sig}); err != nil {
		return err
	}
	// browser -> $auth{sig}
	var a envelope
	b, err := h.read(conn)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(b, &a); err != nil || a.Method != "$auth" {
		return ErrProof
	}
	var ai authParams
	_ = json.Unmarshal(a.Params, &ai)
	if !identity.Verify(hi.Cert.Key, ai.Sig, []byte(Binding(RoleBrowser, h.FPBrowser, h.FPPod, hi.Nonce, nonceP))) {
		if !h.count() {
			return ErrLimit
		}
		_ = h.sendError(conn, CodeProof)
		return ErrProof
	}
	// pod -> $ready
	return h.send(conn, "$ready", map[string]any{"ok": true})
}

// SendError writes one $hello{error: code} refusal on a channel, for a peer refused before the
// handshake runs (a browser the gateway vouches for is revoked here).
func SendError(conn MsgConn, code string) error {
	return sendMsg(conn, "$hello", helloParams{Protocol: Protocol, Version: Version, Error: code})
}

// ErrDeadline is returned when the browser does not answer within the handshake window.
var ErrDeadline = errors.New("tunnel: the handshake timed out")

// read returns the next message, or ErrDeadline if it takes longer than the handshake
// window. It runs the (blocking) Read in a goroutine so the deadline can cut it.
func (h *Handshake) read(conn MsgConn) ([]byte, error) {
	type r struct {
		b   []byte
		err error
	}
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
	return sendMsg(conn, method, params)
}

func (h *Handshake) sendError(conn MsgConn, code string) error {
	return h.send(conn, "$hello", helloParams{Protocol: Protocol, Version: Version, Error: code})
}

// count records a failed proof and reports whether the peer is still within the limit.
func (h *Handshake) count() bool { return h.Limits == nil || h.Limits.Allow() }

// code maps an identity error to the code sent to the browser.
func code(err error) string {
	switch {
	case errors.Is(err, identity.ErrForeignAccount):
		return CodeForeign
	case errors.Is(err, identity.ErrRevoked):
		return CodeRevoked
	case errors.Is(err, identity.ErrNoCert):
		return CodeCert
	}
	return CodeProof
}

// sendMsg writes one envelope to the transport (shared by both sides).
func sendMsg(conn MsgConn, method string, params any) error {
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

// RunBrowser is the browser side of the token mode: it sends $hello{protocol, version,
// nonce}, reads the pod's $hello (verifying its proof and the protocol), sends
// $auth{proof}, and waits for $ready. It returns ErrProof on a bad pod proof.
func RunBrowser(conn MsgConn, token, fpBrowser, fpPod, nonceB string) error {
	if err := sendMsg(conn, "$hello", helloParams{Protocol: Protocol, Version: VersionToken, Nonce: nonceB}); err != nil {
		return err
	}
	// pod -> $hello{protocol, version, nonce, proof}
	var e envelope
	b, err := conn.Read()
	if err != nil {
		return err
	}
	if err := json.Unmarshal(b, &e); err != nil || e.Method != "$hello" {
		return ErrProof
	}
	var hi helloParams
	_ = json.Unmarshal(e.Params, &hi)
	if hi.Protocol != Protocol || hi.Version != VersionToken {
		return ErrVersion
	}
	if hi.Error != "" {
		return errors.New("tunnel: " + hi.Error)
	}
	if !equalMac(hi.Proof, MAC(RolePod, token, fpBrowser, fpPod, nonceB, hi.Nonce)) {
		return ErrProof
	}
	if err := sendMsg(conn, "$auth", authParams{Proof: MAC(RoleBrowser, token, fpBrowser, fpPod, nonceB, hi.Nonce)}); err != nil {
		return err
	}
	// pod -> $ready
	var r envelope
	b, err = conn.Read()
	if err != nil {
		return err
	}
	if err := json.Unmarshal(b, &r); err != nil || r.Method != "$ready" {
		return ErrProof
	}
	return nil
}

// RunBrowserCert is the browser side of the cert mode: it shows its certificate, checks the
// pod's certificate against the pinned account, signs the binding string with its device
// key and checks the pod's signature. It returns the pod's certificate.
func RunBrowserCert(conn MsgConn, key ed25519.PrivateKey, cert *identity.Cert, account, fpBrowser, fpPod, nonceB string) (*identity.Cert, error) {
	return RunBrowserCertVia(conn, key, cert, account, fpBrowser, fpPod, nonceB, nil)
}

// RunBrowserCertVia is RunBrowserCert for a peer that vouches for a browser: it shows its own
// certificate and the certificate (via) of the browser it relays, which the pod checks against
// the list it holds itself.
func RunBrowserCertVia(conn MsgConn, key ed25519.PrivateKey, cert *identity.Cert, account, fpBrowser, fpPod, nonceB string, via *identity.Cert) (*identity.Cert, error) {
	if err := sendMsg(conn, "$hello", helloParams{Protocol: Protocol, Version: Version, Nonce: nonceB, Cert: cert, Via: via}); err != nil {
		return nil, err
	}
	// pod -> $hello{protocol, version, nonce, cert, sig}
	var e envelope
	b, err := conn.Read()
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(b, &e); err != nil || e.Method != "$hello" {
		return nil, ErrProof
	}
	var hi helloParams
	_ = json.Unmarshal(e.Params, &hi)
	if hi.Protocol != Protocol || hi.Version != Version {
		return nil, ErrVersion
	}
	if hi.Error != "" {
		return nil, errors.New("tunnel: " + hi.Error)
	}
	if err := identity.VerifyCert(hi.Cert, account, nil); err != nil {
		return nil, err
	}
	if !identity.Verify(hi.Cert.Key, hi.Sig, []byte(Binding(RolePod, fpBrowser, fpPod, nonceB, hi.Nonce))) {
		return nil, ErrProof
	}
	if err := sendMsg(conn, "$auth", authParams{Sig: identity.Sign(key, []byte(Binding(RoleBrowser, fpBrowser, fpPod, nonceB, hi.Nonce)))}); err != nil {
		return nil, err
	}
	// pod -> $ready
	var r envelope
	b, err = conn.Read()
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(b, &r); err != nil || r.Method != "$ready" {
		return nil, ErrProof
	}
	return hi.Cert, nil
}
