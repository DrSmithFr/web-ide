package relay

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// isSponsor: the row of a GitHub id lifts the quota (Supporter or above).
func isSponsor(st *Store, githubID string) bool {
	sp, err := st.Sponsor(githubID)
	if err != nil || sp == nil {
		return false
	}
	lv, _ := levelOf(*sp, time.Now())
	return lv >= LevelSupporter
}

// isSupporter: the level of an account lifts the quota.
func isSupporter(st *Store, account string) bool {
	return st.Level(account, time.Now()).Level >= LevelSupporter
}

func levelHook(st *Store) func(string) AccountLevel {
	return func(key string) AccountLevel { return st.Level(key, time.Now()) }
}

func newGitHubStore(t *testing.T) *Store {
	t.Helper()
	st, err := OpenStore(filepath.Join(t.TempDir(), "relay.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

// TestGitHubLinkStore: one login per account, replaced on a new link, dropped on unlink; the
// sponsor list is what lifts the quota.
func TestGitHubLinkStore(t *testing.T) {
	st := newGitHubStore(t)
	if id, login, err := st.GitHubLink("acct-1"); err != nil || id != "" || login != "" {
		t.Fatalf("an account with no link: %q %q %v", id, login, err)
	}
	if err := st.LinkGitHub("acct-1", "42", "drsmith"); err != nil {
		t.Fatal(err)
	}
	if err := st.LinkGitHub("acct-1", "77", "other"); err != nil {
		t.Fatal(err)
	}
	if id, login, err := st.GitHubLink("acct-1"); err != nil || id != "77" || login != "other" {
		t.Fatalf("a new link replaces the old one: %q %q %v", id, login, err)
	}
	if err := st.SaveSponsors([]Sponsor{{ID: "77", Login: "other", Cents: 500}}); err != nil {
		t.Fatal(err)
	}
	if !isSponsor(st, "77") || isSponsor(st, "42") || isSponsor(st, "") {
		t.Fatal("the sponsor list is what counts")
	}
	if err := st.UnlinkGitHub("acct-1"); err != nil {
		t.Fatal(err)
	}
	if id, _, _ := st.GitHubLink("acct-1"); id != "" {
		t.Fatal("unlink drops the link")
	}
}

// TestGitHubSupporter: an account is freed from its quota when the GitHub login it linked is a
// sponsor; an unlinked account, or a linked one that does not sponsor, is not.
func TestGitHubSupporter(t *testing.T) {
	st := newGitHubStore(t)
	g := NewGitHub(GitHubConfig{ClientID: "id", ClientSecret: "secret"}, st)
	if g == nil {
		t.Fatal("a client id and secret make a GitHub side")
	}
	if NewGitHub(GitHubConfig{}, st) != nil {
		t.Fatal("without an OAuth app, the relay has no GitHub link at all")
	}
	if err := st.LinkGitHub("acct-sponsor", "77", "drsmith"); err != nil {
		t.Fatal(err)
	}
	if err := st.LinkGitHub("acct-plain", "12", "visitor"); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveSponsors([]Sponsor{{ID: "77", Login: "drsmith", Cents: 500}}); err != nil {
		t.Fatal(err)
	}
	if !isSupporter(st, "acct-sponsor") {
		t.Fatal("a linked sponsor is a supporter")
	}
	if isSupporter(st, "acct-plain") {
		t.Fatal("a linked login that does not sponsor is not")
	}
	if isSupporter(st, "acct-none") || isSupporter(st, "") {
		t.Fatal("an account with no link is not")
	}
}

// TestGitHubOAuth: the code the browser is sent back with is exchanged, the id of the person is
// read and stored, and the token is never kept.
func TestGitHubOAuth(t *testing.T) {
	st := newGitHubStore(t)
	var gotToken string
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/login/oauth/access_token":
			if r.Method != http.MethodPost {
				http.Error(w, "POST", http.StatusMethodNotAllowed)
				return
			}
			r.ParseForm()
			if r.Form.Get("code") != "the-code" {
				http.Error(w, "bad code", http.StatusBadRequest)
				return
			}
			gotToken = "a-user-token"
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{"access_token": gotToken})
		case "/user":
			if r.Header.Get("Authorization") != "Bearer "+gotToken {
				http.Error(w, "no token", http.StatusUnauthorized)
				return
			}
			json.NewEncoder(w).Encode(map[string]any{"id": 4242, "login": "drsmith"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer fake.Close()

	g := NewGitHub(GitHubConfig{ClientID: "id", ClientSecret: "secret"}, st)
	g.tokenURL = fake.URL + "/login/oauth/access_token"
	g.apiBase = fake.URL
	srv := New(newGitHubStore(t), nil)
	srv.GitHub = g
	srv.mu.Lock()
	g.pending["state-1"] = &githubPending{account: "acct-1", device: "dev-1", at: time.Now()}
	srv.mu.Unlock()

	req := httptest.NewRequest(http.MethodGet, "/_relay/github/callback?state=state-1&code=the-code", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("the callback answered %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "drsmith") {
		t.Fatalf("the page tells the login: %s", rec.Body.String())
	}
	// It thanks, closes on a button, and runs no inline script (the policy of the relay
	// refuses one): its script is a file.
	page := rec.Body.String()
	if !strings.Contains(page, "Thanks for linking it") || !strings.Contains(page, `id="close"`) ||
		!strings.Contains(page, `src="/_relay/github/done.js"`) || strings.Contains(page, "<script>") || strings.Contains(page, "style=") {
		t.Fatalf("the window GitHub sends back to: %s", page)
	}
	id, login, err := st.GitHubLink("acct-1")
	if err != nil || id != "4242" || login != "drsmith" {
		t.Fatalf("the account is linked to the id the token names: %q %q %v", id, login, err)
	}
	g.mu.Lock()
	_, still := g.pending["state-1"]
	g.mu.Unlock()
	if still {
		t.Fatal("a state is single use")
	}

	// An unknown or expired state is refused: the callback cannot be aimed at an account.
	req = httptest.NewRequest(http.MethodGet, "/_relay/github/callback?state=nope&code=the-code", nil)
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("an unknown state: %d", rec.Code)
	}
}

// TestGitHubQuotaLifted: the quota of the hosted relay is what a supporter is freed from — the
// link of the account to a GitHub login on the sponsor list is enough, and an account that is
// linked but does not sponsor stays under the quota.
func TestGitHubQuotaLifted(t *testing.T) {
	st := newGitHubStore(t)
	if err := st.LinkGitHub("acct-sponsor", "77", "drsmith"); err != nil {
		t.Fatal(err)
	}
	if err := st.LinkGitHub("acct-plain", "12", "visitor"); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveSponsors([]Sponsor{{ID: "77", Login: "drsmith", Cents: 500}}); err != nil {
		t.Fatal(err)
	}
	month := usageMonth(time.Now())
	for _, key := range []string{"acct-sponsor", "acct-plain", "pod-alone"} {
		if err := st.AddUsage(key, month, 900, 300); err != nil {
			t.Fatal(err)
		}
		if err := st.Register(key, "secret-"+key, ""); err != nil {
			t.Fatal(err)
		}
	}
	turn, _ := startTurn(t, st, TurnConfig{AdvertiseIP: "127.0.0.1", Quota: 1000, Level: levelHook(st)})
	defer turn.Close()
	if _, _, err := turn.Creds("acct-sponsor", "acct-sponsor", time.Hour); err != nil {
		t.Fatalf("a sponsor over the quota: %v", err)
	}
	if _, _, err := turn.Creds("acct-plain", "acct-plain", time.Hour); err != errQuota {
		t.Fatalf("a linked login that does not sponsor stays under the quota: %v", err)
	}
	if _, _, err := turn.Creds("pod-alone", "pod-alone", time.Hour); err != errQuota {
		t.Fatalf("an account with no link: %v", err)
	}
	if st := turn.QuotaState("acct-sponsor"); !st.Supporter || st.Over || st.Used != 1200 {
		t.Fatalf("what the relay tells a sponsor: %+v", st)
	}
}

// TestSponsorRule: the tier grid of #87, monthly and one-time, at every threshold and on both
// sides of the end of a one-time payment.
func TestSponsorRule(t *testing.T) {
	now := time.Now()
	days := func(n int) int64 { return now.Add(-time.Duration(n) * day).Unix() }
	for _, c := range []struct {
		name  string
		sp    Sponsor
		want  Level
		lasts int // days left, 0: no end
	}{
		{"monthly $1", Sponsor{Cents: 100}, LevelFree, 0},
		{"monthly $1.99", Sponsor{Cents: 199}, LevelFree, 0},
		{"monthly $2", Sponsor{Cents: 200, Since: days(400)}, LevelBacker, 0},
		{"monthly $4.99", Sponsor{Cents: 499}, LevelBacker, 0},
		{"monthly $5", Sponsor{Cents: 500}, LevelSupporter, 0},
		{"monthly $7", Sponsor{Cents: 700}, LevelSupporter, 0},
		{"monthly $10", Sponsor{Cents: 1000}, LevelVIP, 0},
		{"monthly $49", Sponsor{Cents: 4900}, LevelVIP, 0},
		{"monthly $50 (Team)", Sponsor{Cents: 5000}, LevelVIP, 0},
		{"one-time $2", Sponsor{Cents: 200, OneTime: true, Since: days(1)}, LevelFree, 0},
		{"one-time $4.99", Sponsor{Cents: 499, OneTime: true, Since: days(1)}, LevelFree, 0},
		{"one-time $5 day 29", Sponsor{Cents: 500, OneTime: true, Since: days(29)}, LevelSupporter, 1},
		{"one-time $5 day 31", Sponsor{Cents: 500, OneTime: true, Since: days(31)}, LevelFree, 0},
		{"one-time $7", Sponsor{Cents: 700, OneTime: true, Since: days(0)}, LevelSupporter, 30},
		{"one-time $10 day 89", Sponsor{Cents: 1000, OneTime: true, Since: days(89)}, LevelVIP, 1},
		{"one-time $10 day 91", Sponsor{Cents: 1000, OneTime: true, Since: days(91)}, LevelFree, 0},
		{"one-time $49", Sponsor{Cents: 4900, OneTime: true, Since: days(0)}, LevelVIP, 90},
		{"one-time $50", Sponsor{Cents: 5000, OneTime: true, Since: days(200)}, LevelVIP, 160},
		{"one-time $50 day 361", Sponsor{Cents: 5000, OneTime: true, Since: days(361)}, LevelFree, 0},
	} {
		lv, until := levelOf(c.sp, now)
		left := 0
		if until > 0 {
			left = int((time.Unix(until, 0).Sub(now) + day/2) / day)
		}
		if lv != c.want || left != c.lasts {
			t.Errorf("%s: %v with %d days left, want %v with %d", c.name, lv, left, c.want, c.lasts)
		}
	}
}

// TestSponsorMerge: two sponsorships of one sponsor do not add up, the best live one counts; a
// monthly one replaces a monthly one (a tier change, down too).
func TestSponsorMerge(t *testing.T) {
	now := time.Now()
	days := func(n int) int64 { return now.Add(-time.Duration(n) * day).Unix() }
	backer := Sponsor{Cents: 200, Since: days(300)}
	vipOnce := Sponsor{Cents: 1000, OneTime: true, Since: days(10)}
	if got := mergeSponsor(&backer, vipOnce, now); got != vipOnce {
		t.Errorf("a live VIP payment wins over a Backer: %+v", got)
	}
	if got := mergeSponsor(&vipOnce, backer, now); got != vipOnce {
		t.Errorf("in either order: %+v", got)
	}
	ended := Sponsor{Cents: 5000, OneTime: true, Since: days(400)}
	if got := mergeSponsor(&backer, ended, now); got != backer {
		t.Errorf("an ended payment never wins: %+v", got)
	}
	supporter := Sponsor{Cents: 500, Since: days(300)}
	supOnce := Sponsor{Cents: 500, OneTime: true, Since: days(2)}
	if got := mergeSponsor(&supOnce, supporter, now); got != supporter {
		t.Errorf("at one level, the one with no end wins: %+v", got)
	}
	if got := mergeSponsor(&supporter, backer, now); got != backer {
		t.Errorf("a monthly one replaces a monthly one: %+v", got)
	}
}

// sponsorshipEvent is a webhook body in the shape GitHub sends (trimmed to what matters).
func sponsorshipEvent(action string, id int64, cents int64, oneTime bool, created time.Time) []byte {
	b, _ := json.Marshal(map[string]any{
		"action": action,
		"sponsorship": map[string]any{
			"node_id":       "S_kwHOA",
			"created_at":    created.UTC().Format(time.RFC3339),
			"privacy_level": "public",
			"sponsorable":   map[string]any{"login": "DrSmithFr", "id": 1, "type": "User"},
			"sponsor":       map[string]any{"login": "sponsor" + strconv64(id), "id": id, "type": "User"},
			"tier": map[string]any{
				"node_id": "ST_kwHOA", "name": "$5 a month", "description": "Supporter",
				"monthly_price_in_cents": cents, "monthly_price_in_dollars": cents / 100,
				"is_one_time": oneTime, "is_custom_amount": false,
			},
		},
		"sender": map[string]any{"login": "sponsor" + strconv64(id), "id": id},
	})
	return b
}

// TestGitHubWebhook: the real events move one row each when their HMAC matches, are refused when
// it does not, and never wipe the other rows of the list.
func TestGitHubWebhook(t *testing.T) {
	st := newGitHubStore(t)
	g := NewGitHub(GitHubConfig{ClientID: "id", ClientSecret: "secret", WebhookSecret: "s3cret"}, st)
	srv := New(newGitHubStore(t), nil)
	srv.GitHub = g
	if err := st.LinkGitHub("acct-1", "4242", "drsmith"); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveSponsors([]Sponsor{{ID: "7", Login: "someone", Cents: 500}}); err != nil {
		t.Fatal(err)
	}
	sign := func(b []byte) string {
		mac := hmac.New(sha256.New, []byte("s3cret"))
		mac.Write(b)
		return "sha256=" + hex.EncodeToString(mac.Sum(nil))
	}
	post := func(body []byte, sig string) int {
		req := httptest.NewRequest(http.MethodPost, "/_relay/github/webhook", strings.NewReader(string(body)))
		req.Header.Set("X-GitHub-Event", "sponsorship")
		req.Header.Set("X-Hub-Signature-256", sig)
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, req)
		return rec.Code
	}
	send := func(action string, cents int64, oneTime bool) {
		t.Helper()
		body := sponsorshipEvent(action, 4242, cents, oneTime, time.Now())
		if code := post(body, sign(body)); code != http.StatusNoContent {
			t.Fatalf("%s: %d", action, code)
		}
	}

	send("created", 500, false)
	if !isSupporter(st, "acct-1") {
		t.Fatal("a $5 sponsorship makes the account a supporter")
	}
	body := sponsorshipEvent("created", 4242, 500, false, time.Now())
	if code := post(body, "sha256=deadbeef"); code != http.StatusUnauthorized {
		t.Fatalf("a bad signature is refused: %d", code)
	}
	send("pending_cancellation", 500, false)
	if !isSupporter(st, "acct-1") {
		t.Fatal("a pending cancellation keeps the perks until the end of the paid period")
	}
	send("tier_changed", 300, false)
	if lv := st.Level("acct-1", time.Now()).Level; lv != LevelBacker {
		t.Fatalf("a tier change down to $3 makes a Backer: %v", lv)
	}
	send("tier_changed", 1500, false)
	if !isSupporter(st, "acct-1") {
		t.Fatal("a tier change up to $15 gives them back")
	}
	send("cancelled", 1500, false)
	if isSupporter(st, "acct-1") {
		t.Fatal("a cancelled sponsorship ends the tier")
	}
	send("created", 1000, true)
	if !isSupporter(st, "acct-1") {
		t.Fatal("a one-time payment of $10 counts")
	}
	send("cancelled", 1000, true)
	if !isSupporter(st, "acct-1") {
		t.Fatal("a cancel never drops a one-time payment")
	}
	if !isSponsor(st, "7") {
		t.Fatal("the events never touch the other rows")
	}
}

// graphqlPage is one page of viewer.sponsorshipsAsMaintainer in the shape GitHub answers.
func graphqlPage(next string, nodes ...map[string]any) map[string]any {
	return map[string]any{"data": map[string]any{"viewer": map[string]any{
		"sponsorshipsAsMaintainer": map[string]any{
			"pageInfo": map[string]any{"hasNextPage": next != "", "endCursor": next},
			"nodes":    nodes,
		}}}}
}

func sponsorshipNode(id int64, login string, cents int64, oneTime, active bool, created time.Time) map[string]any {
	return map[string]any{
		"isActive": active, "isOneTimePayment": oneTime,
		"createdAt":     created.UTC().Format(time.RFC3339),
		"tier":          map[string]any{"monthlyPriceInCents": cents},
		"sponsorEntity": map[string]any{"databaseId": id, "login": login},
	}
}

// TestGitHubSponsorsRefresh: the list is read through GraphQL with the maintainer token, page by
// page, private sponsors and one-time payments included, and replaces the cached one.
func TestGitHubSponsorsRefresh(t *testing.T) {
	st := newGitHubStore(t)
	now := time.Now()
	ago := func(days int) time.Time { return now.Add(-time.Duration(days) * 24 * time.Hour) }
	if err := st.SaveSponsors([]Sponsor{{ID: "999", Login: "gone", Cents: 500}}); err != nil {
		t.Fatal(err)
	}
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/graphql" || r.Method != http.MethodPost {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("Authorization") != "Bearer maintainer" {
			http.Error(w, "no token", http.StatusUnauthorized)
			return
		}
		var q struct {
			Query     string         `json:"query"`
			Variables map[string]any `json:"variables"`
		}
		json.NewDecoder(r.Body).Decode(&q)
		// Organization.databaseId needs read:org, which the token of the maintainer does not have.
		if strings.Contains(q.Query, "Organization") {
			http.Error(w, "The 'databaseId' field requires one of the following scopes: ['read:org']", http.StatusBadRequest)
			return
		}
		if !strings.Contains(q.Query, "activeOnly: false") || !strings.Contains(q.Query, "includePrivate: true") {
			http.Error(w, "the query misses past and private sponsorships", http.StatusBadRequest)
			return
		}
		if q.Variables["after"] == nil {
			json.NewEncoder(w).Encode(graphqlPage("c1",
				sponsorshipNode(4242, "drsmith", 500, false, true, ago(100)),
				sponsorshipNode(3, "small", 300, false, true, ago(5)),
				sponsorshipNode(8, "past", 2500, false, false, ago(300)),
			))
			return
		}
		json.NewEncoder(w).Encode(graphqlPage("",
			sponsorshipNode(10, "recent-once", 1000, true, false, ago(10)),
			// An organization: the User fragment does not match, GitHub answers an empty entity.
			map[string]any{"isActive": true, "isOneTimePayment": false, "createdAt": ago(5).UTC().Format(time.RFC3339),
				"tier": map[string]any{"monthlyPriceInCents": 10000}, "sponsorEntity": map[string]any{}},
			// $10 once is a VIP for 90 days: this one ended.
			sponsorshipNode(40, "old-once", 1000, true, false, ago(95)),
			// A monthly sponsor whose one-time payment ended keeps the monthly row.
			sponsorshipNode(4242, "drsmith", 2000, true, false, ago(100)),
		))
	}))
	defer fake.Close()
	g := NewGitHub(GitHubConfig{ClientID: "id", ClientSecret: "secret", Token: "maintainer"}, st)
	g.apiBase = fake.URL
	if err := g.refreshSponsors(context.Background()); err != nil {
		t.Fatal(err)
	}
	for id, want := range map[string]bool{"4242": true, "3": false, "8": false, "10": true, "40": false, "999": false} {
		if got := isSponsor(st, id); got != want {
			t.Errorf("sponsor %s: supporter %v, want %v", id, got, want)
		}
	}
	if sp, _ := st.Sponsor("4242"); sp == nil || sp.OneTime || sp.Cents != 500 {
		t.Errorf("the monthly row is kept: %+v", sp)
	}
	if sp, _ := st.Sponsor("8"); sp != nil {
		t.Errorf("a past monthly sponsor is not kept: %+v", sp)
	}
}

// TestGitHubSponsorsRefreshError: a GraphQL error (a missing scope, a field gone) is reported
// and leaves the cached list as it was.
func TestGitHubSponsorsRefreshError(t *testing.T) {
	st := newGitHubStore(t)
	if err := st.SaveSponsors([]Sponsor{{ID: "7", Login: "someone", Cents: 500}}); err != nil {
		t.Fatal(err)
	}
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"errors": []map[string]any{{"message": "Your token has not been granted the required scopes"}}})
	}))
	defer fake.Close()
	g := NewGitHub(GitHubConfig{ClientID: "id", ClientSecret: "secret", Token: "maintainer"}, st)
	g.apiBase = fake.URL
	if err := g.refreshSponsors(context.Background()); err == nil || !strings.Contains(err.Error(), "scopes") {
		t.Fatalf("the error of GitHub is told: %v", err)
	}
	if !isSponsor(st, "7") {
		t.Fatal("a failed read keeps the cached list")
	}
}

// TestGitHubNotice: the start line says when the link is off, and names the env keys the relay
// does not read (the hosted relay ran without a link from a file written with GITHUB_*).
func TestGitHubNotice(t *testing.T) {
	stray := map[string]string{"GITHUB_CLIENT_ID": "x", "GITHUB_CLIENT_SECRET": "y", "GITHUB_TOKEN": "z"}
	msg := githubNotice(GitHubConfig{}, stray, "/etc/antennae/env")
	for _, want := range []string{"no GitHub link", "/etc/antennae/env names GITHUB_CLIENT_ID, GITHUB_CLIENT_SECRET, GITHUB_TOKEN", "rename them WEBIDE_GITHUB_CLIENT"} {
		if !strings.Contains(msg, want) {
			t.Errorf("stray keys: %q misses %q", msg, want)
		}
	}
	if msg := githubNotice(GitHubConfig{}, map[string]string{}, ""); strings.Contains(msg, "rename") {
		t.Errorf("a relay with no env file: %q", msg)
	}
	msg = githubNotice(GitHubConfig{ClientID: "id", ClientSecret: "s"}, nil, "")
	if !strings.Contains(msg, "GitHub link on") || !strings.Contains(msg, "no WEBIDE_GITHUB_TOKEN") || !strings.Contains(msg, "no WEBIDE_GITHUB_WEBHOOK") {
		t.Errorf("a link without token nor webhook: %q", msg)
	}
	if msg := githubNotice(GitHubConfig{ClientID: "id", ClientSecret: "s", Token: "t", WebhookSecret: "w"}, nil, ""); msg != "relay: GitHub link on" {
		t.Errorf("a full link: %q", msg)
	}
}

// TestGitHubTellsPods: a sponsorship that moves the supporter state of an account reaches its
// pods at once (a quota message), so Settings does not show the old limit until they reconnect.
func TestGitHubTellsPods(t *testing.T) {
	st, _ := OpenStore(filepath.Join(relayDir(t), "relay.db"))
	t.Cleanup(func() { st.Close() })
	g := NewGitHub(GitHubConfig{ClientID: "id", ClientSecret: "secret", WebhookSecret: "s3cret"}, st)
	turn, _ := startTurn(t, st, TurnConfig{AdvertiseIP: "127.0.0.1", Quota: 1000, Level: levelHook(st)})
	t.Cleanup(func() { turn.Close() })
	srv := New(st, turn)
	srv.GitHub = g
	g.changed = srv.tellQuota
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	a := testAccount(t)
	pod := registerPod(t, ts, "A", "s1", a.ID, podDevice(t, a, "A").cert, true, podDevice(t, a, "A").key, false)
	defer pod.close()
	if m := ready(t, pod); m.Quota == nil || m.Quota.Supporter || m.Quota.Limit != 1000 {
		t.Fatalf("the pod starts under the quota: %+v", m.Quota)
	}
	if err := st.LinkGitHub(a.ID, "4242", "drsmith"); err != nil {
		t.Fatal(err)
	}

	body := sponsorshipEvent("created", 4242, 500, false, time.Now())
	mac := hmac.New(sha256.New, []byte("s3cret"))
	mac.Write(body)
	req := httptest.NewRequest(http.MethodPost, "/_relay/github/webhook", strings.NewReader(string(body)))
	req.Header.Set("X-Hub-Signature-256", "sha256="+hex.EncodeToString(mac.Sum(nil)))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("the event: %d", rec.Code)
	}
	if m := nextUntil(t, pod, "quota"); m.Quota == nil || !m.Quota.Supporter {
		t.Fatalf("the pod is told it is a supporter: %+v", m.Quota)
	}
}
