package identity

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/rand"
	"fmt"

	"github.com/DrSmithFr/web-ide/pod/internal/i18n"
)

// The sync key of an account comes from the sync seed: the key of an epoch is HKDF over the
// seed with "web-ide sync epoch <e>", so a device that knows the phrase can always rebuild it
// and a rotation needs no random key to lose. A pod paired by an admin (no phrase) gets the
// same key wrapped for it: each device makes an X25519 key, publishes it signed by its device
// key, and an admin wraps the epoch key for it.
//
// The sub-keys per kind come from the epoch key. Every blob is AES-GCM with a random nonce per
// write and the associated data account|kind|epoch|seq: a blob cannot be moved to another
// account, kind, epoch or seq. The relay stores the ciphertext, it never sees a value.
const (
	InfoX25519 = "web-ide x25519 v1"
	InfoWrap   = "web-ide sync wrap v1"
)

// Kinds of blob the relay stores, one per account and kind. The project list of a pod lives under
// its own kind, "projects:<device key>": one writer, and a pod reads the whole family with the
// prefix KindProjects.
const (
	KindSettings = "settings"
	KindVault    = "vault"
	KindProjects = "projects"
)

// ProjectsPrefix is the start of the kind of every project list: a blobGet of the prefix returns
// the lists of all the pods of the account.
const ProjectsPrefix = KindProjects + ":"

// ProjectsKind is the kind of the project list published by one device.
func ProjectsKind(device string) string { return ProjectsPrefix + device }

// EpochInfo is the info string of the key of an epoch.
func EpochInfo(epoch int64) string { return fmt.Sprintf("web-ide sync epoch %d", epoch) }

// EpochKey is the sync key of an epoch, derived from the sync seed.
func EpochKey(syncSeed []byte, epoch int64) []byte { return Derive(syncSeed, EpochInfo(epoch)) }

// KindKey is the sub-key of a kind under an epoch key.
func KindKey(epochKey []byte, kind string) []byte { return Derive(epochKey, kind) }

// X25519FromSeed turns a 32-byte seed into an X25519 key pair. A pod derives its key from its
// device seed, so it rebuilds it after a restart without a new file on disk. The browser cannot
// import an X25519 private key from a raw seed, so it keeps its own key as a JWK.
func X25519FromSeed(seed []byte) (*ecdh.PrivateKey, error) { return ecdh.X25519().NewPrivateKey(seed) }

// X25519ID is the base64url of an X25519 public key.
func X25519ID(pub *ecdh.PublicKey) string { return B64u(pub.Bytes()) }

// Shared is the X25519 secret with a peer public key given as its base64url.
func Shared(priv *ecdh.PrivateKey, pub string) ([]byte, error) {
	b, err := B64d(pub)
	if err != nil || len(b) != 32 {
		return nil, i18n.New("not an X25519 key")
	}
	p, err := ecdh.X25519().NewPublicKey(b)
	if err != nil {
		return nil, i18n.New("not an X25519 key")
	}
	return priv.ECDH(p)
}

// WrapKey is the AES key that wraps a sync key for a device.
func WrapKey(shared []byte) []byte { return Derive(shared, InfoWrap) }

// Seal encrypts with AES-GCM and returns the nonce and the ciphertext as base64url. A random
// nonce per write: the same value written twice gives two different blobs.
func Seal(key []byte, aad string, plain []byte) (string, string, error) {
	n := make([]byte, 12)
	if _, err := rand.Read(n); err != nil {
		return "", "", err
	}
	return SealWith(key, n, aad, plain)
}

// SealWith encrypts with a given nonce (the shared test vectors use fixed ones).
func SealWith(key []byte, nonce []byte, aad string, plain []byte) (string, string, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", "", err
	}
	return B64u(nonce), B64u(gcm.Seal(nil, nonce, plain, []byte(aad))), nil
}

// Unseal decrypts a blob. The associated data must be the one the write used.
func Unseal(key []byte, aad string, nonce, data string) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	n, err := B64d(nonce)
	if err != nil {
		return nil, err
	}
	ct, err := B64d(data)
	if err != nil {
		return nil, err
	}
	return gcm.Open(nil, n, ct, []byte(aad))
}

// BlobAAD is the associated data of a blob write: account|kind|epoch|seq.
func BlobAAD(account, kind string, epoch, seq int64) string {
	return fmt.Sprintf("%s|%s|%d|%d", account, kind, epoch, seq)
}

// WrapAAD is the associated data of a wrap: account|device|epoch.
func WrapAAD(account, device string, epoch int64) string {
	return fmt.Sprintf("%s|%s|%d", account, device, epoch)
}

// VerifySig checks a signature over a canonical form. The relay has no private key: it can only
// check that a record was signed by the key it names, which is what makes a wrap, a blob write or
// a published X25519 key trustworthy.
func VerifySig(id, sig, canonical string) error {
	if sig == "" {
		return i18n.New("no signature")
	}
	if !Verify(id, sig, []byte(canonical)) {
		return i18n.New("the signature does not match")
	}
	return nil
}

// SyncKeyRecord is the X25519 public key a device published, signed by its device key: the
// guard against a relay that swaps it, the same role the certificate plays for a pairing.
type SyncKeyRecord struct {
	Account string `json:"account"`
	Device  string `json:"device"`
	Key     string `json:"key"`
	Sig     string `json:"sig"`
}

func (s *SyncKeyRecord) Canonical() string {
	return fmt.Sprintf(`{"account":%s,"device":%s,"key":%s}`, jstr(s.Account), jstr(s.Device), jstr(s.Key))
}

// Ops of a blob write.
const (
	OpPut = "put"
	OpDel = "del"
)

// BlobRecord is one write (or deletion) of a blob: the bytes the device key signs. The device is
// part of the canonical form, so a signature cannot be attributed to another device; the op is
// part of it too, so a write cannot be replayed as a deletion.
type BlobRecord struct {
	Account string `json:"account"`
	Kind    string `json:"kind"`
	Epoch   int64  `json:"epoch"`
	Seq     int64  `json:"seq"`
	Device  string `json:"device"`
	Op      string `json:"op"`
	Nonce   string `json:"nonce"`
	Data    string `json:"data"`
	Sig     string `json:"sig"`
}

func (b *BlobRecord) Canonical() string {
	return fmt.Sprintf(`{"account":%s,"kind":%s,"epoch":%d,"seq":%d,"device":%s,"op":%s,"nonce":%s,"data":%s}`,
		jstr(b.Account), jstr(b.Kind), b.Epoch, b.Seq, jstr(b.Device), jstr(b.Op), jstr(b.Nonce), jstr(b.Data))
}

// WrapRecord is the key of an epoch wrapped for a device.
type WrapRecord struct {
	Account string `json:"account"`
	Device  string `json:"device"`
	Epoch   int64  `json:"epoch"`
	Key     string `json:"key"`
	Nonce   string `json:"nonce"`
	Data    string `json:"data"`
	Sig     string `json:"sig"`
}

func (w *WrapRecord) Canonical() string {
	return fmt.Sprintf(`{"account":%s,"device":%s,"epoch":%d,"key":%s,"nonce":%s,"data":%s}`,
		jstr(w.Account), jstr(w.Device), w.Epoch, jstr(w.Key), jstr(w.Nonce), jstr(w.Data))
}
