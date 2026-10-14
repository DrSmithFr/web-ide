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
//   - the sponsor list is read with the maintainer token once an hour, and a sponsorship webhook
//     (HMAC-checked) moves it between two reads.

// GitHubConfig holds what the relay needs of the GitHub app. The client id and secret are the
// OAuth app (no scope: the identity of the visitor is enough); the token is the maintainer's,
// it only reads the sponsor list; the webhook secret signs the sponsorship events.
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

	mu       sync.Mutex
	pending  map[string]*githubPending // state nonce → the link that asked for it
	sponsors map[string]Sponsor        // by GitHub user id
}

// Sponsor is one person sponsoring the project, as the maintainer's token lists them.
type Sponsor struct {
	ID    string `json:"id"`
	Login string `json:"login"`
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
		sponsors: map[string]Sponsor{},
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
	return err
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
		if _, err := tx.Exec("INSERT INTO sponsors (github_id, login, updated) VALUES (?,?,?)",
			sp.ID, sp.Login, time.Now().Unix()); err != nil {
			tx.Rollback()
			return err
		}
	}
	return tx.Commit()
}

// IsSponsor reports whether a GitHub id is on the cached sponsor list.
func (s *Store) IsSponsor(githubID string) bool {
	if githubID == "" {
		return false
	}
	var id string
	err := s.db.QueryRow("SELECT github_id FROM sponsors WHERE github_id=?", githubID).Scan(&id)
	return err == nil
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

// refreshSponsors reads the sponsor list with the maintainer token.
func (g *GitHub) refreshSponsors(ctx context.Context) error {
	if g.cfg.Token == "" {
		return errors.New("github: no maintainer token, the sponsor list is not read")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, g.apiBase+"/user/sponsorships_as_maintainer?per_page=100", nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+g.cfg.Token)
	req.Header.Set("Accept", "application/json")
	res, err := g.client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("github: the sponsor list answered %d", res.StatusCode)
	}
	var raw []struct {
		TwitterUsername *string `json:"twitter_username"`
		Name            string  `json:"name"`
		AccountUser     struct {
			ID    int64  `json:"id"`
			Login string `json:"login"`
		} `json:"accountUser"`
	}
	if err := json.NewDecoder(res.Body).Decode(&raw); err != nil {
		return err
	}
	list := make([]Sponsor, 0, len(raw))
	for _, s := range raw {
		if s.AccountUser.ID == 0 {
			continue
		}
		list = append(list, Sponsor{ID: strconv64(s.AccountUser.ID), Login: s.AccountUser.Login})
	}
	g.mu.Lock()
	g.sponsors = map[string]Sponsor{}
	for _, s := range list {
		g.sponsors[s.ID] = s
	}
	g.mu.Unlock()
	return g.store.SaveSponsors(list)
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

// serveGitHubWebhook takes the sponsorship events of the GitHub app: they move the sponsor list
// between two reads. The body is checked against the webhook secret (HMAC SHA-256, the
// X-Hub-Signature-256 header), so only GitHub can post one.
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
	var ev struct {
		Action  string `json:"action"`
		Sponsor struct {
			ID    int64  `json:"id"`
			Login string `json:"login"`
		} `json:"sponsor"`
	}
	if err := json.Unmarshal(body, &ev); err != nil {
		http.Error(w, "not a sponsorship event", http.StatusBadRequest)
		return
	}
	if ev.Sponsor.ID == 0 {
		http.Error(w, "no sponsor in the event", http.StatusBadRequest)
		return
	}
	id := strconv64(ev.Sponsor.ID)
	g.mu.Lock()
	switch ev.Action {
	case "sponsorship_created":
		g.sponsors[id] = Sponsor{ID: id, Login: ev.Sponsor.Login}
	case "sponsorship_cancelled", "sponsorship_edited":
		if ev.Action == "sponsorship_cancelled" {
			delete(g.sponsors, id)
		}
	default:
		g.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
		return
	}
	list := make([]Sponsor, 0, len(g.sponsors))
	for _, sp := range g.sponsors {
		list = append(list, sp)
	}
	g.mu.Unlock()
	if err := g.store.SaveSponsors(list); err != nil {
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
