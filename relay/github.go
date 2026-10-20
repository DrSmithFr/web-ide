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
	"html"
	"io"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/DrSmithFr/web-ide/lib/i18n"
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
// What a sponsorship gives is a level (level.go): Backer, Supporter or VIP, by the tier grid.

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
	// changed is told the accounts whose level may have moved (a read of the list):
	// the relay tells their pods at once, else they show the old quota until they reconnect.
	changed func(accounts []string)
}

// Sponsor is one person sponsoring the project, as the maintainer's token or
// the webhook tells it: the base keeps one row per sponsor, the sponsorship that counts most.
type Sponsor struct {
	ID      string `json:"id"`
	Login   string `json:"login"`
	Cents   int64  `json:"cents"`   // the monthly price of the tier (the amount, for a one-time payment)
	OneTime bool   `json:"oneTime"` // a one-time payment rather than a monthly sponsorship
	Since   int64  `json:"since"`   // when the sponsorship started (unix seconds)
	Org     bool   `json:"org"`     // an organization (#88): it links no account, its seats make a team
}

// mergeSponsor picks the row a sponsor keeps when a second sponsorship of theirs shows up. A
// monthly sponsorship replaces a monthly one (a tier change); otherwise the best live level wins
// (then the one that lasts longer), then the latest.
func mergeSponsor(old *Sponsor, nw Sponsor, now time.Time) Sponsor {
	if old == nil || (!old.OneTime && !nw.OneTime) {
		return nw
	}
	lo, uo := levelOf(*old, now)
	ln, un := levelOf(nw, now)
	if lo != ln || uo != un {
		if better(lo, uo, ln, un) {
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
	purpose string // "" links the account; "team" proves the admin of an organization (#88)
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
		{"org", "INTEGER NOT NULL DEFAULT 0"}, // #88
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

// LinkedAccounts lists the accounts linked to a GitHub login, or to the given GitHub id only.
func (s *Store) LinkedAccounts(githubID string) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	q, args := "SELECT account FROM github", []any{}
	if githubID != "" {
		q, args = q+" WHERE github_id=?", []any{githubID}
	}
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var a string
		if err := rows.Scan(&a); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// UnlinkGitHub drops the link of an account: its sponsorship no longer counts.
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
	_, err := tx.Exec(`INSERT INTO sponsors (github_id, login, cents, one_time, since, org, updated) VALUES (?,?,?,?,?,?,?)
			ON CONFLICT (github_id) DO UPDATE SET login=excluded.login, cents=excluded.cents,
			one_time=excluded.one_time, since=excluded.since, org=excluded.org, updated=excluded.updated`,
		sp.ID, sp.Login, sp.Cents, sp.OneTime, sp.Since, sp.Org, time.Now().Unix())
	return err
}

// Sponsor is the row of a GitHub id (nil when none).
func (s *Store) Sponsor(githubID string) (*Sponsor, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return getSponsor(s.db.QueryRow, githubID)
}

// sponsors is the whole cached list.
func (s *Store) sponsors() ([]Sponsor, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rows, err := s.db.Query("SELECT github_id, login, cents, one_time, since, org FROM sponsors")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Sponsor
	for rows.Next() {
		var sp Sponsor
		if err := rows.Scan(&sp.ID, &sp.Login, &sp.Cents, &sp.OneTime, &sp.Since, &sp.Org); err != nil {
			return nil, err
		}
		out = append(out, sp)
	}
	return out, rows.Err()
}

func getSponsor(query func(string, ...any) *sql.Row, githubID string) (*Sponsor, error) {
	sp := Sponsor{ID: githubID}
	err := query("SELECT login, cents, one_time, since, org FROM sponsors WHERE github_id=?", githubID).
		Scan(&sp.Login, &sp.Cents, &sp.OneTime, &sp.Since, &sp.Org)
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
// its days.
func (s *Store) CancelSponsor(githubID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec("DELETE FROM sponsors WHERE github_id=? AND one_time=0", githubID)
	return err
}

// startLink is the admin panel asking for the link: the relay hands out a URL the browser opens,
// under a state it remembers for a few minutes, tied to the account of the link that asked.
func (s *Server) githubStart(c *identityConn, purpose string) (string, error) {
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
	if purpose != purposeTeam {
		purpose = ""
	}
	g.pending[nonce] = &githubPending{account: c.account, device: c.key, at: time.Now(), purpose: purpose}
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
	// No scope: the relay only asks who the visitor is — but for the team of an organization,
	// whose admin it must check (read:org, asked for this alone).
	if p.purpose == purposeTeam {
		q.Set("scope", "read:org")
	}
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
	if p.purpose == purposeTeam {
		s.serveTeamDone(ctx, w, r, p, token, id, login)
		return
	}
	if err := g.store.LinkGitHub(p.account, id, login); err != nil {
		log.Printf("relay: github: %v", err)
		http.Error(w, "the link could not be stored", http.StatusInternalServerError)
		return
	}
	s.tellQuota([]string{p.account})
	// A small window that thanks, tells the panel (github-done.js) and closes on its button: the
	// relay never sees a token again.
	words := githubDoneWords(r.Header.Get("Accept-Language"), s.store.Level(p.account, time.Now()).Level)
	writeDonePage(w, words.lang, words.title, id, login, fmt.Sprintf(words.linked, "@"+login), words.thanks, words.close)
}

// writeDonePage writes the window GitHub sends back to: a title, two lines, and a button that
// closes it (github-done.js tells the panel that opened it).
func writeDonePage(w http.ResponseWriter, lang, title, id, login, line, muted, close string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprintf(w, `<!doctype html><html lang="%s"><meta charset=utf-8>
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>%s</title><link rel="stylesheet" href="/_relay/bootstrap.css">
<main class="card" id="done" data-id="%s" data-login="%s">
<div class="brand"><div class="name">%s</div></div>
<p>%s</p><p class="muted">%s</p>
<button class="btn primary" type="button" id="close">%s</button>
</main><script type="module" src="/_relay/github/done.js"></script>`,
		lang, html.EscapeString(title), html.EscapeString(id), html.EscapeString(login),
		html.EscapeString(title), html.EscapeString(line), html.EscapeString(muted), html.EscapeString(close))
}

// githubDoneWords are the words of the window GitHub sends back to, in the language of the
// browser: the relay pages follow it.
type doneWords struct{ lang, title, linked, thanks, thanksBacker, thanksSupporter, close string }

// thanksSupporter names the level (Supporter or VIP, the words of the badge, never translated).
var doneCatalog = map[string]doneWords{
	"en": {"en", "GitHub linked", "This account is now linked to %s.",
		"Thanks for linking it! Sponsoring Web IDE on GitHub raises or lifts the monthly TURN quota of the relay.",
		"Thanks for backing Web IDE ♥ As a Backer, your monthly TURN quota on the relay is raised.",
		"Thanks for supporting Web IDE, hugs ♥ As a %s, the relay no longer counts your TURN traffic.", "Close"},
	"fr": {"fr", "GitHub lié", "Ce compte est maintenant lié à %s.",
		"Merci de l’avoir lié ! Soutenir Web IDE sur GitHub augmente ou lève le quota TURN mensuel du relay.",
		"Merci de soutenir Web IDE ♥ En Backer, votre quota TURN mensuel sur le relay est augmenté.",
		"Merci de soutenir Web IDE, des bisous ♥ En %s, le relay ne compte plus votre trafic TURN.", "Fermer"},
	"es": {"es", "GitHub vinculado", "Esta cuenta ya está vinculada a %s.",
		"¡Gracias por vincularla! Patrocinar Web IDE en GitHub aumenta o elimina la cuota mensual de TURN del relay.",
		"Gracias por respaldar Web IDE ♥ Como Backer, tu cuota mensual de TURN en el relay aumenta.",
		"Gracias por apoyar Web IDE, un abrazo ♥ Como %s, el relay ya no cuenta tu tráfico TURN.", "Cerrar"},
}

func githubDoneWords(acceptLanguage string, lv Level) doneWords {
	w, ok := doneCatalog[i18n.Detect(acceptLanguage)]
	if !ok {
		w = doneCatalog["en"]
	}
	switch {
	case lv == LevelBacker:
		w.thanks = w.thanksBacker
	case lv >= LevelSupporter:
		w.thanks = fmt.Sprintf(w.thanksSupporter, levelBadgeWords[lv])
	}
	return w
}

// levelBadgeWords are the words of the badges, the same in every language.
var levelBadgeWords = map[Level]string{LevelBacker: "Backer", LevelSupporter: "Supporter", LevelVIP: "VIP"}

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
// payments are not "active", and a past monthly sponsor is told apart by isActive. An
// organization (#88: its seats make a team) is read by its login only: its databaseId would need
// the read:org scope on the token, so its id comes from the public /orgs/<login> (orgID).
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
          __typename
          ... on User { databaseId login }
          ... on Organization { login }
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
	// The ids of the organizations already known: one call to /orgs per new one only.
	orgIDs := map[string]string{}
	if known, err := g.store.sponsors(); err == nil {
		for _, sp := range known {
			if sp.Org {
				orgIDs[strings.ToLower(sp.Login)] = sp.ID
			}
		}
	}
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
								Type  string `json:"__typename"`
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
			if !n.OneTime && !n.IsActive {
				continue
			}
			org := n.Entity.Type == "Organization" && n.Entity.Login != ""
			if org && n.Entity.ID == 0 {
				id, ok := orgIDs[strings.ToLower(n.Entity.Login)]
				if !ok {
					var err error
					if id, err = g.orgID(ctx, n.Entity.Login); err != nil {
						log.Printf("relay: github sponsors: %v", err)
						continue
					}
					orgIDs[strings.ToLower(n.Entity.Login)] = id
				}
				n.Entity.ID, _ = strconv.ParseInt(id, 10, 64)
			}
			if n.Entity.ID == 0 {
				continue
			}
			sp := Sponsor{ID: strconv64(n.Entity.ID), Login: n.Entity.Login, OneTime: n.OneTime, Since: n.CreatedAt.Unix(), Org: org}
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
	if err := g.store.SaveSponsors(sponsors); err != nil {
		return err
	}
	if g.changed != nil {
		if accounts, err := g.store.LinkedAccounts(""); err == nil {
			g.changed(accounts)
		}
	}
	return nil
}

// orgID reads the id of an organization from its public profile, with the maintainer token (its
// rate is 5000 calls an hour, 60 without).
func (g *GitHub) orgID(ctx context.Context, login string) (string, error) {
	id, _, err := githubAccount(ctx, g.client, g.apiBase, "orgs", login, g.cfg.Token)
	return id, err
}

// githubAccount reads the id and the exact login of a public GitHub user or organization
// (kind "users" or "orgs"), with a token when one is given.
func githubAccount(ctx context.Context, client *http.Client, apiBase, kind, login, token string) (id, exact string, err error) {
	login = strings.TrimPrefix(login, "@")
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiBase+"/"+kind+"/"+url.PathEscape(login), nil)
	if err != nil {
		return "", "", err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "web-ide-relay")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	res, err := client.Do(req)
	if err != nil {
		return "", "", fmt.Errorf("github: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode == http.StatusNotFound {
		return "", "", i18n.Errorf("no GitHub account %q", login)
	}
	if res.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("github: /%s/%s answered %d", kind, login, res.StatusCode)
	}
	var out struct {
		ID    int64  `json:"id"`
		Login string `json:"login"`
	}
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		return "", "", err
	}
	if out.ID == 0 {
		return "", "", fmt.Errorf("github: /%s/%s gave no id", kind, login)
	}
	return strconv64(out.ID), out.Login, nil
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
				Type  string `json:"type"` // "User" or "Organization"
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
			OneTime: sp.Tier.OneTime, Since: sp.CreatedAt.Unix(), Org: sp.Sponsor.Type == "Organization"})
	case "cancelled":
		// Sent at the end of the paid period (pending_cancellation before it): access lasts until then.
		err = g.store.CancelSponsor(id)
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if accounts, err := g.store.LinkedAccounts(id); err == nil {
		s.tellQuota(accounts)
	}
	w.WriteHeader(http.StatusNoContent)
}

// tellQuota sends the TURN month to the pods of these accounts: a level that moved (a
// GitHub link, a sponsorship, the VIP list) shows in their Settings at once, not at their next
// connection.
func (s *Server) tellQuota(accounts []string) {
	if s.turn == nil || len(accounts) == 0 {
		return
	}
	want := map[string]bool{}
	for _, a := range accounts {
		want[a] = true
	}
	s.mu.Lock()
	var pods []*podConn
	for _, pc := range s.pods {
		if pc.account != "" && want[pc.account] {
			pods = append(pods, pc)
		}
	}
	s.mu.Unlock()
	for _, pc := range pods {
		_ = pc.send(sigMsg{Type: "quota", Quota: s.turn.QuotaState(pc.account)})
	}
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
