package relay

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestAdminVIP: `relay vip add/remove/list` against a base, a login resolved by the GitHub API
// (here a fake), an account id taken as it is, -days and -until setting an end.
func TestAdminVIP(t *testing.T) {
	dir := t.TempDir()
	if err := runAdmin(dir, []string{"supporters"}, &bytes.Buffer{}, ""); err == nil || !strings.Contains(err.Error(), "no relay base") {
		t.Fatalf("a folder with no relay.db is told, not filled: %v", err)
	}
	st, err := OpenStore(filepath.Join(dir, "relay.db"))
	if err != nil {
		t.Fatal(err)
	}
	st.Close()
	asked := 0
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked++
		if r.URL.Path != "/users/drsmith" {
			http.NotFound(w, r)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"id": 4242, "login": "DrSmith"})
	}))
	defer fake.Close()
	run := func(args ...string) (string, error) {
		var out bytes.Buffer
		err := runAdmin(dir, args, &out, fake.URL)
		return out.String(), err
	}
	account := testAccount(t).ID
	if !isAccountID(account) || isAccountID("drsmith") || isAccountID(strings.Repeat("a", 39)) {
		t.Fatal("an account id is told from a login")
	}
	if out, err := run("vip", "add", "drsmith", "-note", "the maintainer", "-days", "90"); err != nil || !strings.Contains(out, "@DrSmith is a VIP, until") {
		t.Fatalf("add by login: %q %v", out, err)
	}
	if out, err := run("vip", "add", "-until", time.Now().AddDate(0, 0, 3).Format("2006-01-02"), account); err != nil || !strings.Contains(out, account+" is a VIP") {
		t.Fatalf("add by account, the flags first: %q %v", out, err)
	}
	if _, err := run("vip", "add", "nobody"); err == nil || !strings.Contains(err.Error(), `no GitHub user "nobody"`) {
		t.Fatalf("an unknown login: %v", err)
	}
	if _, err := run("vip", "add", "drsmith", "-days", "3", "-until", "2030-01-01"); err == nil {
		t.Fatal("-days or -until, not both")
	}
	if _, err := run("vip", "add", account, "-until", "2001-01-01"); err == nil {
		t.Fatal("an end already past")
	}

	st, _ = OpenStore(filepath.Join(dir, "relay.db"))
	now := time.Now()
	if v := st.vipFor("", "4242", now); v == nil || v.Login != "DrSmith" || v.Note != "the maintainer" ||
		v.Until < now.Add(89*day).Unix() || v.Until > now.Add(91*day).Unix() {
		t.Fatalf("the row of the login: %+v", v)
	}
	if v := st.vipFor(account, "", now); v == nil || v.Until < now.Add(3*day).Unix() || v.Until > now.Add(5*day).Unix() {
		t.Fatalf("the row of the account, to the end of the day: %+v", v)
	}
	st.Close()

	if out, err := run("vip", "list"); err != nil || !strings.Contains(out, "@DrSmith") || !strings.Contains(out, "the maintainer") || !strings.Contains(out, account) {
		t.Fatalf("list: %q %v", out, err)
	}
	before := asked
	if out, err := run("vip", "remove", "drsmith"); err != nil || !strings.Contains(out, "no longer a VIP") {
		t.Fatalf("remove by login: %q %v", out, err)
	}
	if asked != before {
		t.Fatal("a login on the list is removed with no call to GitHub")
	}
	if _, err := run("vip", "remove", account); err != nil {
		t.Fatal(err)
	}
	if _, err := run("vip", "remove", account); err == nil {
		t.Fatal("removing again says it is not on the list")
	}
	if _, err := run("vip"); err == nil || !strings.Contains(err.Error(), "usage") {
		t.Fatalf("no command: %v", err)
	}
}

// TestAdminSupporters: everyone at Backer level or above, by level, with the source, the end,
// the linked accounts and the note; free sponsors and ended rows are not listed.
func TestAdminSupporters(t *testing.T) {
	dir := t.TempDir()
	st, err := OpenStore(filepath.Join(dir, "relay.db"))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(st.SaveSponsors([]Sponsor{
		{ID: "1", Login: "backer", Cents: 200},
		{ID: "2", Login: "supp", Cents: 700},
		{ID: "3", Login: "once", Cents: 1000, OneTime: true, Since: now.Add(-80 * day).Unix()},
		{ID: "4", Login: "small", Cents: 100},
		{ID: "5", Login: "both", Cents: 500},
	}))
	must(st.LinkGitHub("acct-supp", "2", "supp"))
	must(st.LinkGitHub("acct-lone", "9", "lone"))
	must(st.AddVIP(VIP{Kind: vipGitHub, ID: "5", Login: "both", Note: "early tester"}))
	must(st.AddVIP(VIP{Kind: vipAccount, ID: "acct-lone", Note: "no sponsor"}))
	must(st.AddVIP(VIP{Kind: vipAccount, ID: "acct-ended", Until: now.Add(-day).Unix()}))
	st.Close()

	var out bytes.Buffer
	if err := runAdmin(dir, []string{"supporters"}, &out, ""); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 6 {
		t.Fatalf("a header and five people: %q", out.String())
	}
	want := []string{
		"WHO",
		"@both VIP monthly $5 + vip - - early tester",
		"@once VIP one-time $10",
		"acct-lone (@lone) VIP vip - acct-lone no sponsor",
		"@supp Supporter monthly $7 - acct-supp",
		"@backer Backer monthly $2 - -",
	}
	for i, w := range want {
		got := strings.Join(strings.Fields(lines[i]), " ")
		if !strings.HasPrefix(got, w) {
			t.Errorf("line %d: %q, want it to start with %q", i, got, w)
		}
	}
	if !strings.Contains(lines[2], "(10 d)") {
		t.Errorf("the days left of a one-time payment: %q", lines[2])
	}
}

// TestAdminTeam: `relay team add/remove/list/admin`, and the members within the seats in
// `relay supporters`.
func TestAdminTeam(t *testing.T) {
	dir := t.TempDir()
	st, err := OpenStore(filepath.Join(dir, "relay.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SaveSponsors([]Sponsor{{ID: "100", Login: "boss", Cents: 5000}, {ID: "9", Login: "acme", Cents: 7500, Org: true}, {ID: "5", Login: "small", Cents: 1000}}); err != nil {
		t.Fatal(err)
	}
	st.Close()
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/users/drsmith" {
			json.NewEncoder(w).Encode(map[string]any{"id": 4242, "login": "DrSmith"})
			return
		}
		http.NotFound(w, r)
	}))
	defer fake.Close()
	run := func(args ...string) (string, error) {
		var out bytes.Buffer
		err := runAdmin(dir, append([]string{"team"}, args...), &out, fake.URL)
		return out.String(), err
	}
	member := testAccount(t).ID
	if out, err := run("add", "boss", member); err != nil || !strings.Contains(out, "member of the team boss") {
		t.Fatalf("add to the team of a person: %q %v", out, err)
	}
	if _, err := run("add", "small", member); err == nil || !strings.Contains(err.Error(), "Team tier") {
		t.Fatalf("a sponsor below $50 has no team: %v", err)
	}
	if _, err := run("add", "acme", member); err == nil || !strings.Contains(err.Error(), "no admin yet") {
		t.Fatalf("an organization needs its admin first: %v", err)
	}
	if out, err := run("admin", "acme", "drsmith"); err != nil || !strings.Contains(out, "@DrSmith manages the team acme (15 seats now)") {
		t.Fatalf("admin: %q %v", out, err)
	}
	if out, err := run("add", "acme", "drsmith"); err != nil || !strings.Contains(out, "@DrSmith is a member of the team acme") {
		t.Fatalf("add by login: %q %v", out, err)
	}
	if out, err := run("list"); err != nil || !strings.Contains(out, "boss  1/10") || !strings.Contains(out, "acme  1/15") || !strings.Contains(out, member) {
		t.Fatalf("list: %q %v", out, err)
	}
	var out bytes.Buffer
	if err := runAdmin(dir, []string{"supporters"}, &out, ""); err != nil || !strings.Contains(out.String(), "team boss") || !strings.Contains(out.String(), "team acme") {
		t.Fatalf("supporters lists the members: %q %v", out.String(), err)
	}
	if out, err := run("remove", "acme", "drsmith"); err != nil || !strings.Contains(out, "no longer a member") {
		t.Fatalf("remove by login: %q %v", out, err)
	}
}
