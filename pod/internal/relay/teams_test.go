package relay

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestSeats: $5 a seat from $50 a month; nothing below, nothing for a one-time payment.
func TestSeats(t *testing.T) {
	for _, c := range []struct {
		sp   Sponsor
		want int
	}{
		{Sponsor{Cents: 4900}, 0},
		{Sponsor{Cents: 5000}, 10},
		{Sponsor{Cents: 7500}, 15},
		{Sponsor{Cents: 7999}, 15},
		{Sponsor{Cents: 10000}, 20},
		{Sponsor{Cents: 10000, OneTime: true}, 0},
		{Sponsor{}, 0},
	} {
		if got := seats(c.sp); got != c.want {
			t.Errorf("%+v: %d seats, want %d", c.sp, got, c.want)
		}
	}
}

// accountID is a fresh account id (the base64url of 32 bytes, like an Ed25519 key).
func accountID(t *testing.T) string {
	t.Helper()
	b := make([]byte, 32)
	rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

// TestTeamLevel: the members within the seats are VIP with the name of the team, the owner takes
// no seat, an extra member is refused, a lower count drops the members added last, and the end of
// the sponsorship drops them all.
func TestTeamLevel(t *testing.T) {
	st := newGitHubStore(t)
	now := time.Now()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(st.SaveSponsors([]Sponsor{{ID: "100", Login: "boss", Cents: 5500}}))
	must(st.PutTeam(Team{OwnerID: "100", Name: "boss", AdminID: "100"}))
	if err := st.AddTeamMember("100", TeamMember{Kind: vipGitHub, ID: "100", Login: "boss"}); err == nil {
		t.Fatal("the owner takes no seat")
	}
	var members []string
	for range 11 {
		a := accountID(t)
		must(st.AddTeamMember("100", TeamMember{Kind: vipAccount, ID: a}))
		members = append(members, a)
	}
	if err := st.AddTeamMember("100", TeamMember{Kind: vipAccount, ID: accountID(t)}); err == nil || !strings.Contains(err.Error(), "no seat left (11 of 11)") {
		t.Fatalf("a twelfth member on 11 seats: %v", err)
	}
	must(st.AddTeamMember("100", TeamMember{Kind: vipAccount, ID: members[3]})) // added again: kept, no seat taken
	// A member by GitHub id, through the account linked to it.
	must(st.LinkGitHub("acct-linked", "777", "friend"))
	must(st.SaveSponsors([]Sponsor{{ID: "100", Login: "boss", Cents: 6000}}))
	must(st.AddTeamMember("100", TeamMember{Kind: vipGitHub, ID: "777", Login: "friend"}))

	if al := st.Level(members[0], now); al.Level != LevelVIP || al.Team != "boss" || al.Until != 0 {
		t.Fatalf("a member is a VIP of the team: %+v", al)
	}
	if al := st.Level("acct-linked", now); al.Level != LevelVIP || al.Team != "boss" {
		t.Fatalf("a member by GitHub id: %+v", al)
	}
	// The count falls to 10: the members added last lose the level first.
	must(st.SaveSponsors([]Sponsor{{ID: "100", Login: "boss", Cents: 5000}}))
	if al := st.Level(members[9], now); al.Team != "boss" {
		t.Fatalf("the tenth member keeps a seat: %+v", al)
	}
	if al := st.Level(members[10], now); al.Team != "" || al.Level != LevelFree {
		t.Fatalf("the eleventh member is beyond the seats: %+v", al)
	}
	if al := st.Level("acct-linked", now); al.Team != "" {
		t.Fatalf("the twelfth one too: %+v", al)
	}
	// The sponsorship ends: nobody keeps the level.
	must(st.CancelSponsor("100"))
	if al := st.Level(members[0], now); al.Team != "" || al.Level != LevelFree {
		t.Fatalf("no sponsorship, no team: %+v", al)
	}
	if levels, _ := st.levels(now); len(levels) != 0 {
		t.Fatalf("the tick sees the members and finds them free: %+v", levels)
	}
}

// TestTeamOrgAdmin: the team flow asks read:org, checks that the person is the one linked to the
// account, makes them the admin of each sponsoring organization they are an admin of, says why
// not for one that restricts the apps, and never keeps the token.
func TestTeamOrgAdmin(t *testing.T) {
	st := newGitHubStore(t)
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization") == "Bearer a-user-token"
		switch {
		case r.URL.Path == "/login/oauth/access_token":
			json.NewEncoder(w).Encode(map[string]any{"access_token": "a-user-token"})
		case r.URL.Path == "/user" && auth:
			json.NewEncoder(w).Encode(map[string]any{"id": 4242, "login": "drsmith"})
		case r.URL.Path == "/user/memberships/orgs/acme" && auth:
			json.NewEncoder(w).Encode(map[string]any{"state": "active", "role": "admin"})
		case r.URL.Path == "/user/memberships/orgs/plain" && auth:
			json.NewEncoder(w).Encode(map[string]any{"state": "active", "role": "member"})
		case r.URL.Path == "/user/memberships/orgs/closed" && auth:
			http.Error(w, "OAuth App access restrictions", http.StatusForbidden)
		default:
			http.NotFound(w, r)
		}
	}))
	defer fake.Close()
	g := NewGitHub(GitHubConfig{ClientID: "id", ClientSecret: "secret"}, st)
	g.tokenURL, g.apiBase, g.authURL = fake.URL+"/login/oauth/access_token", fake.URL, fake.URL+"/authorize"
	srv := New(st, nil)
	srv.GitHub = g
	if err := st.SaveSponsors([]Sponsor{
		{ID: "1", Login: "acme", Cents: 5000, Org: true},
		{ID: "2", Login: "plain", Cents: 5000, Org: true},
		{ID: "3", Login: "closed", Cents: 10000, Org: true},
		{ID: "4", Login: "small", Cents: 1000, Org: true},
	}); err != nil {
		t.Fatal(err)
	}
	conn := &identityConn{account: "acct-1", key: "dev-1"}
	start := func() string {
		t.Helper()
		target, err := srv.githubStart(conn, purposeTeam)
		if err != nil {
			t.Fatal(err)
		}
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, target, nil))
		loc, _ := url.Parse(rec.Header().Get("Location"))
		if loc.Query().Get("scope") != "read:org" {
			t.Fatalf("the team flow asks read:org: %s", rec.Header().Get("Location"))
		}
		rec = httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/_relay/github/callback?code=c&state="+loc.Query().Get("state"), nil))
		return rec.Body.String()
	}
	if page := start(); !strings.Contains(page, "is not the one linked") {
		t.Fatalf("a GitHub account not linked to the account is refused: %s", page)
	}
	if err := st.LinkGitHub("acct-1", "4242", "drsmith"); err != nil {
		t.Fatal(err)
	}
	page := start()
	if !strings.Contains(page, "You manage the team acme") || !strings.Contains(page, "closed restricts the apps") || strings.Contains(page, "a-user-token") {
		t.Fatalf("the window says what was proven: %s", page)
	}
	if teams, _ := st.Teams("4242"); len(teams) != 1 || teams[0].Name != "acme" || !teams[0].Org {
		t.Fatalf("the admin of acme only: %+v", teams)
	}
	if target, _ := srv.githubStart(conn, ""); target == "" {
		t.Fatal("the plain link still works")
	} else {
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, target, nil))
		if strings.Contains(rec.Header().Get("Location"), "scope=") {
			t.Fatalf("the plain link asks no scope: %s", rec.Header().Get("Location"))
		}
	}
}

// TestTeamMessages: the panel of the admin lists its teams (its own one made on first use), adds
// and removes members by account id or GitHub login, is refused past the seats, and a link that
// proved no key or an account that manages nothing is refused.
func TestTeamMessages(t *testing.T) {
	st := newGitHubStore(t)
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/users/friend" && r.Header.Get("Authorization") == "Bearer maintainer" {
			json.NewEncoder(w).Encode(map[string]any{"id": 777, "login": "Friend"})
			return
		}
		http.NotFound(w, r)
	}))
	defer fake.Close()
	g := NewGitHub(GitHubConfig{ClientID: "id", ClientSecret: "secret", Token: "maintainer"}, st)
	g.apiBase = fake.URL
	srv := New(st, nil)
	srv.GitHub = g
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(st.LinkGitHub("acct-boss", "100", "boss"))
	must(st.SaveSponsors([]Sponsor{{ID: "100", Login: "boss", Cents: 5000}, {ID: "9", Login: "acme", Cents: 5000, Org: true}}))
	boss := &identityConn{account: "acct-boss", key: "dev"}

	if m := srv.teamMessage(&identityConn{account: "acct-boss"}, idMsg{Type: "team"}); m.Type != "team" || len(m.Teams) != 0 {
		t.Fatalf("a link that proved no key sees no team: %+v", m)
	}
	if m := srv.teamMessage(&identityConn{account: "acct-boss"}, idMsg{Type: "teamAdd", Owner: "100", Member: accountID(t)}); m.Type != "error" {
		t.Fatalf("nor changes one: %+v", m)
	}
	m := srv.teamMessage(boss, idMsg{Type: "team"})
	if m.Type != "team" || len(m.Teams) != 1 || m.Teams[0].Name != "boss" || m.Teams[0].Seats != 10 || !m.MayOwn {
		t.Fatalf("the own team of a $50 sponsor, and the org flow offered: %+v", m)
	}
	member := accountID(t)
	if m = srv.teamMessage(boss, idMsg{Type: "teamAdd", Owner: "100", Member: member}); m.Type != "team" || len(m.Teams[0].Members) != 1 {
		t.Fatalf("add by account id: %+v", m)
	}
	if m = srv.teamMessage(boss, idMsg{Type: "teamAdd", Owner: "100", Member: "friend"}); m.Type != "team" || m.Teams[0].Members[1].Login != "Friend" || m.Teams[0].Members[1].ID != "777" {
		t.Fatalf("add by GitHub login: %+v", m)
	}
	if m = srv.teamMessage(boss, idMsg{Type: "teamAdd", Owner: "100", Member: "nobody"}); m.Type != "error" || !strings.Contains(m.Message, "no GitHub account") {
		t.Fatalf("an unknown login: %+v", m)
	}
	for range 8 {
		srv.teamMessage(boss, idMsg{Type: "teamAdd", Owner: "100", Member: accountID(t)})
	}
	if m = srv.teamMessage(boss, idMsg{Type: "teamAdd", Owner: "100", Member: accountID(t)}); m.Type != "error" || !strings.Contains(m.Message, "no seat left (10 of 10)") {
		t.Fatalf("past the seats: %+v", m)
	}
	if m = srv.teamMessage(boss, idMsg{Type: "teamRemove", Owner: "100", Kind: vipGitHub, Member: "777"}); m.Type != "team" || len(m.Teams[0].Members) != 9 {
		t.Fatalf("remove by the kind and id of the list: %+v", m)
	}
	if m = srv.teamMessage(boss, idMsg{Type: "teamAdd", Owner: "9", Member: member}); m.Type != "error" {
		t.Fatalf("a team the account does not manage: %+v", m)
	}
	if b, _ := json.Marshal(m); strings.Contains(string(b), "teams") {
		t.Fatalf("an error carries no team: %s", b)
	}
}

// TestTeamMemberTold: a member is told its level when the admin adds it, and again when the
// sponsorship of the owner drops below the Team tier (the tick: the webhook only tells the
// owner's accounts), without reconnecting.
func TestTeamMemberTold(t *testing.T) {
	st, _ := OpenStore(filepath.Join(relayDir(t), "relay.db"))
	t.Cleanup(func() { st.Close() })
	turn, _ := startTurn(t, st, TurnConfig{AdvertiseIP: "127.0.0.1", Quota: 1000,
		Level: func(key string) AccountLevel { return st.Level(key, time.Now()) }})
	t.Cleanup(func() { turn.Close() })
	srv := New(st, turn)
	srv.GitHub = NewGitHub(GitHubConfig{ClientID: "id", ClientSecret: "secret"}, st)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go srv.watchLevels(ctx, 20*time.Millisecond)

	if err := st.LinkGitHub("acct-boss", "100", "boss"); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveSponsors([]Sponsor{{ID: "100", Login: "boss", Cents: 5000}}); err != nil {
		t.Fatal(err)
	}
	a := testAccount(t)
	pod := registerPod(t, ts, "A", "s1", a.ID, podDevice(t, a, "A").cert, true, podDevice(t, a, "A").key, false)
	defer pod.close()
	if m := ready(t, pod); m.Quota == nil || m.Quota.Team != "" {
		t.Fatalf("the member starts free: %+v", m.Quota)
	}
	if m := srv.teamMessage(&identityConn{account: "acct-boss", key: "dev"}, idMsg{Type: "teamAdd", Owner: "100", Member: a.ID}); m.Type != "team" {
		t.Fatalf("add: %+v", m)
	}
	if m := nextUntil(t, pod, "quota"); m.Quota == nil || m.Quota.Team != "boss" || m.Quota.Level != "vip" || m.Quota.Limit != 0 {
		t.Fatalf("the member is told it is a VIP of the team: %+v", m.Quota)
	}
	time.Sleep(60 * time.Millisecond)
	if err := st.UpdateSponsor(Sponsor{ID: "100", Login: "boss", Cents: 1000}); err != nil {
		t.Fatal(err)
	}
	// The tick may first repeat the addition it saw in the base: read on to the change.
	var q *QuotaState
	for range 3 {
		if q = nextUntil(t, pod, "quota").Quota; q != nil && q.Team == "" {
			break
		}
	}
	if q == nil || q.Team != "" || q.Level != "" || q.Limit != 1000 {
		t.Fatalf("the owner at $10: the member is told it is free again: %+v", q)
	}
}

// TestTeamOrgWebhook: a sponsorship event of an organization keeps its kind.
func TestTeamOrgWebhook(t *testing.T) {
	st := newGitHubStore(t)
	srv := New(st, nil)
	srv.GitHub = NewGitHub(GitHubConfig{ClientID: "id", ClientSecret: "secret", WebhookSecret: "s3cret"}, st)
	body, _ := json.Marshal(map[string]any{"action": "created", "sponsorship": map[string]any{
		"created_at": time.Now().UTC().Format(time.RFC3339),
		"sponsor":    map[string]any{"id": 9000, "login": "acme", "type": "Organization"},
		"tier":       map[string]any{"monthly_price_in_cents": 5000, "is_one_time": false}}})
	mac := hmac.New(sha256.New, []byte("s3cret"))
	mac.Write(body)
	req := httptest.NewRequest(http.MethodPost, "/_relay/github/webhook", strings.NewReader(string(body)))
	req.Header.Set("X-Hub-Signature-256", "sha256="+hex.EncodeToString(mac.Sum(nil)))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if sp, _ := st.Sponsor("9000"); rec.Code != http.StatusNoContent || sp == nil || !sp.Org || seats(*sp) != 10 {
		t.Fatalf("the organization row: %d %+v", rec.Code, sp)
	}
}
