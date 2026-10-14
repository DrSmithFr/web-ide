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
	if err := st.SaveSponsors([]Sponsor{{ID: "77", Login: "other"}}); err != nil {
		t.Fatal(err)
	}
	if !st.IsSponsor("77") || st.IsSponsor("42") || st.IsSponsor("") {
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
	if err := st.SaveSponsors([]Sponsor{{ID: "77", Login: "drsmith"}}); err != nil {
		t.Fatal(err)
	}
	if !g.isSupporter("acct-sponsor") {
		t.Fatal("a linked sponsor is a supporter")
	}
	if g.isSupporter("acct-plain") {
		t.Fatal("a linked login that does not sponsor is not")
	}
	if g.isSupporter("acct-none") || g.isSupporter("") {
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
	g := NewGitHub(GitHubConfig{ClientID: "id", ClientSecret: "secret"}, st)
	if err := st.LinkGitHub("acct-sponsor", "77", "drsmith"); err != nil {
		t.Fatal(err)
	}
	if err := st.LinkGitHub("acct-plain", "12", "visitor"); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveSponsors([]Sponsor{{ID: "77", Login: "drsmith"}}); err != nil {
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
	turn, err := NewTurn(st, TurnConfig{AdvertiseIP: "127.0.0.1", Port: freeTurnPort(t), Quota: 1000, Supporter: g.isSupporter})
	if err != nil {
		t.Fatal(err)
	}
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

// TestGitHubWebhook: a sponsorship event moves the sponsor list when its HMAC matches, and is
// refused when it does not.
func TestGitHubWebhook(t *testing.T) {
	st := newGitHubStore(t)
	g := NewGitHub(GitHubConfig{ClientID: "id", ClientSecret: "secret", WebhookSecret: "s3cret"}, st)
	srv := New(newGitHubStore(t), nil)
	srv.GitHub = g
	if err := st.LinkGitHub("acct-1", "4242", "drsmith"); err != nil {
		t.Fatal(err)
	}

	body := []byte(`{"action":"sponsorship_created","sponsor":{"id":4242,"login":"drsmith"}}`)
	sign := func(b []byte) string {
		mac := hmac.New(sha256.New, []byte("s3cret"))
		mac.Write(b)
		return "sha256=" + hex.EncodeToString(mac.Sum(nil))
	}

	req := httptest.NewRequest(http.MethodPost, "/_relay/github/webhook", strings.NewReader(string(body)))
	req.Header.Set("X-Hub-Signature-256", sign(body))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("a signed event: %d %s", rec.Code, rec.Body.String())
	}
	if !g.isSupporter("acct-1") {
		t.Fatal("the event made the account a supporter")
	}

	req = httptest.NewRequest(http.MethodPost, "/_relay/github/webhook", strings.NewReader(string(body)))
	req.Header.Set("X-Hub-Signature-256", "sha256=deadbeef")
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("a bad signature is refused: %d", rec.Code)
	}

	body = []byte(`{"action":"sponsorship_cancelled","sponsor":{"id":4242,"login":"drsmith"}}`)
	req = httptest.NewRequest(http.MethodPost, "/_relay/github/webhook", strings.NewReader(string(body)))
	req.Header.Set("X-Hub-Signature-256", sign(body))
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("a cancellation: %d", rec.Code)
	}
	if g.isSupporter("acct-1") {
		t.Fatal("a cancelled sponsorship ends the tier")
	}
}

// TestGitHubSponsorsRefresh: the list is read with the maintainer token and cached.
func TestGitHubSponsorsRefresh(t *testing.T) {
	st := newGitHubStore(t)
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/user/sponsorships_as_maintainer" {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("Authorization") != "Bearer maintainer" {
			http.Error(w, "no token", http.StatusUnauthorized)
			return
		}
		json.NewEncoder(w).Encode([]map[string]any{
			{"accountUser": map[string]any{"id": 4242, "login": "drsmith"}},
			{"accountUser": map[string]any{"id": 7, "login": "someone"}},
		})
	}))
	defer fake.Close()
	g := NewGitHub(GitHubConfig{ClientID: "id", ClientSecret: "secret", Token: "maintainer"}, st)
	g.apiBase = fake.URL
	if err := g.refreshSponsors(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !st.IsSponsor("4242") || !st.IsSponsor("7") {
		t.Fatal("the sponsor list is cached in the base")
	}
}
