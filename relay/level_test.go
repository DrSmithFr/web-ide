package relay

import (
	"context"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestVIPList: a row by account or by GitHub id, replaced when added again, ignored once ended
// but kept on the list, and removed.
func TestVIPList(t *testing.T) {
	st := newGitHubStore(t)
	now := time.Now()
	if err := st.AddVIP(VIP{Kind: vipAccount, ID: "acct-1", Note: "friend"}); err != nil {
		t.Fatal(err)
	}
	if err := st.AddVIP(VIP{Kind: vipGitHub, ID: "77", Login: "Drsmith", Until: now.Add(day).Unix()}); err != nil {
		t.Fatal(err)
	}
	if err := st.AddVIP(VIP{Kind: vipGitHub, ID: "88", Login: "gone", Until: now.Add(-day).Unix()}); err != nil {
		t.Fatal(err)
	}
	if err := st.AddVIP(VIP{Kind: "email", ID: "x"}); err == nil {
		t.Fatal("a VIP is a GitHub id or an account id")
	}
	if v := st.vipFor("acct-1", "", now); v == nil || v.Note != "friend" {
		t.Fatalf("by account id: %+v", v)
	}
	if v := st.vipFor("acct-2", "77", now); v == nil || v.Login != "Drsmith" {
		t.Fatalf("by the linked GitHub id: %+v", v)
	}
	if v := st.vipFor("acct-3", "88", now); v != nil {
		t.Fatalf("an ended row no longer counts: %+v", v)
	}
	if list, _ := st.VIPs(); len(list) != 3 {
		t.Fatalf("the ended row stays on the list: %+v", list)
	}
	if v := st.vipByLogin("drsmith"); v == nil || v.ID != "77" {
		t.Fatalf("a login is case-insensitive: %+v", v)
	}
	if err := st.AddVIP(VIP{Kind: vipAccount, ID: "acct-1", Note: "family", Until: now.Add(10 * day).Unix()}); err != nil {
		t.Fatal(err)
	}
	if v := st.vipFor("acct-1", "", now); v == nil || v.Note != "family" || v.Until == 0 {
		t.Fatalf("adding again replaces the note and the end: %+v", v)
	}
	if ok, err := st.RemoveVIP(vipAccount, "acct-1"); !ok || err != nil {
		t.Fatalf("remove: %v %v", ok, err)
	}
	if ok, _ := st.RemoveVIP(vipAccount, "acct-1"); ok {
		t.Fatal("a second remove finds nothing")
	}
	if v := st.vipFor("acct-1", "", now); v != nil {
		t.Fatal("removed")
	}
}

// TestAccountLevel: the best of the sponsorship and the VIP list, with its end and its sources;
// a key with nothing (a pod id) is free.
func TestAccountLevel(t *testing.T) {
	st := newGitHubStore(t)
	now := time.Now()
	for acct, id := range map[string]string{"acct-backer": "1", "acct-sup": "2", "acct-once": "3", "acct-both": "4"} {
		if err := st.LinkGitHub(acct, id, "login-"+id); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.SaveSponsors([]Sponsor{
		{ID: "1", Cents: 200},
		{ID: "2", Cents: 500},
		{ID: "3", Cents: 1000, OneTime: true, Since: now.Add(-80 * day).Unix()},
		{ID: "4", Cents: 500},
	}); err != nil {
		t.Fatal(err)
	}
	vipEnd := now.Add(5 * day).Unix()
	for _, v := range []VIP{
		{Kind: vipGitHub, ID: "4", Login: "login-4"},
		{Kind: vipAccount, ID: "acct-vip", Until: vipEnd},
	} {
		if err := st.AddVIP(v); err != nil {
			t.Fatal(err)
		}
	}
	for acct, want := range map[string]AccountLevel{
		"pod-alone":   {},
		"acct-backer": {Level: LevelBacker, Sponsor: LevelBacker},
		"acct-sup":    {Level: LevelSupporter, Sponsor: LevelSupporter},
		"acct-once": {Level: LevelVIP, Until: now.Add(10 * day).Unix(), Sponsor: LevelVIP,
			SponsorUntil: now.Add(10 * day).Unix()},
		"acct-both": {Level: LevelVIP, Sponsor: LevelSupporter, VIP: true},
		// No GitHub link at all: the VIP list by account id is enough.
		"acct-vip": {Level: LevelVIP, Until: vipEnd, VIP: true, VIPUntil: vipEnd},
	} {
		got := st.Level(acct, now)
		// The end of a one-time payment comes from its start: allow the second it was made in.
		if d := got.Until - want.Until; d == 1 || d == -1 {
			got.Until, got.SponsorUntil = want.Until, want.SponsorUntil
		}
		if got != want {
			t.Errorf("%s: %+v, want %+v", acct, got, want)
		}
	}
}

// TestTurnQuotaByLevel: free gets -quota, a Backer -quota-backer (twice -quota when unset),
// Supporter and VIP none; a relay with no -quota has none at all.
func TestTurnQuotaByLevel(t *testing.T) {
	st, _ := OpenStore(filepath.Join(t.TempDir(), "relay.db"))
	defer st.Close()
	month := usageMonth(time.Now())
	levels := map[string]Level{"acct-free": LevelFree, "acct-backer": LevelBacker, "acct-sup": LevelSupporter, "acct-vip": LevelVIP}
	for key := range levels {
		if err := st.AddUsage(key, month, 1500, 0); err != nil {
			t.Fatal(err)
		}
		if err := st.Register(key, "secret-"+key, ""); err != nil {
			t.Fatal(err)
		}
	}
	hook := func(key string) AccountLevel { return AccountLevel{Level: levels[key], Sponsor: levels[key]} }
	turn, _ := startTurn(t, st, TurnConfig{AdvertiseIP: "127.0.0.1", Quota: 1000, Level: hook})
	defer turn.Close()
	for key, want := range map[string]struct {
		limit   int64
		refused bool
	}{"acct-free": {1000, true}, "acct-backer": {2000, false}, "acct-sup": {0, false}, "acct-vip": {0, false}} {
		_, _, err := turn.Creds(key, key, time.Hour)
		if (err == errQuota) != want.refused {
			t.Errorf("%s: creds %v", key, err)
		}
		q := turn.QuotaState(key)
		if q.Limit != want.limit || q.Over != want.refused || q.Supporter != (levels[key] >= LevelSupporter) || q.Level != levels[key].wire() {
			t.Errorf("%s: %+v", key, q)
		}
	}
	set, _ := startTurn(t, st, TurnConfig{AdvertiseIP: "127.0.0.1", Quota: 1000, QuotaBacker: 1200, Level: hook})
	defer set.Close()
	if q := set.QuotaState("acct-backer"); q.Limit != 1200 || !q.Over {
		t.Errorf("-quota-backer sets the quota of a Backer: %+v", q)
	}
	free, _ := startTurn(t, st, TurnConfig{AdvertiseIP: "127.0.0.1", Level: hook})
	defer free.Close()
	if q := free.QuotaState("acct-backer"); q.Limit != 0 || q.Over || q.Level != "backer" {
		t.Errorf("no -quota, no limit at any level: %+v", q)
	}
}

// TestLevelsTellPods: a VIP added next to the relay (the CLI writes from another connection) is
// felt by the pods of the account without a reconnection, and so is its removal; a write of the
// relay itself (the TURN usage) tells nobody.
func TestLevelsTellPods(t *testing.T) {
	st, _ := OpenStore(filepath.Join(relayDir(t), "relay.db"))
	t.Cleanup(func() { st.Close() })
	turn, _ := startTurn(t, st, TurnConfig{AdvertiseIP: "127.0.0.1", Quota: 1000,
		Level: func(key string) AccountLevel { return st.Level(key, time.Now()) }})
	t.Cleanup(func() { turn.Close() })
	srv := New(st, turn)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go srv.watchLevels(ctx, 20*time.Millisecond)

	a := testAccount(t)
	pod := registerPod(t, ts, "A", "s1", a.ID, podDevice(t, a, "A").cert, true, podDevice(t, a, "A").key, false)
	defer pod.close()
	if m := ready(t, pod); m.Quota == nil || m.Quota.VIP || m.Quota.Limit != 1000 {
		t.Fatalf("the pod starts free: %+v", m.Quota)
	}
	time.Sleep(60 * time.Millisecond) // the watcher read its first levels
	if err := st.AddVIP(VIP{Kind: vipAccount, ID: a.ID}); err != nil {
		t.Fatal(err)
	}
	if m := nextUntil(t, pod, "quota"); m.Quota == nil || !m.Quota.VIP || m.Quota.Level != "vip" || m.Quota.Limit != 0 || !m.Quota.Supporter {
		t.Fatalf("the pod is told it is a VIP: %+v", m.Quota)
	}
	if err := st.AddUsage(a.ID, usageMonth(time.Now()), 10, 10); err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond)
	if _, err := st.RemoveVIP(vipAccount, a.ID); err != nil {
		t.Fatal(err)
	}
	if m := nextUntil(t, pod, "quota"); m.Quota == nil || m.Quota.VIP || m.Quota.Level != "" || m.Quota.Limit != 1000 {
		t.Fatalf("the next message is the removal, not the usage write: %+v", m.Quota)
	}

	// An end that passes is felt too, with no write at all.
	if err := st.AddVIP(VIP{Kind: vipAccount, ID: a.ID, Until: time.Now().Add(300*time.Millisecond).Unix() + 1}); err != nil {
		t.Fatal(err)
	}
	if m := nextUntil(t, pod, "quota"); m.Quota == nil || !m.Quota.VIP || m.Quota.Until == 0 {
		t.Fatalf("a VIP with an end: %+v", m.Quota)
	}
	if m := nextUntil(t, pod, "quota"); m.Quota == nil || m.Quota.VIP {
		t.Fatalf("its end is told: %+v", m.Quota)
	}
}

// TestDoneWordsByLevel: the window GitHub sends back to names the level.
func TestDoneWordsByLevel(t *testing.T) {
	if w := githubDoneWords("en", LevelFree).thanks; !strings.Contains(w, "Thanks for linking it") {
		t.Errorf("free: %q", w)
	}
	if w := githubDoneWords("en", LevelBacker).thanks; !strings.Contains(w, "As a Backer") {
		t.Errorf("backer: %q", w)
	}
	if w := githubDoneWords("fr-FR", LevelVIP).thanks; !strings.Contains(w, "En VIP") || !strings.Contains(w, "bisous") {
		t.Errorf("vip in French: %q", w)
	}
}
