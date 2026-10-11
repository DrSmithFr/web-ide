package identity

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/DrSmithFr/web-ide/pod/internal/i18n"
)

// PodIdentity is the identity of a pod in its data folder: its own device key (device.key,
// 0600), the certificate the account signed for it, the pinned account id and the last
// revocation list it received. Neither the phrase nor the account private key is ever here:
// a paired pod cannot sign a certificate, it only checks the ones it is shown.
//
// A running pod shares it between the relay link and the LAN links, and the pair command may
// rewrite its files meanwhile: the methods lock, and Refresh reads the files again when they
// changed. The fields are for the pair command, which owns the identity alone.
type PodIdentity struct {
	mu   sync.Mutex
	dir  string
	Seed []byte // the device seed, read from device.key
	priv ed25519.PrivateKey
	// mod is the modification time of the files last read (Refresh).
	mod time.Time

	Cert        *Cert        `json:"cert"`
	Account     string       `json:"account"` // the pinned account id
	Revocations *Revocations `json:"revocations,omitempty"`
}

// Open reads the identity of the pod, or creates a new device key on first run. Without
// pairing the pod has a key but no account: it stays unpaired and runs in token mode.
func Open(dir string) (*PodIdentity, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	p := &PodIdentity{dir: dir}
	if err := p.readKey(); err != nil {
		return nil, err
	}
	if err := p.load(); err != nil {
		return nil, err
	}
	p.mod = p.modTime()
	return p, nil
}

// readKey reads the device seed, or makes one on first run.
func (p *PodIdentity) readKey() error {
	data, err := os.ReadFile(filepath.Join(p.dir, "device.key"))
	switch {
	case err == nil:
		s, err := hex.DecodeString(strings.TrimSpace(string(data)))
		if err != nil || len(s) != 32 {
			return i18n.Errorf("%s is not a device seed", filepath.Join(p.dir, "device.key"))
		}
		p.Seed = s
	case os.IsNotExist(err):
		buf := make([]byte, 32)
		if _, err := rand.Read(buf); err != nil {
			return err
		}
		p.Seed = buf
		if err := os.WriteFile(filepath.Join(p.dir, "device.key"), []byte(hex.EncodeToString(buf)+"\n"), 0o600); err != nil {
			return err
		}
	default:
		return err
	}
	p.priv = KeyFromSeed(p.Seed)
	return nil
}

// modTime is the latest modification time of device.key and identity.json.
func (p *PodIdentity) modTime() time.Time {
	var t time.Time
	for _, name := range []string{"device.key", "identity.json"} {
		if st, err := os.Stat(filepath.Join(p.dir, name)); err == nil && st.ModTime().After(t) {
			t = st.ModTime()
		}
	}
	return t
}

// Refresh reads the files again when the pair command changed them while the pod runs (a
// pod paired, restored or given a revocation list from the command line). It reports
// whether anything was read.
func (p *PodIdentity) Refresh() (bool, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	mod := p.modTime()
	if !mod.After(p.mod) {
		return false, nil
	}
	if err := p.readKey(); err != nil {
		return false, err
	}
	if err := p.load(); err != nil {
		return false, err
	}
	p.mod = mod
	return true, nil
}

// Key returns the device private key of the pod.
func (p *PodIdentity) Key() ed25519.PrivateKey {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.priv
}

// ID is the device id of the pod (the base64url of its public key).
func (p *PodIdentity) ID() string { return KeyID(p.Key()) }

// Paired reports whether the pod has an account and its own certificate: a paired pod
// refuses the M1 token proof through the relay.
func (p *PodIdentity) Paired() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.Account != "" && p.Cert != nil
}

// State returns the pinned account, the certificate of the pod and the revocation list it
// holds, read together.
func (p *PodIdentity) State() (account string, cert *Cert, revoked *Revocations) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.Account, p.Cert, p.Revocations
}

// Pin stores the account id the pod is paired with: the key it checks every peer against.
func (p *PodIdentity) Pin(accountID string) error {
	p.Account = accountID
	return p.save()
}

// SetCert stores the certificate the account signed for this pod, after checking it.
func (p *PodIdentity) SetCert(c *Cert) error {
	if err := VerifyCert(c, p.Account, nil); err != nil {
		return err
	}
	p.Cert = c
	return p.save()
}

// ApplyRevocations takes a revocation list: only a signed list of the pinned account, with a
// higher seq than the one already stored, is kept.
func (p *PodIdentity) ApplyRevocations(r *Revocations) error {
	_, err := p.Apply(r)
	return err
}

// Apply is ApplyRevocations that also reports whether the list was newer than the one held:
// the LAN links pass a new list on to the other pods.
func (p *PodIdentity) Apply(r *Revocations) (bool, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := VerifyRevocations(r, p.Account); err != nil {
		return false, err
	}
	if p.Revocations != nil && r.Seq <= p.Revocations.Seq {
		return false, nil
	}
	p.Revocations = r
	if err := p.save(); err != nil {
		return false, err
	}
	p.mod = p.modTime()
	return true, nil
}

// PeerError checks the certificate of a peer against the pinned account and the revocation
// list. It is what the handshake answers when the peer is not a device of this account.
func (p *PodIdentity) PeerError(c *Cert) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	return VerifyCert(c, p.Account, p.Revocations)
}

func (p *PodIdentity) load() error {
	data, err := os.ReadFile(filepath.Join(p.dir, "identity.json"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	var v struct {
		Cert        *Cert        `json:"cert"`
		Account     string       `json:"account"`
		Revocations *Revocations `json:"revocations"`
	}
	if err := json.Unmarshal(data, &v); err != nil {
		return err
	}
	p.Cert, p.Account, p.Revocations = v.Cert, v.Account, v.Revocations
	return nil
}

func (p *PodIdentity) save() error {
	data, err := json.MarshalIndent(map[string]any{
		"cert":        p.Cert,
		"account":     p.Account,
		"revocations": p.Revocations,
	}, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(p.dir, "identity.json"), data, 0o600)
}
