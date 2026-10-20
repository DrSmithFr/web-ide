package update

import (
	"crypto/ed25519"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/DrSmithFr/web-ide/lib/identity"
)

// pod makes a paired pod identity in a folder of its own, and the browser device of the same
// account (the one that signs the requests of the tests).
func pod(t *testing.T) (*Handler, *identity.Account, ed25519.PrivateKey) {
	t.Helper()
	a, err := identity.FromPhrase("lecture vapor excuse note pledge rough bundle start regular burden reveal theme banana pencil owner cube social job emotion member joy sting dash whisper")
	if err != nil {
		t.Fatal(err)
	}
	p, err := identity.Open(filepath.Join(t.TempDir(), "pod"))
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Pin(a.ID); err != nil {
		t.Fatal(err)
	}
	if err := p.SetCert(identity.NewCert(a, p.Key(), "this pod", identity.KindPod)); err != nil {
		t.Fatal(err)
	}
	browser := identity.KeyFromSeed(identity.Derive(a.Seed, "the browser"))
	h := New(p)
	// Not a unit, no systemd: the tests watch the command that would run, with an installer of
	// their own in a folder of the test.
	h.Unit = func() bool { return true }
	h.Start = func(cmd []string) error { return nil }
	h.Script = filepath.Join(t.TempDir(), "install.sh")
	os.WriteFile(h.Script, []byte("#!/bin/sh\n"), 0o755)
	return h, a, browser
}

// request is a signed request of the browser device, for the pod of the handler.
func request(t *testing.T, h *Handler, browser ed25519.PrivateKey, a *identity.Account, version string, created time.Time) *identity.UpdateRequest {
	t.Helper()
	u := &identity.UpdateRequest{V: 1, Pod: h.Own, Version: version, Created: created.UnixMilli(),
		Cert: identity.NewCert(a, browser, "phone", identity.KindBrowser)}
	u.Sig = identity.SignUpdate(browser, u)
	return u
}

// TestRunGood: a request the pod can trust starts the installer of the release, in a unit of its
// own, and the answer says the update is running.
func TestRunGood(t *testing.T) {
	h, a, browser := pod(t)
	var got []string
	h.Start = func(cmd []string) error { got = cmd; return nil }
	ans := h.Run(request(t, h, browser, a, "v1.11.0", time.Now()))
	if ans.Status != StatusRunning {
		t.Fatalf("a good request runs: %+v", ans)
	}
	if len(got) < 3 || got[0] != "systemd-run" || !strings.HasPrefix(got[2], "--unit=web-ide-update-") {
		t.Fatalf("the install runs detached, in a unit of its own: %v", got)
	}
	if got[len(got)-1] != "v1.11.0" || !strings.HasSuffix(got[len(got)-2], "install.sh") {
		t.Fatalf("the installed script installs the tag asked for: %v", got)
	}
}

// TestRunRefused: the checks of the certificate, of the freshness, of the replay and of the tag.
// Each case gets a pod of its own, so one case never changes what the next one checks.
func TestRunRefused(t *testing.T) {
	cases := []struct {
		what string
		pick func(t *testing.T, h *Handler, a *identity.Account, browser ed25519.PrivateKey) *identity.UpdateRequest
	}{
		{"unsigned", func(t *testing.T, h *Handler, a *identity.Account, browser ed25519.PrivateKey) *identity.UpdateRequest {
			u := request(t, h, browser, a, "v1.11.0", time.Now())
			u.Sig = ""
			return u
		}},
		{"stale", func(t *testing.T, h *Handler, a *identity.Account, browser ed25519.PrivateKey) *identity.UpdateRequest {
			return request(t, h, browser, a, "v1.11.0", time.Now().Add(-identity.UpdateFreshness-time.Minute))
		}},
		{"revoked", func(t *testing.T, h *Handler, a *identity.Account, browser ed25519.PrivateKey) *identity.UpdateRequest {
			u := request(t, h, browser, a, "v1.11.0", time.Now())
			list := &identity.Revocations{Account: a.ID, Seq: 1, Epoch: 1, Revoked: []string{u.Cert.Key}, Created: time.Now().Unix()}
			if err := identity.SignRevocations(a.Priv, list); err != nil {
				t.Fatal(err)
			}
			if _, err := h.Identity.Apply(list); err != nil {
				t.Fatal(err)
			}
			return u
		}},
		{"foreign", func(t *testing.T, h *Handler, a *identity.Account, browser ed25519.PrivateKey) *identity.UpdateRequest {
			other, err := identity.FromPhrase("abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon art")
			if err != nil {
				t.Fatal(err)
			}
			key := identity.KeyFromSeed(identity.Derive(other.Seed, "a browser of another account"))
			u := request(t, h, browser, a, "v1.11.0", time.Now())
			u.Cert = identity.NewCert(other, key, "intruder", identity.KindBrowser)
			u.Sig = identity.SignUpdate(key, u)
			return u
		}},
		{"not a tag", func(t *testing.T, h *Handler, a *identity.Account, browser ed25519.PrivateKey) *identity.UpdateRequest {
			return request(t, h, browser, a, "v1.11.0-12-gabc1234", time.Now())
		}},
		{"another pod", func(t *testing.T, h *Handler, a *identity.Account, browser ed25519.PrivateKey) *identity.UpdateRequest {
			u := request(t, h, browser, a, "v1.11.0", time.Now())
			u.Pod = "someone else"
			u.Sig = identity.SignUpdate(browser, u)
			return u
		}},
	}
	for _, c := range cases {
		h, a, browser := pod(t)
		ran := false
		h.Start = func(cmd []string) error { ran = true; return nil }
		ans := h.Run(c.pick(t, h, a, browser))
		if ans.Status != StatusRefused {
			t.Errorf("%s: refused, got %+v", c.what, ans)
		}
		if ans.Message == "" {
			t.Errorf("%s: the reason reaches the page: %+v", c.what, ans)
		}
		if ran {
			t.Errorf("%s: the installer must not run", c.what)
		}
	}

	// A replay of the very same request: the signature was already run.
	h, a, browser := pod(t)
	ran := false
	h.Start = func(cmd []string) error { ran = true; return nil }
	u := request(t, h, browser, a, "v1.11.0", time.Now())
	if ans := h.Run(u); ans.Status != StatusRunning {
		t.Fatalf("the first request runs: %+v", ans)
	}
	ran = false
	if ans := h.Run(u); ans.Status != StatusRefused {
		t.Errorf("a request cannot be run twice: %+v", ans)
	}
	if ran {
		t.Fatal("the replay ran the installer")
	}
}

// TestRunNotService: a pod that does not run as its unit answers the command the user runs by
// hand, and a unit whose installer is missing does the same.
func TestRunNotService(t *testing.T) {
	h, a, browser := pod(t)
	ran := false
	h.Start = func(cmd []string) error { ran = true; return nil }

	h.Unit = func() bool { return false }
	ans := h.Run(request(t, h, browser, a, "v1.11.0", time.Now()))
	if ans.Status != StatusNotService || ans.Command != "scripts/install.sh v1.11.0" {
		t.Fatalf("a pod that is not a unit gives the command: %+v", ans)
	}

	h.Unit = func() bool { return true }
	h.Script = filepath.Join(t.TempDir(), "no-such-install.sh")
	if ans := h.Run(request(t, h, browser, a, "v1.12.0", time.Now())); ans.Status != StatusNotService ||
		!strings.Contains(ans.Message, "installer") || ans.Command != "scripts/install.sh v1.12.0" {
		t.Fatalf("a unit with no installed script gives the command too: %+v", ans)
	}
	if ran {
		t.Fatal("nothing ran")
	}
}

// TestRunHook: WEBIDE_UPDATE_CMD replaces the install command — the hook of the browser tests,
// which restarts a pod that is not a unit.
func TestRunHook(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "updated")
	t.Setenv(UpdateCmdEnv, "touch "+marker)
	h, a, browser := pod(t)
	h.Unit = func() bool { return false } // the hook answers for the unit, wherever the pod runs
	h.Start = Detached
	ans := h.Run(request(t, h, browser, a, "v1.11.0", time.Now()))
	if ans.Status != StatusRunning {
		t.Fatalf("the hook runs: %+v", ans)
	}
	for i := 0; i < 100 && !fileExists(marker); i++ {
		time.Sleep(20 * time.Millisecond)
	}
	if !fileExists(marker) {
		t.Fatal("the hook did not run")
	}
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// TestAsUnit: only the systemd user unit of the pod can restart itself.
func TestAsUnit(t *testing.T) {
	t.Setenv("INVOCATION_ID", "")
	if AsUnit() {
		t.Fatal("without INVOCATION_ID, this is not a unit")
	}
	t.Setenv("INVOCATION_ID", "1234567890abcdef")
	if AsUnit() {
		t.Fatal("a process of another unit is not the pod unit")
	}
}
