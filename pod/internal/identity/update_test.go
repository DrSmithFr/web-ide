package identity

import (
	"crypto/ed25519"
	"testing"
	"time"
)

// updateVector is the request of the shared vectors, signed by the device key of the vectors.
func updateVector(t *testing.T) (*UpdateRequest, ed25519.PrivateKey, *Account) {
	t.Helper()
	v := loadVectors(t)
	a, err := FromPhrase(v.Phrase)
	if err != nil {
		t.Fatal(err)
	}
	dev := KeyFromSeed(Derive(a.Seed, InfoDevice))
	u := &UpdateRequest{V: 1, Pod: v.DeviceID, Version: "v1.11.0", Created: time.Now().UnixMilli()}
	u.Cert = NewCert(a, dev, "test browser", KindBrowser)
	u.Sig = SignUpdate(dev, u)
	return u, dev, a
}

// TestUpdateVectors: the bytes a browser signs are the bytes the pod checks — the same canonical
// form and the same signature as scripts/identity-vectors.mjs reads.
func TestUpdateVectors(t *testing.T) {
	v := loadVectors(t)
	u := &UpdateRequest{V: 1, Pod: v.DeviceID, Version: "v1.11.0", Created: 1766721600000}
	if got := u.Canonical(); got != v.UpdateCanonical {
		t.Fatalf("canonical:\n got  %s\n want %s", got, v.UpdateCanonical)
	}
	dev := KeyFromSeed(Derive(mustAccount(t, v).Seed, InfoDevice))
	if got := SignUpdate(dev, u); got != v.UpdateSig {
		t.Fatalf("signature:\n got  %s\n want %s", got, v.UpdateSig)
	}
}

func mustAccount(t *testing.T, v *vectors) *Account {
	t.Helper()
	a, err := FromPhrase(v.Phrase)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

// TestIsReleaseTag: only a release tag may be installed; a dev build or a git describe with a hash
// is never offered to another pod.
func TestIsReleaseTag(t *testing.T) {
	for _, v := range []string{"v1.2.3", "v0.1.0", "v10.20.30"} {
		if !IsReleaseTag(v) {
			t.Errorf("%q is a release tag", v)
		}
	}
	for _, v := range []string{"dev", "1.10.0", "v1.10.0-12-gabc1234", "v1.10", "v1.10.0-dirty", "v1.10.0+local"} {
		if IsReleaseTag(v) {
			t.Errorf("%q is not a release tag", v)
		}
	}
}

// TestCheckUpdate: the pod named by a request only runs it for a device of the account it pinned,
// that it does not find revoked, with a signature that matches and a request that is fresh.
func TestCheckUpdate(t *testing.T) {
	now := time.Now()
	good, dev, a := updateVector(t)
	if err := CheckUpdate(good, a.ID, nil, now); err != nil {
		t.Fatalf("a request signed by a device of the account: %v", err)
	}

	t.Run("unsigned", func(t *testing.T) {
		u := *good
		u.Sig = ""
		if err := CheckUpdate(&u, a.ID, nil, now); err == nil {
			t.Fatal("a request without a signature is refused")
		}
	})
	t.Run("foreign signature", func(t *testing.T) {
		u := *good
		other := KeyFromSeed(Derive(a.Seed, "not the device"))
		u.Sig = SignUpdate(other, &u)
		if err := CheckUpdate(&u, a.ID, nil, now); err == nil {
			t.Fatal("a signature made by another key is refused")
		}
		_ = dev
	})
	t.Run("stale", func(t *testing.T) {
		u := *good
		u.Created = now.Add(-UpdateFreshness - time.Minute).UnixMilli()
		u.Sig = SignUpdate(dev, &u)
		if err := CheckUpdate(&u, a.ID, nil, now); err == nil {
			t.Fatal("a request older than the freshness window is refused")
		}
	})
	t.Run("replayed later", func(t *testing.T) {
		u := *good
		if err := CheckUpdate(&u, a.ID, nil, now.Add(UpdateFreshness+time.Minute)); err == nil {
			t.Fatal("a request cannot be run once its window passed")
		}
	})
	t.Run("revoked device", func(t *testing.T) {
		list := &Revocations{Account: a.ID, Seq: 1, Epoch: 1, Revoked: []string{good.Cert.Key}, Created: now.Unix()}
		list.Sig = Sign(a.Priv, []byte(list.Canonical()))
		if err := CheckUpdate(good, a.ID, list, now); err == nil {
			t.Fatal("a revoked device cannot ask for an update")
		}
	})
	t.Run("another account", func(t *testing.T) {
		other, err := FromPhrase("abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon art")
		if err != nil {
			t.Fatal(err)
		}
		if err := CheckUpdate(good, other.ID, nil, now); err == nil {
			t.Fatal("a request from a device of another account is refused")
		}
	})
	t.Run("another version of the format", func(t *testing.T) {
		u := *good
		u.V = 2
		if err := CheckUpdate(&u, a.ID, nil, now); err == nil {
			t.Fatal("a request of another format is refused")
		}
	})
	t.Run("swapped certificate", func(t *testing.T) {
		// The certificate is not in the bytes the signature covers: another one, even a valid one
		// of the same account, breaks the signature.
		u := *good
		u.Cert = NewCert(a, KeyFromSeed(Derive(a.Seed, "another device")), "other", KindBrowser)
		if err := CheckUpdate(&u, a.ID, nil, now); err == nil {
			t.Fatal("a swapped certificate must not verify")
		}
	})
}
