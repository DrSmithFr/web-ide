package identity

import (
	"crypto/ed25519"
	"fmt"
	"regexp"
	"time"

	"github.com/DrSmithFr/web-ide/lib/i18n"
)

// UpdateRequest is what a browser signs to ask one pod of its account to update itself
// (relay.md, *Multi-pod*): the pod it names, the release tag to install, and the moment the user
// asked. The signature is the device key of the browser, so a pod only updates itself for a device
// of its own account that it does not find on its revocation list, and a request captured once
// cannot be run again later.
//
// The certificate travels beside it, it is not in the bytes the signature covers: the pod checks
// it against the account it pinned, and the signature is verified against the key it names, so a
// swapped certificate breaks the signature.
type UpdateRequest struct {
	V       int    `json:"v"`
	Pod     string `json:"pod"`     // the device key of the pod to update
	Version string `json:"version"` // the release tag to install
	Created int64  `json:"created"` // ms: the freshness window of the request
	Cert    *Cert  `json:"cert"`    // the certificate of the device that signed it
	Sig     string `json:"sig"`
}

// Canonical writes the request as the bytes the signature covers: fixed field order, no spaces.
// The browser builds the same string (identity.js canonicalUpdate), so a request signed in the
// page is checked in the pod — the shared vectors of vectors.json pin both sides.
func (u *UpdateRequest) Canonical() string {
	return fmt.Sprintf(`{"v":%d,"pod":%s,"version":%s,"created":%d}`, u.V, jstr(u.Pod), jstr(u.Version), u.Created)
}

// SignUpdate signs a request with a device key (the browser side does the same with its own).
func SignUpdate(key ed25519.PrivateKey, u *UpdateRequest) string {
	return Sign(key, []byte(u.Canonical()))
}

// UpdateFreshness is how long an update request stays valid: long enough for it to travel from the
// browser to the pod, through a gateway or the relay, short enough that a captured one dies.
const UpdateFreshness = 5 * time.Minute

// releaseTag is a release version: the only thing a request may ask for, because it is what
// scripts/install.sh installs. A dev build (git describe with a hash, or "dev") never matches, so
// a pod built from the sources is never installed over a pod of a release.
var releaseTag = regexp.MustCompile(`^v\d+\.\d+\.\d+$`)

// IsReleaseTag reports whether a pod version is a release tag — and so, whether a pod running it
// may offer another pod to install it.
func IsReleaseTag(v string) bool { return releaseTag.MatchString(v) }

// CheckUpdate is what the pod named by a request checks before it touches its own binary: the
// certificate of the device that signed it (the pinned account, the scope, the revocation list),
// the signature over the canonical form, and the freshness. The version it names is checked by the
// one that runs it (a release tag, nothing else).
func CheckUpdate(u *UpdateRequest, pinned string, revoked *Revocations, now time.Time) error {
	if u == nil {
		return i18n.New("no update request")
	}
	if u.V != 1 {
		return i18n.Errorf("update request version %d", u.V)
	}
	if err := VerifyCert(u.Cert, pinned, revoked); err != nil {
		return err
	}
	if err := VerifySig(u.Cert.Key, u.Sig, u.Canonical()); err != nil {
		return err
	}
	age := now.Sub(time.UnixMilli(u.Created))
	if age < 0 {
		age = -age
	}
	if age > UpdateFreshness {
		return i18n.New("the request is too old")
	}
	return nil
}
