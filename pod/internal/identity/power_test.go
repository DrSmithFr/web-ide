package identity

import (
	"testing"
	"time"
)

// TestPowerVectors: the bytes a browser signs are the bytes the pod checks
// (scripts/identity-vectors.mjs reads the same ones).
func TestPowerVectors(t *testing.T) {
	v := loadVectors(t)
	p := &PowerRequest{V: 1, Pod: v.DeviceID, Action: PowerShutdown, Created: 1766721600000}
	if got := p.Canonical(); got != v.PowerCanonical {
		t.Fatalf("canonical:\n got  %s\n want %s", got, v.PowerCanonical)
	}
	dev := KeyFromSeed(Derive(mustAccount(t, v).Seed, InfoDevice))
	if got := SignPower(dev, p); got != v.PowerSig {
		t.Fatalf("signature:\n got  %s\n want %s", got, v.PowerSig)
	}
}

// TestCheckPower: the checks of an update request, and an action the pod knows. The action is in
// the bytes signed: a reboot cannot be turned into a shutdown on the way.
func TestCheckPower(t *testing.T) {
	now := time.Now()
	v := loadVectors(t)
	a := mustAccount(t, v)
	dev := KeyFromSeed(Derive(a.Seed, InfoDevice))
	good := &PowerRequest{V: 1, Pod: v.DeviceID, Action: PowerReboot, Created: now.UnixMilli(), Cert: NewCert(a, dev, "test browser", KindBrowser)}
	good.Sig = SignPower(dev, good)
	if err := CheckPower(good, a.ID, nil, now); err != nil {
		t.Fatalf("a request signed by a device of the account: %v", err)
	}
	for _, c := range []struct {
		what string
		edit func(p *PowerRequest)
		at   time.Time
	}{
		{"unsigned", func(p *PowerRequest) { p.Sig = "" }, now},
		{"action swapped", func(p *PowerRequest) { p.Action = PowerShutdown }, now},
		{"pod swapped", func(p *PowerRequest) { p.Pod = "another" }, now},
		{"unknown action", func(p *PowerRequest) { p.Action = "explode"; p.Sig = SignPower(dev, p) }, now},
		{"stale", func(p *PowerRequest) {}, now.Add(UpdateFreshness + time.Minute)},
		{"another format", func(p *PowerRequest) { p.V = 2; p.Sig = SignPower(dev, p) }, now},
		{"no certificate", func(p *PowerRequest) { p.Cert = nil }, now},
	} {
		p := *good
		c.edit(&p)
		if err := CheckPower(&p, a.ID, nil, c.at); err == nil {
			t.Errorf("%s: refused", c.what)
		}
	}
	list := &Revocations{Account: a.ID, Seq: 1, Epoch: 1, Revoked: []string{good.Cert.Key}, Created: now.Unix()}
	list.Sig = Sign(a.Priv, []byte(list.Canonical()))
	if err := CheckPower(good, a.ID, list, now); err == nil {
		t.Fatal("a revoked device cannot power a machine")
	}
	if err := CheckPower(nil, a.ID, nil, now); err == nil {
		t.Fatal("no request")
	}
}
