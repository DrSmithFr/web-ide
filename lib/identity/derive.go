package identity

import (
	"crypto/ed25519"
	"crypto/sha256"
	"io"

	"golang.org/x/crypto/hkdf"
	"golang.org/x/crypto/pbkdf2"
)

// Info strings of the derivation. They are part of the format: the browser derives the same
// keys with the same strings, so they cannot change without a version bump.
const (
	InfoAccount = "web-ide account v1"
	InfoSync    = "web-ide sync v1"
	InfoDevice  = "web-ide device v1"
)

// Seed is the BIP39 seed of a phrase: PBKDF2-SHA256, salt "mnemonic", 2048 rounds, 64 bytes.
func Seed(phrase string) []byte {
	return pbkdf2.Key([]byte(phrase), []byte("mnemonic"), 2048, 64, sha256.New)
}

// Derive is HKDF-SHA256 with an explicit 32-byte zero salt. WebCrypto derives the same bytes
// with an empty salt (zeros of the hash size), so the pod and the browser agree.
func Derive(seed []byte, info string) []byte {
	hk := hkdf.New(sha256.New, seed, make([]byte, sha256.Size), []byte(info))
	out := make([]byte, 32)
	if _, err := io.ReadFull(hk, out); err != nil {
		return nil
	}
	return out
}

// Account is the key pair derived from the phrase. The phrase and the private key live only
// in memory: nothing on disk or in the relay holds them in clear.
type Account struct {
	Seed []byte // the BIP39 seed, 64 bytes
	Priv ed25519.PrivateKey
	ID   string // the public half, as base64url: the account id
}

// FromPhrase validates the phrase and derives the account key.
func FromPhrase(phrase string) (*Account, error) {
	if err := Validate(phrase); err != nil {
		return nil, err
	}
	s := Seed(phrase)
	priv := KeyFromSeed(Derive(s, InfoAccount))
	return &Account{Seed: s, Priv: priv, ID: KeyID(priv)}, nil
}

// SyncSeed is the seed of the sync key of the account. Milestone 4 encrypts the project
// lists and the settings with it; the revocation list carries the epoch of that key.
func (a *Account) SyncSeed() []byte { return Derive(a.Seed, InfoSync) }

// DeviceSeed derives a device key from the phrase: the path of an admin browser, which
// holds the account key. A paired device keeps only its own key.
func DeviceSeed(seed []byte) []byte { return Derive(seed, InfoDevice) }
