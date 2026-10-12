package main

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"

	"github.com/DrSmithFr/web-ide/pod/internal/identity"
)

type vectors struct {
	Phrase              string `json:"phrase"`
	Seed                string `json:"seed"`
	AccountSeed         string `json:"accountSeed"`
	SyncSeed            string `json:"syncSeed"`
	AccountID           string `json:"accountId"`
	AccountFingerprint  string `json:"accountFingerprint"`
	DeviceSeed          string `json:"deviceSeed"`
	DeviceID            string `json:"deviceId"`
	DeviceFingerprint   string `json:"deviceFingerprint"`
	Message             string `json:"message"`
	MessageSig          string `json:"messageSig"`
	DeviceMessageSig    string `json:"deviceMessageSig"`
	CertCanonical       string `json:"certCanonical"`
	CertSig             string `json:"certSig"`
	RevocationCanonical string `json:"revocationCanonical"`
	RevocationSig       string `json:"revocationSig"`

	// Sync (milestone 4): the key of an epoch, its sub-keys, the X25519 pair of a device and
	// the wrap of the epoch key for it. The nonces are fixed (twelve zeros) so the browser can
	// check the same ciphertext.
	EpochKey1        string `json:"epochKey1"`
	SettingsKey      string `json:"settingsKey"`
	VaultKey         string `json:"vaultKey"`
	PodXSeed         string `json:"podXSeed"`
	PodXPub          string `json:"podXPub"`
	AdminXSeed       string `json:"adminXSeed"`
	AdminXPub        string `json:"adminXPub"`
	Shared           string `json:"shared"`
	WrapKey          string `json:"wrapKey"`
	WrapAAD          string `json:"wrapAAD"`
	WrapNonce        string `json:"wrapNonce"`
	WrapData         string `json:"wrapData"`
	BlobAAD          string `json:"blobAAD"`
	BlobNonce        string `json:"blobNonce"`
	BlobData         string `json:"blobData"`
	SyncKeyCanonical string `json:"syncKeyCanonical"`
	SyncKeySig       string `json:"syncKeySig"`
	BlobCanonical    string `json:"blobCanonical"`
	BlobSig          string `json:"blobSig"`
	WrapCanonical    string `json:"wrapCanonical"`
	WrapSig          string `json:"wrapSig"`
}

func main() {
	path := os.Args[1]
	data, err := os.ReadFile(path)
	if err != nil {
		panic(err)
	}
	var v vectors
	if err := json.Unmarshal(data, &v); err != nil {
		panic(err)
	}

	a, err := identity.FromPhrase(v.Phrase)
	if err != nil {
		panic(err)
	}
	seed, err := hex.DecodeString(v.Seed)
	if err != nil {
		panic(err)
	}
	syncSeed, err := hex.DecodeString(v.SyncSeed)
	if err != nil {
		panic(err)
	}
	ekey := identity.EpochKey(syncSeed, 1)
	v.EpochKey1 = hex.EncodeToString(ekey)
	v.SettingsKey = hex.EncodeToString(identity.KindKey(ekey, identity.KindSettings))
	v.VaultKey = hex.EncodeToString(identity.KindKey(ekey, identity.KindVault))

	// The pod's X25519 key, derived from its device seed.
	xseed := identity.Derive(identity.DeviceSeed(seed), identity.InfoX25519)
	p, err := identity.X25519FromSeed(xseed)
	if err != nil {
		panic(err)
	}
	v.PodXSeed = hex.EncodeToString(xseed)
	v.PodXPub = identity.X25519ID(p.PublicKey())

	// An admin key with a fixed seed, so the browser can rebuild the same wrap.
	aseed := make([]byte, 32)
	for i := range aseed {
		aseed[i] = 11
	}
	ak, err := identity.X25519FromSeed(aseed)
	if err != nil {
		panic(err)
	}
	v.AdminXSeed = hex.EncodeToString(aseed)
	v.AdminXPub = identity.X25519ID(ak.PublicKey())
	shared, err := identity.Shared(ak, v.PodXPub)
	if err != nil {
		panic(err)
	}
	v.Shared = hex.EncodeToString(shared)
	wk := identity.WrapKey(shared)
	v.WrapKey = hex.EncodeToString(wk)

	zero := make([]byte, 12)
	v.WrapNonce = identity.B64u(zero)
	v.WrapAAD = identity.WrapAAD(a.ID, v.DeviceID, 1)
	_, v.WrapData, err = identity.SealWith(wk, zero, v.WrapAAD, ekey)
	if err != nil {
		panic(err)
	}

	bkey := identity.KindKey(ekey, identity.KindSettings)
	v.BlobAAD = identity.BlobAAD(a.ID, identity.KindSettings, 1, 1)
	_, v.BlobData, err = identity.SealWith(bkey, zero, v.BlobAAD, []byte(`{"theme":"nuit"}`))
	if err != nil {
		panic(err)
	}
	v.BlobNonce = identity.B64u(zero)

	podkey := identity.KeyFromSeed(identity.DeviceSeed(seed))
	rec := identity.SyncKeyRecord{Account: a.ID, Device: v.DeviceID, Key: v.PodXPub}
	v.SyncKeyCanonical = rec.Canonical()
	v.SyncKeySig = identity.Sign(podkey, []byte(rec.Canonical()))

	blob := identity.BlobRecord{Account: a.ID, Kind: identity.KindSettings, Epoch: 1, Seq: 1, Device: v.DeviceID, Op: identity.OpPut, Nonce: v.BlobNonce, Data: v.BlobData}
	v.BlobCanonical = blob.Canonical()
	v.BlobSig = identity.Sign(podkey, []byte(blob.Canonical()))

	wrap := identity.WrapRecord{Account: a.ID, Device: v.DeviceID, Epoch: 1, Key: v.AdminXPub, Nonce: v.WrapNonce, Data: v.WrapData}
	v.WrapCanonical = wrap.Canonical()
	v.WrapSig = identity.Sign(a.Priv, []byte(wrap.Canonical()))

	out, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		panic(err)
	}
	if err := os.WriteFile(path, append(out, '\n'), 0644); err != nil {
		panic(err)
	}
	fmt.Println("vectors written")
}
