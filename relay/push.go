package relay

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"log"
	"math/big"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/DrSmithFr/web-ide/lib/i18n"
	"github.com/DrSmithFr/web-ide/lib/identity"
)

// Web Push (RFC 8030) through the relay. The pod encrypts the payload for the subscription of
// the browser (RFC 8291), so the relay only holds an endpoint and forwards ciphertext: it
// signs the request with its VAPID key (RFC 8292) and POSTs it to the push service. It never
// POSTs to an endpoint that a signed, non-revoked browser of the same account did not
// register, so it is no open proxy.

// pushSigInfo prefixes what a browser signs to register or drop a subscription.
const pushSigInfo = "web-ide push v1"

// pushMaxAge is how old a signed subscription request may be.
const pushMaxAge = 10 * time.Minute

// pushMaxBody is the largest ciphertext forwarded (the push services take 4 KB).
const pushMaxBody = 4096

// pushHosts are the push services of the browsers: an endpoint elsewhere is refused, so a
// subscription cannot point the relay at an address of its own network.
var pushHosts = []string{"fcm.googleapis.com", "updates.push.services.mozilla.com", "web.push.apple.com", ".notify.windows.com"}

var (
	errPushOff     = i18n.New("push notifications are off on this relay")
	errPushService = i18n.New("this push service is not allowed")
	errPushOld     = i18n.New("the request is too old")
	errPushSig     = i18n.New("the signature does not match")
)

// Push is the Web Push side of the relay: its VAPID key, and the rate limit per account.
type Push struct {
	key     *ecdsa.PrivateKey
	pub     string // base64url of the uncompressed public point: the applicationServerKey
	subject string // the contact of the VAPID claims
	// TestEndpoint lets endpoints under this prefix through (the e2e fake push service).
	TestEndpoint string
	client       *http.Client

	mu      sync.Mutex
	buckets map[string]*bucket
}

// bucket is a token bucket: pushRate notifications a minute per account, pushBurst at once.
type bucket struct {
	tokens float64
	last   time.Time
}

const (
	pushRate  = 30.0
	pushBurst = 30.0
)

// LoadPush reads the VAPID key of the relay (vapid.json, 0600) or makes it on first run.
func LoadPush(dataDir, subject string) (*Push, error) {
	p := filepath.Join(dataDir, "vapid.json")
	var file struct {
		Private string `json:"private"`
	}
	var key *ecdsa.PrivateKey
	if b, err := os.ReadFile(p); err == nil {
		if err := json.Unmarshal(b, &file); err != nil {
			return nil, err
		}
		d, err := identity.B64d(file.Private)
		if err != nil {
			return nil, err
		}
		if key, err = ecdsa.ParseRawPrivateKey(elliptic.P256(), d); err != nil {
			return nil, err
		}
	} else {
		var err error
		if key, err = ecdsa.GenerateKey(elliptic.P256(), rand.Reader); err != nil {
			return nil, err
		}
		d, err := key.Bytes()
		if err != nil {
			return nil, err
		}
		file.Private = identity.B64u(d)
		b, _ := json.Marshal(file)
		if err := os.MkdirAll(dataDir, 0o700); err != nil {
			return nil, err
		}
		if err := os.WriteFile(p, b, 0o600); err != nil {
			return nil, err
		}
	}
	pub, err := key.PublicKey.Bytes()
	if err != nil {
		return nil, err
	}
	if subject == "" {
		subject = "https://github.com/DrSmithFr/web-ide"
	}
	return &Push{key: key, pub: identity.B64u(pub), subject: subject, buckets: map[string]*bucket{},
		// No redirect: the relay POSTs to the registered endpoint and nowhere else.
		client: &http.Client{Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}

// PublicKey is the VAPID public key, base64url (the applicationServerKey of the browsers).
func (p *Push) PublicKey() string { return p.pub }

// allowed reports whether the relay may POST to an endpoint: https on the default port of a
// known push service, or the test prefix.
func (p *Push) allowed(endpoint string) bool {
	if p.TestEndpoint != "" && strings.HasPrefix(endpoint, p.TestEndpoint) {
		return true
	}
	u, err := url.Parse(endpoint)
	if err != nil || u.Scheme != "https" || u.Port() != "" || u.User != nil {
		return false
	}
	host := strings.ToLower(u.Hostname())
	for _, h := range pushHosts {
		if host == h || (strings.HasPrefix(h, ".") && strings.HasSuffix(host, h)) {
			return true
		}
	}
	return false
}

// take spends one token of the bucket of an account.
func (p *Push) take(account string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	now := time.Now()
	b := p.buckets[account]
	if b == nil {
		b = &bucket{tokens: pushBurst, last: now}
		p.buckets[account] = b
	}
	b.tokens = min(pushBurst, b.tokens+now.Sub(b.last).Minutes()*pushRate)
	b.last = now
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

// jwt is the VAPID token for the origin of an endpoint (RFC 8292): ES256, 12 hours.
func (p *Push) jwt(endpoint string) (string, error) {
	u, err := url.Parse(endpoint)
	if err != nil {
		return "", err
	}
	head := identity.B64u([]byte(`{"typ":"JWT","alg":"ES256"}`))
	claims, _ := json.Marshal(map[string]any{"aud": u.Scheme + "://" + u.Host, "exp": time.Now().Add(12 * time.Hour).Unix(), "sub": p.subject})
	signed := head + "." + identity.B64u(claims)
	sum := sha256.Sum256([]byte(signed))
	r, s, err := ecdsa.Sign(rand.Reader, p.key, sum[:])
	if err != nil {
		return "", err
	}
	sig := make([]byte, 64)
	r.FillBytes(sig[:32])
	s.FillBytes(sig[32:])
	return signed + "." + identity.B64u(sig), nil
}

// verifyJWT checks a VAPID token against a public key (tests).
func verifyJWT(token, pub string) bool {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return false
	}
	raw, err := identity.B64d(pub)
	if err != nil {
		return false
	}
	key, err := ecdsa.ParseUncompressedPublicKey(elliptic.P256(), raw)
	if err != nil {
		return false
	}
	sig, err := identity.B64d(parts[2])
	if err != nil || len(sig) != 64 {
		return false
	}
	sum := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	return ecdsa.Verify(key, sum[:], new(big.Int).SetBytes(sig[:32]), new(big.Int).SetBytes(sig[32:]))
}

// send POSTs a ciphertext to an endpoint; it answers the status of the push service.
func (p *Push) send(endpoint string, body []byte, ttl int, urgency, topic string) (int, error) {
	token, err := p.jwt(endpoint)
	if err != nil {
		return 0, err
	}
	req, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Authorization", "vapid t="+token+", k="+p.pub)
	req.Header.Set("Content-Encoding", "aes128gcm")
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("TTL", strconv.Itoa(max(0, ttl)))
	switch urgency {
	case "very-low", "low", "normal", "high":
		req.Header.Set("Urgency", urgency)
	}
	if topic != "" {
		req.Header.Set("Topic", topic)
	}
	res, err := p.client.Do(req)
	if err != nil {
		return 0, err
	}
	res.Body.Close()
	return res.StatusCode, nil
}

// pushRequest is what a browser posts to register or drop a subscription: signed by its
// device key, with its certificate.
type pushRequest struct {
	Cert     *identity.Cert `json:"cert"`
	Endpoint string         `json:"endpoint,omitempty"`
	Sub      string         `json:"sub,omitempty"`
	Created  int64          `json:"created"` // unix milliseconds
	Sig      string         `json:"sig"`
}

// pushSigned is the text a browser signs: info|action|endpoint or sub|created.
func pushSigned(action, target string, created int64) string {
	return pushSigInfo + "|" + action + "|" + target + "|" + strconv.FormatInt(created, 10)
}

// check verifies the certificate (the account it claims, not revoked on the relay's list), the
// age and the signature of the device key.
func (s *Server) checkPushRequest(r *pushRequest, action, target string) error {
	if r.Cert == nil {
		return identity.ErrNoCert
	}
	list, err := s.store.Revocations(r.Cert.Account)
	if err != nil {
		return err
	}
	if err := identity.VerifyCert(r.Cert, r.Cert.Account, list); err != nil {
		return err
	}
	if age := time.Since(time.UnixMilli(r.Created)); age > pushMaxAge || age < -pushMaxAge {
		return errPushOld
	}
	if !identity.Verify(r.Cert.Key, r.Sig, []byte(pushSigned(action, target, r.Created))) {
		return errPushSig
	}
	return nil
}

// servePushKey answers the VAPID public key.
func (s *Server) servePushKey(w http.ResponseWriter, r *http.Request) {
	if s.Push == nil {
		pushError(w, r, http.StatusNotFound, errPushOff)
		return
	}
	writeJSON(w, map[string]string{"key": s.Push.PublicKey()})
}

// servePushSubscribe registers the endpoint of a browser for its account: an opaque id, the
// one the pods of the account will name.
func (s *Server) servePushSubscribe(w http.ResponseWriter, r *http.Request) {
	if s.Push == nil {
		pushError(w, r, http.StatusNotFound, errPushOff)
		return
	}
	var req pushRequest
	if r.Method != http.MethodPost || json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&req) != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if !s.Push.allowed(req.Endpoint) {
		pushError(w, r, http.StatusForbidden, errPushService)
		return
	}
	if err := s.checkPushRequest(&req, "subscribe", req.Endpoint); err != nil {
		pushError(w, r, http.StatusForbidden, err)
		return
	}
	id, err := s.store.AddPushSub(req.Cert.Account, req.Cert.Key, req.Endpoint)
	if err != nil {
		pushError(w, r, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, map[string]string{"sub": id})
}

// servePushUnsubscribe drops a subscription of the device that signs.
func (s *Server) servePushUnsubscribe(w http.ResponseWriter, r *http.Request) {
	if s.Push == nil {
		pushError(w, r, http.StatusNotFound, errPushOff)
		return
	}
	var req pushRequest
	if r.Method != http.MethodPost || json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&req) != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if err := s.checkPushRequest(&req, "unsubscribe", req.Sub); err != nil {
		pushError(w, r, http.StatusForbidden, err)
		return
	}
	if err := s.store.DeletePushSub(req.Sub, req.Cert.Account, req.Cert.Key); err != nil {
		pushError(w, r, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, map[string]bool{"ok": true})
}

// pushSend is the message of a pod: a ciphertext for a subscription of its account.
type pushSend struct {
	Sub     string `json:"sub"`
	Body    []byte `json:"body"`
	TTL     int    `json:"ttl"`
	Urgency string `json:"urgency"`
	Topic   string `json:"topic"`
}

// pushFromPod forwards the ciphertext of a pod. Only a pod paired with a certificate the relay
// checked, for a subscription of its own account, within the rate of the account. A push
// service that answers 404 or 410 dropped the subscription: the relay deletes it and tells the
// pod. Nothing of the payload is stored or logged.
func (s *Server) pushFromPod(pc *podConn, raw []byte) {
	var m pushSend
	if s.Push == nil || json.Unmarshal(raw, &m) != nil || pc.certified == "" || len(m.Body) == 0 || len(m.Body) > pushMaxBody {
		return
	}
	account, endpoint, ok := s.store.PushSub(m.Sub)
	if !ok || account != pc.certified || !s.Push.allowed(endpoint) {
		_ = pc.send(sigMsg{Type: "push.gone", ID: m.Sub})
		return
	}
	if !s.Push.take(account) {
		return
	}
	go func() {
		code, err := s.Push.send(endpoint, m.Body, m.TTL, m.Urgency, m.Topic)
		switch {
		case err != nil:
			log.Print("relay: push: the push service is not reachable") // the endpoint is a secret: not logged
		case code == http.StatusGone || code == http.StatusNotFound:
			_ = s.store.DeletePushSub(m.Sub, account, "")
			_ = pc.send(sigMsg{Type: "push.gone", ID: m.Sub})
		case code >= 300:
			log.Printf("relay: push: the push service answered %d", code)
		}
	}()
}

func pushError(w http.ResponseWriter, r *http.Request, code int, err error) {
	lang := i18n.Detect(r.Header.Get("Accept-Language"))
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": i18n.Translate(lang, err)})
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
