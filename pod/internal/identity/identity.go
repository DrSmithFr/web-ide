// Package identity holds the account of the relay: the recovery phrase, the keys derived
// from it, the device certificates signed by the account key and the signed revocation
// list. An account is a key: no e-mail, no password, no server that knows who is who.
//
// The phrase is shown once and never stored — on disk or in the relay: storing it would
// turn the theft of one machine into the theft of the account. From the phrase (HKDF) come
// the account key (Ed25519, its public half is the account id) and the sync key seed. Each
// device keeps only its own key; the pod stores its key in device.key (0600) and pins the
// account public key it got at pairing.
//
// The conventions shared with the browser (web/src and the bootstrap page):
//   - base64url without padding, for ids and signatures;
//   - HKDF-SHA256 with an explicit 32-byte zero salt, so WebCrypto derives the same bytes;
//   - canonical JSON with a fixed field order, no spaces, for what a signature covers.
package identity

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"strings"
)

// B64u encodes bytes as base64url without padding (the form of the ids and signatures).
func B64u(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

// B64d decodes a base64url string without padding.
func B64d(s string) ([]byte, error) { return base64.RawURLEncoding.DecodeString(s) }

// KeyFromSeed turns a 32-byte seed into an Ed25519 private key.
func KeyFromSeed(seed []byte) ed25519.PrivateKey { return ed25519.NewKeyFromSeed(seed) }

// ID is the public half of a key, as base64url: the account id, or the device id.
func ID(pub ed25519.PublicKey) string { return B64u(pub) }

// KeyID is the id of a private key (its public half, as base64url).
func KeyID(priv ed25519.PrivateKey) string { return ID(priv.Public().(ed25519.PublicKey)) }

// Sign signs the bytes with the private key and returns base64url.
func Sign(priv ed25519.PrivateKey, msg []byte) string { return B64u(ed25519.Sign(priv, msg)) }

// Verify checks a base64url signature against a public key given as its base64url id.
func Verify(id, sig string, msg []byte) bool {
	pub, err := B64d(id)
	if err != nil || len(pub) != ed25519.PublicKeySize {
		return false
	}
	b, err := B64d(sig)
	if err != nil || len(b) != ed25519.SignatureSize {
		return false
	}
	return ed25519.Verify(pub, msg, b)
}

// RelayBinding is the bytes a device signs to prove it holds the private half of the key its
// certificate names: the nonce the relay sent, tied to the origin the link was opened at, so a
// proof made for one relay cannot be replayed on another. A copied certificate answers nothing.
func RelayBinding(origin, nonce string) string { return "relay|" + origin + "|" + nonce }

// Fingerprint is the 4-word reading of a key id (44 bits of its SHA-256, 11 bits per
// word): what the user compares on both sides of a pairing, the guard against a relay that
// swaps a key.
func Fingerprint(id string) string {
	b, err := B64d(id)
	if err != nil {
		return ""
	}
	h := sha256.Sum256(b)
	v := uint64(h[0])<<40 | uint64(h[1])<<32 | uint64(h[2])<<24 | uint64(h[3])<<16 | uint64(h[4])<<8 | uint64(h[5])
	v >>= 4 // 44 bits, four groups of 11
	var words []string
	for i := 3; i >= 0; i-- {
		words = append(words, Word(int(v>>uint(11*i))&2047))
	}
	return strings.Join(words, " ")
}

// jstr quotes a string the way JSON does, so the canonical form is the same in Go and in
// the browser: only the quote, the backslash and the control characters are escaped.
func jstr(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch {
		case r == '"':
			b.WriteString(`\"`)
		case r == '\\':
			b.WriteString(`\\`)
		case r < 0x20:
			b.WriteString(fmt.Sprintf(`\u%04x`, r))
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}
