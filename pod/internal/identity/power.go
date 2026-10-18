package identity

import (
	"crypto/ed25519"
	"fmt"
	"time"

	"github.com/DrSmithFr/web-ide/pod/internal/i18n"
)

// The actions a power request may ask for (relay.md, *Power*): wake a machine that sleeps, shut it
// down, reboot it, or restart the pod unit only.
const (
	PowerWake     = "wake"
	PowerShutdown = "shutdown"
	PowerReboot   = "reboot"
	PowerRestart  = "restart"
)

// PowerRequest is what a browser signs to ask one pod of its account to power its machine off,
// reboot it or restart itself, or to have an awake pod of its network wake it. It is the
// UpdateRequest of #60 with an action in place of a version: the same certificate beside it, the
// same freshness window, the same refusal of a request already run.
type PowerRequest struct {
	V       int    `json:"v"`
	Pod     string `json:"pod"`    // the device key of the pod the action is for
	Action  string `json:"action"` // wake, shutdown, reboot or restart
	Created int64  `json:"created"`
	Cert    *Cert  `json:"cert"`
	Sig     string `json:"sig"`
}

// Canonical writes the request as the bytes the signature covers (identity.js canonicalPower).
func (p *PowerRequest) Canonical() string {
	return fmt.Sprintf(`{"v":%d,"pod":%s,"action":%s,"created":%d}`, p.V, jstr(p.Pod), jstr(p.Action), p.Created)
}

// SignPower signs a request with a device key (the browser side does the same with its own).
func SignPower(key ed25519.PrivateKey, p *PowerRequest) string {
	return Sign(key, []byte(p.Canonical()))
}

// IsPowerAction reports whether an action is one a request may ask for.
func IsPowerAction(a string) bool {
	switch a {
	case PowerWake, PowerShutdown, PowerReboot, PowerRestart:
		return true
	}
	return false
}

// CheckPower is what a pod checks before it acts on a request: the checks of CheckUpdate, and an
// action it knows. Which pod the request must name is the business of the one that runs it: the
// target itself, or for a wake the awake pod that sends the packet.
func CheckPower(p *PowerRequest, pinned string, revoked *Revocations, now time.Time) error {
	if p == nil {
		return i18n.New("no power request")
	}
	if p.V != 1 {
		return i18n.Errorf("power request version %d", p.V)
	}
	if !IsPowerAction(p.Action) {
		return i18n.Errorf("unknown power action %q", p.Action)
	}
	if err := VerifyCert(p.Cert, pinned, revoked); err != nil {
		return err
	}
	if err := VerifySig(p.Cert.Key, p.Sig, p.Canonical()); err != nil {
		return err
	}
	age := now.Sub(time.UnixMilli(p.Created))
	if age < 0 {
		age = -age
	}
	if age > UpdateFreshness {
		return i18n.New("the request is too old")
	}
	return nil
}
