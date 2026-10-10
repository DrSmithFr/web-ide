package identity

import (
	"crypto/ed25519"
	"fmt"
	"sort"
	"strings"
	"time"
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
	c := &Cert{V: 1, Account: account.ID, Key: KeyID(device), Name: name, Kind: kind, Created: time.Now().Unix(), Scope: ScopeAll}
	c.Sig = Sign(account.Priv, []byte(c.Canonical()))
	return c
}

// VerifyCert checks a certificate against the pinned account key: the signature, the account
// it claims, the scope, and the revocation list. The pod never trusts the relay to say who
// is who: it checks every peer itself.
func VerifyCert(c *Cert, pinned string, revoked *Revocations) error {
	if c == nil {
		return fmt.Errorf("identity: no certificate")
	}
	if c.V != 1 {
		return fmt.Errorf("identity: certificate version %d", c.V)
	}
	if c.Account != pinned {
		return fmt.Errorf("identity: certificate of another account")
	}
	if c.Scope != ScopeAll {
		return fmt.Errorf("identity: scope %q", c.Scope)
	}
	if !Verify(pinned, c.Sig, []byte(c.Canonical())) {
		return fmt.Errorf("identity: the certificate signature does not match")
	}
	if revoked.Has(c.Key) {
		return fmt.Errorf("identity: device revoked")
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
		return fmt.Errorf("identity: no revocation list")
	}
	if r.Account != pinned {
		return fmt.Errorf("identity: revocation list of another account")
	}
	if !Verify(pinned, r.Sig, []byte(r.Canonical())) {
		return fmt.Errorf("identity: the revocation list signature does not match")
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
