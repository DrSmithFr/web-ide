package identity

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/DrSmithFr/web-ide/pod/internal/i18n"
)

// PodIdentity is the identity of a pod in its data folder: its own device key (device.key,
// 0600), the certificate the account signed for it, the pinned account id and the last
// revocation list it received. Neither the phrase nor the account private key is ever here:
// a paired pod cannot sign a certificate, it only checks the ones it is shown.
type PodIdentity struct {
	dir  string
	Seed []byte // the device seed, read from device.key
	priv ed25519.PrivateKey

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
	data, err := os.ReadFile(filepath.Join(dir, "device.key"))
	switch {
	case err == nil:
		s, err := hex.DecodeString(strings.TrimSpace(string(data)))
		if err != nil || len(s) != 32 {
			return nil, i18n.Errorf("%s is not a device seed", filepath.Join(dir, "device.key"))
		}
		p.Seed = s
	case os.IsNotExist(err):
		buf := make([]byte, 32)
		if _, err := rand.Read(buf); err != nil {
			return nil, err
		}
		p.Seed = buf
		if err := os.WriteFile(filepath.Join(dir, "device.key"), []byte(hex.EncodeToString(buf)+"\n"), 0o600); err != nil {
			return nil, err
		}
	default:
		return nil, err
	}
	p.priv = KeyFromSeed(p.Seed)

	if err := p.load(); err != nil {
		return nil, err
	}
	return p, nil
}

// Key returns the device private key of the pod.
func (p *PodIdentity) Key() ed25519.PrivateKey { return p.priv }

// ID is the device id of the pod (the base64url of its public key).
func (p *PodIdentity) ID() string { return KeyID(p.priv) }

// Paired reports whether the pod has an account and its own certificate: a paired pod
// refuses the M1 token proof through the relay.
func (p *PodIdentity) Paired() bool { return p.Account != "" && p.Cert != nil }

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
	if err := VerifyRevocations(r, p.Account); err != nil {
		return err
	}
	if p.Revocations != nil && r.Seq <= p.Revocations.Seq {
		return nil
	}
	p.Revocations = r
	return p.save()
}

// PeerError checks the certificate of a peer against the pinned account and the revocation
// list. It is what the handshake answers when the peer is not a device of this account.
func (p *PodIdentity) PeerError(c *Cert) error {
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
