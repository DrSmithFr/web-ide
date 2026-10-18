package relay

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"text/tabwriter"
	"time"
)

// The admin commands of the relay (#87): the VIP list and the people at Backer level or above,
// read and written in relay.db directly. They open the base only (no listener), so they run next
// to the live relay, which feels the change at its next look (watchLevels).
//
//	web-ide-pod relay vip add <login|account> [-note …] [-days N | -until YYYY-MM-DD]
//	web-ide-pod relay vip remove <login|account>
//	web-ide-pod relay vip list
//	web-ide-pod relay supporters

// IsAdminCommand reports whether the words after "relay" are an admin command, not the server.
func IsAdminCommand(args []string) bool {
	return len(args) > 0 && (args[0] == "vip" || args[0] == "supporters")
}

// admin is one run of a command: the base, where it writes, and the GitHub API a login is
// resolved with (a field, so a test answers it).
type admin struct {
	st      *Store
	out     io.Writer
	apiBase string
	client  *http.Client
	now     time.Time
}

// RunAdmin runs an admin command against the relay.db of dataDir ("" → ~/.web-ide-relay).
func RunAdmin(dataDir string, args []string, out io.Writer) error {
	return runAdmin(dataDir, args, out, "https://api.github.com")
}

func runAdmin(dataDir string, args []string, out io.Writer, apiBase string) error {
	if dataDir == "" {
		home, _ := os.UserHomeDir()
		dataDir = filepath.Join(home, ".web-ide-relay")
	}
	path := filepath.Join(dataDir, "relay.db")
	// OpenStore would make an empty base: a mistyped -data must say so instead.
	if _, err := os.Stat(path); err != nil {
		return fmt.Errorf("no relay base at %s (-data names the data folder of the relay)", path)
	}
	st, err := OpenStore(path)
	if err != nil {
		return err
	}
	defer st.Close()
	a := &admin{st: st, out: out, apiBase: apiBase, client: &http.Client{Timeout: 15 * time.Second}, now: time.Now()}
	switch {
	case len(args) >= 2 && args[0] == "vip" && args[1] == "add":
		return a.vipAdd(args[2:])
	case len(args) >= 2 && args[0] == "vip" && args[1] == "remove":
		return a.vipRemove(args[2:])
	case len(args) >= 2 && args[0] == "vip" && args[1] == "list":
		return a.vipList()
	case len(args) == 1 && args[0] == "supporters":
		return a.supporters()
	}
	return errors.New(adminUsage)
}

const adminUsage = `usage:
  web-ide-pod [-data DIR] relay vip add <login|account> [-note TEXT] [-days N | -until YYYY-MM-DD]
  web-ide-pod [-data DIR] relay vip remove <login|account>
  web-ide-pod [-data DIR] relay vip list
  web-ide-pod [-data DIR] relay supporters`

// isAccountID tells an account id from a GitHub login: an account id is the base64url of an
// Ed25519 key (43 characters, 32 bytes), and a login has 39 characters at most.
func isAccountID(s string) bool {
	if len(s) != 43 {
		return false
	}
	b, err := base64.RawURLEncoding.DecodeString(s)
	return err == nil && len(b) == 32
}

// parseWho reads the flags around the one positional word: `add bob -days 90` and
// `add -days 90 bob` alike.
func parseWho(fs *flag.FlagSet, args []string) (string, error) {
	fs.SetOutput(io.Discard)
	if err := fs.Parse(args); err != nil {
		return "", err
	}
	rest := fs.Args()
	if len(rest) == 0 {
		return "", errors.New("a GitHub login or an account id is needed")
	}
	who := rest[0]
	if err := fs.Parse(rest[1:]); err != nil {
		return "", err
	}
	if fs.NArg() > 0 {
		return "", fmt.Errorf("unexpected %q", fs.Arg(0))
	}
	return who, nil
}

func (a *admin) vipAdd(args []string) error {
	fs := flag.NewFlagSet("vip add", flag.ContinueOnError)
	note := fs.String("note", "", "a note for the admin only")
	days := fs.Int("days", 0, "the VIP level ends after N days")
	until := fs.String("until", "", "the VIP level ends on this day (YYYY-MM-DD)")
	who, err := parseWho(fs, args)
	if err != nil {
		return fmt.Errorf("%v\n%s", err, adminUsage)
	}
	v := VIP{Note: *note}
	switch {
	case *days > 0 && *until != "":
		return errors.New("-days or -until, not both")
	case *days < 0:
		return errors.New("-days is a number of days")
	case *days > 0:
		v.Until = a.now.Add(time.Duration(*days) * day).Unix()
	case *until != "":
		d, err := time.ParseInLocation("2006-01-02", *until, time.Local)
		if err != nil {
			return fmt.Errorf("-until %q: a day as YYYY-MM-DD", *until)
		}
		// The whole day counts: the level ends at the start of the next one.
		v.Until = d.AddDate(0, 0, 1).Unix()
		if v.Until <= a.now.Unix() {
			return fmt.Errorf("-until %s is already past", *until)
		}
	}
	if isAccountID(who) {
		v.Kind, v.ID = vipAccount, who
	} else {
		id, login, err := a.githubUser(who)
		if err != nil {
			return err
		}
		v.Kind, v.ID, v.Login = vipGitHub, id, login
	}
	if err := a.st.AddVIP(v); err != nil {
		return err
	}
	end := "no end"
	if v.Until > 0 {
		end = "until " + a.endText(v.Until)
	}
	fmt.Fprintf(a.out, "%s is a VIP, %s\n", vipName(v), end)
	return nil
}

func (a *admin) vipRemove(args []string) error {
	fs := flag.NewFlagSet("vip remove", flag.ContinueOnError)
	who, err := parseWho(fs, args)
	if err != nil {
		return fmt.Errorf("%v\n%s", err, adminUsage)
	}
	kind, id := vipAccount, who
	if !isAccountID(who) {
		// The row keeps the login it was added with: no network for a login on the list.
		if v := a.st.vipByLogin(who); v != nil {
			kind, id = vipGitHub, v.ID
		} else {
			gid, _, err := a.githubUser(who)
			if err != nil {
				return err
			}
			kind, id = vipGitHub, gid
		}
	}
	ok, err := a.st.RemoveVIP(kind, id)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("%s is not on the VIP list", who)
	}
	fmt.Fprintf(a.out, "%s is no longer a VIP\n", who)
	return nil
}

func (a *admin) vipList() error {
	list, err := a.st.VIPs()
	if err != nil {
		return err
	}
	w := tabwriter.NewWriter(a.out, 0, 2, 2, ' ', 0)
	fmt.Fprintln(w, "VIP\tKIND\tEND\tNOTE")
	for _, v := range list {
		end := "-"
		if v.Until > 0 {
			end = a.endText(v.Until)
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", vipName(v), v.Kind, end, v.Note)
	}
	return w.Flush()
}

// supporterRow is one person of `relay supporters`: a GitHub id (sponsor or VIP by login), or an
// account on the VIP list by its id.
type supporterRow struct {
	who      string
	level    Level
	until    int64
	sources  []string
	accounts []string
	note     string
}

func (a *admin) supporters() error {
	rows := map[string]*supporterRow{} // by "github:<id>" or "account:<id>"
	row := func(key, who string) *supporterRow {
		r := rows[key]
		if r == nil {
			r = &supporterRow{who: who}
			rows[key] = r
		}
		return r
	}
	lift := func(r *supporterRow, lv Level, until int64) {
		if better(lv, until, r.level, r.until) {
			r.level, r.until = lv, until
		}
	}
	sponsors, err := a.st.sponsors()
	if err != nil {
		return err
	}
	for _, sp := range sponsors {
		lv, until := levelOf(sp, a.now)
		if lv == LevelFree {
			continue
		}
		r := row("github:"+sp.ID, "@"+sp.Login)
		lift(r, lv, until)
		kind := "monthly"
		if sp.OneTime {
			kind = "one-time"
		}
		r.sources = append(r.sources, fmt.Sprintf("%s $%s", kind, dollars(sp.Cents)))
	}
	for _, v := range a.st.liveVIPs(a.now) {
		var r *supporterRow
		if v.Kind == vipGitHub {
			r = row("github:"+v.ID, "@"+v.Login)
		} else {
			who := v.ID
			if _, login, _ := a.st.GitHubLink(v.ID); login != "" {
				who = v.ID + " (@" + login + ")"
			}
			r = row("account:"+v.ID, who)
			r.accounts = []string{v.ID}
		}
		lift(r, LevelVIP, v.Until)
		r.sources = append(r.sources, "vip")
		r.note = v.Note
	}
	list := make([]*supporterRow, 0, len(rows))
	for key, r := range rows {
		if id, ok := strings.CutPrefix(key, "github:"); ok {
			r.accounts, _ = a.st.LinkedAccounts(id)
		}
		list = append(list, r)
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].level != list[j].level {
			return list[i].level > list[j].level
		}
		return strings.ToLower(list[i].who) < strings.ToLower(list[j].who)
	})
	w := tabwriter.NewWriter(a.out, 0, 2, 2, ' ', 0)
	fmt.Fprintln(w, "WHO\tLEVEL\tSOURCE\tEND\tACCOUNTS\tNOTE")
	for _, r := range list {
		end := "-"
		if r.until > 0 {
			end = a.endText(r.until)
		}
		accounts := strings.Join(r.accounts, ", ")
		if accounts == "" {
			accounts = "-"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n", r.who, levelBadgeWords[r.level], strings.Join(r.sources, " + "), end, accounts, r.note)
	}
	return w.Flush()
}

// endText is an end the way the admin reads it: the day, and the days left (or ended).
func (a *admin) endText(until int64) string {
	t := time.Unix(until, 0)
	left := t.Sub(a.now)
	if left <= 0 {
		return t.Format("2006-01-02") + " (ended)"
	}
	return fmt.Sprintf("%s (%d d)", t.Format("2006-01-02"), int((left+day-1)/day))
}

func vipName(v VIP) string {
	if v.Login != "" {
		return "@" + v.Login
	}
	return v.ID
}

func dollars(cents int64) string {
	if cents%100 == 0 {
		return fmt.Sprint(cents / 100)
	}
	return fmt.Sprintf("%d.%02d", cents/100, cents%100)
}

// githubUser resolves a login to its GitHub id and exact login (no token: /users is public).
func (a *admin) githubUser(login string) (id, exact string, err error) {
	login = strings.TrimPrefix(login, "@")
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, a.apiBase+"/users/"+url.PathEscape(login), nil)
	if err != nil {
		return "", "", err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "web-ide-relay")
	res, err := a.client.Do(req)
	if err != nil {
		return "", "", fmt.Errorf("github: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode == http.StatusNotFound {
		return "", "", fmt.Errorf("no GitHub user %q (an account id is 43 characters)", login)
	}
	if res.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("github: /users/%s answered %d", login, res.StatusCode)
	}
	var out struct {
		ID    int64  `json:"id"`
		Login string `json:"login"`
	}
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		return "", "", err
	}
	if out.ID == 0 {
		return "", "", fmt.Errorf("github: /users/%s gave no id", login)
	}
	return strconv64(out.ID), out.Login, nil
}
