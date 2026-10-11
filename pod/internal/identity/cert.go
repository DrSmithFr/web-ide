package identity

import (
	"crypto/ed25519"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/DrSmithFr/web-ide/pod/internal/i18n"
)

// Errors of a certificate check. The handshake maps them to the codes sent to the browser.
// They are written with i18n because the page shows them: the browser module of the identity
// returns exactly the same texts, so one catalog covers both sides.
var (
	ErrNoCert         = i18n.New("no certificate")
	ErrForeignAccount = i18n.New("certificate of another account")
	ErrBadSignature   = i18n.New("the certificate signature does not match")
	ErrRevoked        = i18n.New("device revoked")
)

// Kinds of device, as written in the certificate.
const (
	KindBrowser = "browser"
	KindPod     = "pod"
)

// Scope is always "all" for now: the field exists so that a read-only or per-project
// device needs no new format.
const ScopeAll = "all"

// Cert is a device certificate: the device public key, its name, its kind, the date, and
// the signature of the account key over the canonical form of those fields.
type Cert struct {
	V       int    `json:"v"`
	Account string `json:"account"`
	Key     string `json:"key"`
	Name    string `json:"name"`
	Kind    string `json:"kind"`
	Created int64  `json:"created"`
	Scope   string `json:"scope"`
	Sig     string `json:"sig"`
}

// Canonical writes the certificate as the bytes the signature covers: fixed field order, no
// spaces. The browser builds the same string, so a certificate signed in one is verified in
// the other.
func (c *Cert) Canonical() string {
	return fmt.Sprintf(`{"v":%d,"account":%s,"key":%s,"name":%s,"kind":%s,"created":%d,"scope":%s}`,
		c.V, jstr(c.Account), jstr(c.Key), jstr(c.Name), jstr(c.Kind), c.Created, jstr(c.Scope))
}

// NewCert builds and signs a certificate for a device key.
func NewCert(account *Account, device ed25519.PrivateKey, name, kind string) *Cert {
	return NewCertForPub(account, device.Public().(ed25519.PublicKey), name, kind)
}

// NewCertForPub signs a certificate for the public key of a device the admin read: the
// pairing flow. The admin holds the account key and the key the device posted, never its
// private key, so it signs for the public half it was shown — the fingerprint the joining
// device displays is the guard against a relay that swapped it.
func NewCertForPub(account *Account, pub ed25519.PublicKey, name, kind string) *Cert {
	c := &Cert{V: 1, Account: account.ID, Key: ID(pub), Name: name, Kind: kind, Created: time.Now().Unix(), Scope: ScopeAll}
	c.Sig = Sign(account.Priv, []byte(c.Canonical()))
	return c
}

// VerifyCert checks a certificate against the pinned account key: the signature, the account
// it claims, the scope, and the revocation list. The pod never trusts the relay to say who
// is who: it checks every peer itself.
func VerifyCert(c *Cert, pinned string, revoked *Revocations) error {
	if c == nil {
		return ErrNoCert
	}
	if c.V != 1 {
		return i18n.Errorf("certificate version %d", c.V)
	}
	if c.Account != pinned {
		return ErrForeignAccount
	}
	if c.Scope != ScopeAll {
		return i18n.Errorf("scope %q", c.Scope)
	}
	if !Verify(pinned, c.Sig, []byte(c.Canonical())) {
		return ErrBadSignature
	}
	if revoked.Has(c.Key) {
		return ErrRevoked
	}
	return nil
}

// Revocations is the signed revocation list of an account: the device keys an admin has
// revoked, and the epoch of the sync key rotated when revoking. The relay stores it and
// pushes it to the online pods; every pod checks it before accepting a peer.
type Revocations struct {
	Account string   `json:"account"`
	Seq     int64    `json:"seq"`
	Epoch   int64    `json:"epoch"`
	Revoked []string `json:"revoked"`
	Created int64    `json:"created"`
	Sig     string   `json:"sig"`
}

// Canonical writes the list as the bytes the signature covers: fixed order, no spaces, the
// revoked keys sorted so both sides build the same bytes.
func (r *Revocations) Canonical() string {
	revoked := append([]string{}, r.Revoked...)
	sort.Strings(revoked)
	parts := make([]string, len(revoked))
	for i, k := range revoked {
		parts[i] = jstr(k)
	}
	return fmt.Sprintf(`{"account":%s,"seq":%d,"epoch":%d,"revoked":[%s],"created":%d}`,
		jstr(r.Account), r.Seq, r.Epoch, strings.Join(parts, ","), r.Created)
}

// SignRevocations signs the list with the account key.
func SignRevocations(priv ed25519.PrivateKey, r *Revocations) error {
	r.Account = KeyID(priv)
	r.Sig = Sign(priv, []byte(r.Canonical()))
	return nil
}

// VerifyRevocations checks the list against the pinned account key.
func VerifyRevocations(r *Revocations, pinned string) error {
	if r == nil {
		return i18n.New("no revocation list")
	}
	if r.Account != pinned {
		return i18n.New("revocation list of another account")
	}
	if !Verify(pinned, r.Sig, []byte(r.Canonical())) {
		return i18n.New("the revocation list signature does not match")
	}
	return nil
}

// Has reports whether a device key is in the list. A nil list revokes nothing.
func (r *Revocations) Has(key string) bool {
	if r == nil {
		return false
	}
	for _, k := range r.Revoked {
		if k == key {
			return true
		}
	}
	return false
}
