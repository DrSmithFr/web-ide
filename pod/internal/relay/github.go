package relay

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// The GitHub link of milestone 5: an account links a GitHub login, and the relay checks that
// login against the people sponsoring the project. No e-mail is ever asked, and the relay never
// keeps a user token: the OAuth token is used once, to read the id of the person who just
// authorized the relay, then dropped.
//
//   - the admin panel (an admin device of the account, which proved possession of its key)
//     asks for the link and opens the URL the relay gives it;
//   - GitHub sends that browser back to /_relay/github/callback with a code the relay exchanges
//     for a token it uses once, on GET /user, and forgets;
//   - the relay stores account → github id;
//   - the sponsor list is read with the maintainer token once an hour (GraphQL: Sponsors has no
//     REST API), and the sponsorship webhook of the Sponsors dashboard (HMAC-checked) moves it
//     between two reads.
//
// A supporter sponsors $5 a month or more (any higher tier gives the same), or paid $5 or more
// once in the last 30 days.

// GitHubConfig holds what the relay needs of the GitHub app. The client id and secret are the
// OAuth app (no scope: the identity of the visitor is enough); the token is the maintainer's,
// it only reads the sponsor list; the webhook secret signs the sponsorship events. They come from
// WEBIDE_GITHUB_CLIENT, _SECRET, _TOKEN and _WEBHOOK (see githubConfig).
type GitHubConfig struct {
	ClientID      string
	ClientSecret  string
	Token         string
	WebhookSecret string
}

// GitHub is the sponsor side of the relay: the link, the sponsor cache, the webhook.
type GitHub struct {
	cfg    GitHubConfig
	store  *Store
	client *http.Client
	// The endpoints, fields so a test can answer them itself.
	authURL  string
	tokenURL string
	apiBase  string

	mu      sync.Mutex
	pending map[string]*githubPending // state nonce → the link that asked for it
}

// Sponsor is one person (or organization) sponsoring the project, as the maintainer's token or
// the webhook tells it: the base keeps one row per sponsor, the sponsorship that counts most.
type Sponsor struct {
	ID      string `json:"id"`
	Login   string `json:"login"`
	Cents   int64  `json:"cents"`   // the monthly price of the tier (the amount, for a one-time payment)
	OneTime bool   `json:"oneTime"` // a one-time payment rather than a monthly sponsorship
	Since   int64  `json:"since"`   // when the sponsorship started (unix seconds)
}

// The supporter rule: $5 a month or more, or $5 or more paid once, which counts for 30 days.
const (
	supporterCents = 500
	oneTimeLasts   = 30 * 24 * time.Hour
)

// supporter applies the rule to one sponsorship.
func (sp Sponsor) supporter(now time.Time) bool {
	if sp.Cents < supporterCents {
		return false
	}
	return !sp.OneTime || now.Sub(time.Unix(sp.Since, 0)) < oneTimeLasts
}

// rank orders the sponsorships of one sponsor: a qualifying monthly one, then a qualifying
// one-time one, then the rest.
func (sp Sponsor) rank(now time.Time) int {
	switch {
	case sp.supporter(now) && !sp.OneTime:
		return 2
	case sp.supporter(now):
		return 1
	}
	return 0
}

// mergeSponsor picks the row a sponsor keeps when a second sponsorship of theirs shows up. A
// monthly sponsorship replaces a monthly one (a tier change); otherwise the higher rank wins, then
// the latest.
func mergeSponsor(old *Sponsor, nw Sponsor, now time.Time) Sponsor {
	if old == nil || (!old.OneTime && !nw.OneTime) {
		return nw
	}
	if ro, rn := old.rank(now), nw.rank(now); ro != rn {
		if ro > rn {
			return *old
		}
		return nw
	}
	if old.Since > nw.Since {
		return *old
	}
	return nw
}

// githubPending is one OAuth dance the relay started: the account it was started for, the
// device that asked, and when. It lives a few minutes, in memory only.
type githubPending struct {
	account string
	device  string
	at      time.Time
}

const githubPendingTTL = 10 * time.Minute

// sponsorsRefreshEvery is how often the sponsor list is read again: the webhook covers what
// happens between two reads.
const sponsorsRefreshEvery = time.Hour

// NewGitHub builds the sponsor side from its config. Without a client id there is no GitHub
// link at all (a self-hosted relay runs without one).
func NewGitHub(cfg GitHubConfig, st *Store) *GitHub {
	if cfg.ClientID == "" || cfg.ClientSecret == "" {
		return nil
	}
	return &GitHub{
		cfg:      cfg,
		store:    st,
		client:   &http.Client{Timeout: 15 * time.Second},
		authURL:  "https://github.com/login/oauth/authorize",
		tokenURL: "https://github.com/login/oauth/access_token",
		apiBase:  "https://api.github.com",
		pending:  map[string]*githubPending{},
	}
}

// migrateGitHub adds the two tables: the link of an account, and the cached sponsor list.
func migrateGitHub(db *sql.DB) error {
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS github (
			account TEXT PRIMARY KEY,
			github_id TEXT NOT NULL,
			login TEXT NOT NULL DEFAULT '',
			created INTEGER NOT NULL DEFAULT 0)`); err != nil {
		return err
	}
	_, err := db.Exec(`CREATE TABLE IF NOT EXISTS sponsors (
			github_id TEXT PRIMARY KEY,
			login TEXT NOT NULL DEFAULT '',
			updated INTEGER NOT NULL DEFAULT 0)`)
	if err != nil {
		return err
	}
	// #85: the tier and the start, for the $5 rule (a row of #29 has neither, and no longer
	// counts until the next read of the list).
	for _, col := range [][2]string{
		{"cents", "INTEGER NOT NULL DEFAULT 0"},
		{"one_time", "INTEGER NOT NULL DEFAULT 0"},
		{"since", "INTEGER NOT NULL DEFAULT 0"},
	} {
		if err := addColumn(db, "sponsors", col[0], col[1]); err != nil {
			return err
		}
	}
	return nil
}

// LinkGitHub stores the GitHub id of an account (one login per account, replaced on a new link).
func (s *Store) LinkGitHub(account, githubID, login string) error {
	if account == "" || githubID == "" {
		return errors.New("relay: a GitHub link needs an account and a GitHub id")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(`INSERT INTO github (account, github_id, login, created) VALUES (?,?,?,?)
			ON CONFLICT (account) DO UPDATE SET github_id=excluded.github_id, login=excluded.login`,
		account, githubID, login, time.Now().Unix())
	return err
}

// GitHubLink is the stored link of an account ("" when none).
func (s *Store) GitHubLink(account string) (githubID, login string, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	err = s.db.QueryRow("SELECT github_id, login FROM github WHERE account=?", account).Scan(&githubID, &login)
	if err == sql.ErrNoRows {
		return "", "", nil
	}
	return githubID, login, err
}

// UnlinkGitHub drops the link of an account: the account is no longer a supporter.
func (s *Store) UnlinkGitHub(account string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec("DELETE FROM github WHERE account=?", account)
	return err
}

// SaveSponsors replaces the cached sponsor list.
func (s *Store) SaveSponsors(list []Sponsor) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	if _, err := tx.Exec("DELETE FROM sponsors"); err != nil {
		tx.Rollback()
		return err
	}
	for _, sp := range list {
		if err := putSponsor(tx, sp); err != nil {
			tx.Rollback()
			return err
		}
	}
	return tx.Commit()
}

func putSponsor(tx *sql.Tx, sp Sponsor) error {
	_, err := tx.Exec(`INSERT INTO sponsors (github_id, login, cents, one_time, since, updated) VALUES (?,?,?,?,?,?)
			ON CONFLICT (github_id) DO UPDATE SET login=excluded.login, cents=excluded.cents,
			one_time=excluded.one_time, since=excluded.since, updated=excluded.updated`,
		sp.ID, sp.Login, sp.Cents, sp.OneTime, sp.Since, time.Now().Unix())
	return err
}

// Sponsor is the row of a GitHub id (nil when none).
func (s *Store) Sponsor(githubID string) (*Sponsor, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return getSponsor(s.db.QueryRow, githubID)
}

func getSponsor(query func(string, ...any) *sql.Row, githubID string) (*Sponsor, error) {
	sp := Sponsor{ID: githubID}
	err := query("SELECT login, cents, one_time, since FROM sponsors WHERE github_id=?", githubID).
		Scan(&sp.Login, &sp.Cents, &sp.OneTime, &sp.Since)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &sp, nil
}

// UpdateSponsor merges one sponsorship a webhook told into the row of its sponsor.
func (s *Store) UpdateSponsor(sp Sponsor) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	old, err := getSponsor(tx.QueryRow, sp.ID)
	if err == nil {
		err = putSponsor(tx, mergeSponsor(old, sp, time.Now()))
	}
	if err != nil {
		tx.Rollback()
		return err
	}
	return tx.Commit()
}

// CancelSponsor drops the monthly sponsorship of a GitHub id; a one-time payment still counts
// its 30 days.
func (s *Store) CancelSponsor(githubID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec("DELETE FROM sponsors WHERE github_id=? AND one_time=0", githubID)
	return err
}

// IsSponsor reports whether a GitHub id is a supporter: on the cached list, under the $5 rule.
func (s *Store) IsSponsor(githubID string) bool {
	if githubID == "" {
		return false
	}
	sp, err := s.Sponsor(githubID)
	return err == nil && sp != nil && sp.supporter(time.Now())
}

// isSupporter is the quota hook of the TURN server: an account is freed from its monthly quota
// when the GitHub login it linked is a sponsor.
func (g *GitHub) isSupporter(account string) bool {
	if g == nil || account == "" {
		return false
	}
	id, _, err := g.store.GitHubLink(account)
	if err != nil || id == "" {
		return false
	}
	return g.store.IsSponsor(id)
}

// startLink is the admin panel asking for the link: the relay hands out a URL the browser opens,
// under a state it remembers for a few minutes, tied to the account of the link that asked.
func (s *Server) githubStart(c *identityConn) (string, error) {
	if c.account == "" {
		return "", errors.New("relay: the link is not registered with an account")
	}
	if c.key == "" {
		// Only a device that proved possession of its key may link an account: the panel of an
		// account is such a device.
		return "", errors.New("relay: only a device of the account can link it")
	}
	g := s.GitHub
	nonce := relayNonce()
	g.mu.Lock()
	for id, p := range g.pending {
		if time.Since(p.at) > githubPendingTTL {
			delete(g.pending, id)
		}
	}
	g.pending[nonce] = &githubPending{account: c.account, device: c.key, at: time.Now()}
	g.mu.Unlock()
	q := url.Values{"state": {nonce}, "account": {c.account}}
	return "/_relay/github/login?" + q.Encode(), nil
}

// serveGitHubLogin is the browser opening that URL: it goes on to GitHub with the state.
func (s *Server) serveGitHubLogin(w http.ResponseWriter, r *http.Request) {
	g := s.GitHub
	state := r.URL.Query().Get("state")
	g.mu.Lock()
	p := g.pending[state]
	g.mu.Unlock()
	if p == nil || time.Since(p.at) > githubPendingTTL {
		http.Error(w, "this link request expired", http.StatusNotFound)
		return
	}
	q := url.Values{}
	if g.cfg.ClientID != "" {
		q.Set("client_id", g.cfg.ClientID)
	}
	q.Set("state", state)
	// No scope: the relay only asks who the visitor is.
	http.Redirect(w, r, g.authURL+"?"+q.Encode(), http.StatusFound)
}

// serveGitHubCallback is where GitHub sends the browser back. The code is exchanged, the id of
// the person read, the link stored, and the token dropped.
func (s *Server) serveGitHubCallback(w http.ResponseWriter, r *http.Request) {
	g := s.GitHub
	state := r.URL.Query().Get("state")
	code := r.URL.Query().Get("code")
	g.mu.Lock()
	p := g.pending[state]
	if p != nil {
		delete(g.pending, state)
	}
	g.mu.Unlock()
	if p == nil || time.Since(p.at) > githubPendingTTL {
		http.Error(w, "this link request expired", http.StatusNotFound)
		return
	}
	if code == "" {
		http.Error(w, "GitHub did not answer", http.StatusBadGateway)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	token, err := g.exchange(ctx, code)
	if err != nil {
		log.Printf("relay: github: %v", err)
		http.Error(w, "GitHub did not answer", http.StatusBadGateway)
		return
	}
	id, login, err := g.user(ctx, token)
	if err != nil {
		log.Printf("relay: github: %v", err)
		http.Error(w, "GitHub did not answer", http.StatusBadGateway)
		return
	}
	if err := g.store.LinkGitHub(p.account, id, login); err != nil {
		log.Printf("relay: github: %v", err)
		http.Error(w, "the link could not be stored", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	// A small window that tells the panel and closes itself: the relay never sees a token again.
	fmt.Fprintf(w, `<!doctype html><meta charset=utf-8><title>GitHub linked</title>
<p>@%s</p><script>
try { localStorage.setItem('webide.github.done', %q) } catch {}
if (window.opener) window.opener.postMessage({type:'webide.github', login:%q}, location.origin)
setTimeout(() => window.close(), 400)
</script>`, login, id, login)
}

// exchange trades the code for a token. The token is used once, by user, and never stored.
func (g *GitHub) exchange(ctx context.Context, code string) (string, error) {
	form := url.Values{
		"client_id":     {g.cfg.ClientID},
		"client_secret": {g.cfg.ClientSecret},
		"code":          {code},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, g.tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	res, err := g.client.Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	var out struct {
		AccessToken string `json:"access_token"`
		Error       string `json:"error_description"`
	}
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		return "", err
	}
	if out.AccessToken == "" {
		return "", fmt.Errorf("github: no token (%s)", out.Error)
	}
	return out.AccessToken, nil
}

// user reads the identity the token belongs to: the id stored on the account, the login shown
// in the panel.
func (g *GitHub) user(ctx context.Context, token string) (id, login string, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, g.apiBase+"/user", nil)
	if err != nil {
		return "", "", err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	res, err := g.client.Do(req)
	if err != nil {
		return "", "", err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("github: /user answered %d", res.StatusCode)
	}
	var out struct {
		ID    int64  `json:"id"`
		Login string `json:"login"`
	}
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		return "", "", err
	}
	if out.ID == 0 {
		return "", "", errors.New("github: /user gave no id")
	}
	return strconv64(out.ID), out.Login, nil
}

// sponsorsQuery lists every sponsorship the maintainer ever had, private ones too: the one-time
// payments are not "active", and a past monthly sponsor is told apart by isActive.
const sponsorsQuery = `query($after: String) {
  viewer {
    sponsorshipsAsMaintainer(first: 100, after: $after, activeOnly: false, includePrivate: true) {
      pageInfo { hasNextPage endCursor }
      nodes {
        isActive
        isOneTimePayment
        createdAt
        tier { monthlyPriceInCents }
        sponsorEntity {
          ... on User { databaseId login }
          ... on Organization { databaseId login }
        }
      }
    }
  }
}`

// refreshSponsors reads the sponsor list with the maintainer token, page by page, and replaces
// the cached one: one row per sponsor, the sponsorship that counts most.
func (g *GitHub) refreshSponsors(ctx context.Context) error {
	if g.cfg.Token == "" {
		return errors.New("github: no maintainer token, the sponsor list is not read")
	}
	now := time.Now()
	byID := map[string]Sponsor{}
	after := ""
	for page := 0; page < 100; page++ {
		vars := map[string]any{"after": nil}
		if after != "" {
			vars["after"] = after
		}
		body, _ := json.Marshal(map[string]any{"query": sponsorsQuery, "variables": vars})
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, g.apiBase+"/graphql", strings.NewReader(string(body)))
		if err != nil {
			return err
		}
		req.Header.Set("Authorization", "Bearer "+g.cfg.Token)
		req.Header.Set("Content-Type", "application/json")
		res, err := g.client.Do(req)
		if err != nil {
			return err
		}
		var out struct {
			Data struct {
				Viewer struct {
					Sponsorships struct {
						PageInfo struct {
							HasNextPage bool   `json:"hasNextPage"`
							EndCursor   string `json:"endCursor"`
						} `json:"pageInfo"`
						Nodes []struct {
							IsActive  bool      `json:"isActive"`
							OneTime   bool      `json:"isOneTimePayment"`
							CreatedAt time.Time `json:"createdAt"`
							Tier      *struct {
								Cents int64 `json:"monthlyPriceInCents"`
							} `json:"tier"`
							Entity struct {
								ID    int64  `json:"databaseId"`
								Login string `json:"login"`
							} `json:"sponsorEntity"`
						} `json:"nodes"`
					} `json:"sponsorshipsAsMaintainer"`
				} `json:"viewer"`
			} `json:"data"`
			Errors []struct {
				Message string `json:"message"`
			} `json:"errors"`
		}
		err = json.NewDecoder(res.Body).Decode(&out)
		res.Body.Close()
		if res.StatusCode != http.StatusOK {
			return fmt.Errorf("github: the sponsor list answered %d", res.StatusCode)
		}
		if err != nil {
			return err
		}
		if len(out.Errors) > 0 {
			return fmt.Errorf("github: the sponsor list: %s", out.Errors[0].Message)
		}
		list := out.Data.Viewer.Sponsorships
		for _, n := range list.Nodes {
			// A past monthly sponsor no longer counts; a one-time payment counts by its date.
			if n.Entity.ID == 0 || (!n.OneTime && !n.IsActive) {
				continue
			}
			sp := Sponsor{ID: strconv64(n.Entity.ID), Login: n.Entity.Login, OneTime: n.OneTime, Since: n.CreatedAt.Unix()}
			if n.Tier != nil {
				sp.Cents = n.Tier.Cents
			}
			if old, ok := byID[sp.ID]; ok {
				sp = mergeSponsor(&old, sp, now)
			}
			byID[sp.ID] = sp
		}
		if !list.PageInfo.HasNextPage || list.PageInfo.EndCursor == "" {
			break
		}
		after = list.PageInfo.EndCursor
	}
	sponsors := make([]Sponsor, 0, len(byID))
	for _, sp := range byID {
		sponsors = append(sponsors, sp)
	}
	return g.store.SaveSponsors(sponsors)
}

// refreshLoop reads the sponsor list again every hour.
func (g *GitHub) refreshLoop(ctx context.Context) {
	if g.cfg.Token == "" {
		return
	}
	for {
		callCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		if err := g.refreshSponsors(callCtx); err != nil {
			log.Printf("relay: github sponsors: %v", err)
		}
		cancel()
		select {
		case <-ctx.Done():
			return
		case <-time.After(sponsorsRefreshEvery):
		}
	}
}

// serveGitHubWebhook takes the sponsorship events of the Sponsors dashboard: they move the
// sponsor list between two reads, one row at a time (the list itself is only replaced by a full
// read). The body is checked against the webhook secret (HMAC SHA-256, the X-Hub-Signature-256
// header), so only GitHub can post one.
func (s *Server) serveGitHubWebhook(w http.ResponseWriter, r *http.Request) {
	g := s.GitHub
	if g.cfg.WebhookSecret == "" {
		http.Error(w, "no webhook secret", http.StatusNotFound)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		http.Error(w, "unreadable body", http.StatusBadRequest)
		return
	}
	if !validWebhookSignature(g.cfg.WebhookSecret, r.Header.Get("X-Hub-Signature-256"), body) {
		http.Error(w, "bad signature", http.StatusUnauthorized)
		return
	}
	if r.Header.Get("X-GitHub-Event") == "ping" {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	var ev struct {
		Action      string `json:"action"`
		Sponsorship struct {
			CreatedAt time.Time `json:"created_at"`
			Sponsor   struct {
				ID    int64  `json:"id"`
				Login string `json:"login"`
			} `json:"sponsor"`
			Tier struct {
				Cents   int64 `json:"monthly_price_in_cents"`
				OneTime bool  `json:"is_one_time"`
			} `json:"tier"`
		} `json:"sponsorship"`
	}
	if err := json.Unmarshal(body, &ev); err != nil {
		http.Error(w, "not a sponsorship event", http.StatusBadRequest)
		return
	}
	sp := ev.Sponsorship
	if sp.Sponsor.ID == 0 {
		http.Error(w, "no sponsor in the event", http.StatusBadRequest)
		return
	}
	id := strconv64(sp.Sponsor.ID)
	switch ev.Action {
	case "created", "tier_changed", "edited":
		// tier_changed comes when the change takes effect (pending_tier_change before it).
		err = g.store.UpdateSponsor(Sponsor{ID: id, Login: sp.Sponsor.Login, Cents: sp.Tier.Cents,
			OneTime: sp.Tier.OneTime, Since: sp.CreatedAt.Unix()})
	case "cancelled":
		// Sent at the end of the paid period (pending_cancellation before it): access lasts until then.
		err = g.store.CancelSponsor(id)
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// validWebhookSignature checks the GitHub HMAC of a body.
func validWebhookSignature(secret, header string, body []byte) bool {
	const prefix = "sha256="
	if !strings.HasPrefix(header, prefix) {
		return false
	}
	want, err := hex.DecodeString(strings.TrimPrefix(header, prefix))
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return hmac.Equal(mac.Sum(nil), want)
}

func strconv64(n int64) string { return fmt.Sprintf("%d", n) }
