package relay

import (
	"context"
	"encoding/base64"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
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
//	web-ide-pod relay team list
//	web-ide-pod relay team add|remove <owner> <login|account>
//	web-ide-pod relay team admin <org> <login>
//	web-ide-pod relay supporters

// IsAdminCommand reports whether the words after "relay" are an admin command, not the server.
func IsAdminCommand(args []string) bool {
	return len(args) > 0 && (args[0] == "vip" || args[0] == "team" || args[0] == "supporters")
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
	case len(args) == 2 && args[0] == "team" && args[1] == "list":
		return a.teamList()
	case len(args) == 4 && args[0] == "team" && (args[1] == "add" || args[1] == "remove"):
		return a.teamChange(args[1], args[2], args[3])
	case len(args) == 4 && args[0] == "team" && args[1] == "admin":
		return a.teamAdmin(args[2], args[3])
	case len(args) == 1 && args[0] == "supporters":
		return a.supporters()
	}
	return errors.New(adminUsage)
}

const adminUsage = `usage:
  web-ide-pod [-data DIR] relay vip add <login|account> [-note TEXT] [-days N | -until YYYY-MM-DD]
  web-ide-pod [-data DIR] relay vip remove <login|account>
  web-ide-pod [-data DIR] relay vip list
  web-ide-pod [-data DIR] relay team list
  web-ide-pod [-data DIR] relay team add|remove <owner> <login|account>
  web-ide-pod [-data DIR] relay team admin <org> <login>
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
	// The members of a team within its seats (#88).
	teams, err := a.st.Teams("")
	if err != nil {
		return err
	}
	for _, t := range teams {
		n := a.st.TeamSeats(t.OwnerID)
		members, _ := a.st.TeamMembers(t.OwnerID)
		for i, m := range members {
			if i >= n {
				break
			}
			var r *supporterRow
			if m.Kind == vipGitHub {
				r = row("github:"+m.ID, "@"+m.Login)
			} else {
				who := m.ID
				if _, login, _ := a.st.GitHubLink(m.ID); login != "" {
					who = m.ID + " (@" + login + ")"
				}
				r = row("account:"+m.ID, who)
				r.accounts = []string{m.ID}
			}
			lift(r, LevelVIP, 0)
			r.sources = append(r.sources, "team "+t.Name)
		}
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

// ownerTeam finds the team of an owner by its login: a team already made, or the sponsor row of a
// person at the Team tier, whose team is made with them as its admin.
func (a *admin) ownerTeam(owner string) (*Team, error) {
	owner = strings.TrimPrefix(owner, "@")
	teams, err := a.st.Teams("")
	if err != nil {
		return nil, err
	}
	for _, t := range teams {
		if strings.EqualFold(t.Name, owner) {
			return &t, nil
		}
	}
	sp := a.sponsorByLogin(owner)
	switch {
	case sp == nil:
		return nil, fmt.Errorf("%s is not a sponsor", owner)
	case seats(*sp) == 0:
		return nil, fmt.Errorf("%s does not sponsor at the Team tier ($50 a month or more)", owner)
	case sp.Org:
		return nil, fmt.Errorf("%s has no admin yet: relay team admin %s <login>", owner, sp.Login)
	}
	t := Team{OwnerID: sp.ID, Name: sp.Login, AdminID: sp.ID}
	return &t, a.st.PutTeam(t)
}

func (a *admin) sponsorByLogin(login string) *Sponsor {
	list, _ := a.st.sponsors()
	for _, sp := range list {
		if strings.EqualFold(sp.Login, login) {
			return &sp
		}
	}
	return nil
}

func (a *admin) teamList() error {
	teams, err := a.st.Teams("")
	if err != nil {
		return err
	}
	w := tabwriter.NewWriter(a.out, 0, 2, 2, ' ', 0)
	fmt.Fprintln(w, "TEAM\tSEATS\tADMIN\tMEMBER")
	for _, t := range teams {
		members, _ := a.st.TeamMembers(t.OwnerID)
		n := a.st.TeamSeats(t.OwnerID)
		admin := t.AdminID
		if t.AdminID == t.OwnerID {
			admin = "@" + t.Name
		} else if accounts, _ := a.st.LinkedAccounts(t.AdminID); len(accounts) > 0 {
			if _, login, _ := a.st.GitHubLink(accounts[0]); login != "" {
				admin = "@" + login
			}
		}
		fmt.Fprintf(w, "%s\t%d/%d\t%s\t\n", t.Name, len(members), n, admin)
		for i, m := range members {
			name := m.ID
			if m.Login != "" {
				name = "@" + m.Login
			}
			if i >= n {
				name += " (beyond the seats)"
			}
			fmt.Fprintf(w, "\t\t\t%s\n", name)
		}
	}
	return w.Flush()
}

func (a *admin) teamChange(op, owner, who string) error {
	t, err := a.ownerTeam(owner)
	if err != nil {
		return err
	}
	var m TeamMember
	if isAccountID(who) {
		m = TeamMember{Kind: vipAccount, ID: who}
	} else if op == "remove" && a.memberByLogin(t.OwnerID, who) != nil {
		m = *a.memberByLogin(t.OwnerID, who)
	} else {
		id, login, err := a.githubUser(who)
		if err != nil {
			return err
		}
		m = TeamMember{Kind: vipGitHub, ID: id, Login: login}
	}
	name := m.ID
	if m.Login != "" {
		name = "@" + m.Login
	}
	if op == "add" {
		if err := a.st.AddTeamMember(t.OwnerID, m); err != nil {
			return err
		}
		fmt.Fprintf(a.out, "%s is a member of the team %s\n", name, t.Name)
		return nil
	}
	ok, err := a.st.RemoveTeamMember(t.OwnerID, m.Kind, m.ID)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("%s is not a member of the team %s", name, t.Name)
	}
	fmt.Fprintf(a.out, "%s is no longer a member of the team %s\n", name, t.Name)
	return nil
}

func (a *admin) memberByLogin(ownerID, login string) *TeamMember {
	members, _ := a.st.TeamMembers(ownerID)
	for _, m := range members {
		if m.Kind == vipGitHub && strings.EqualFold(m.Login, strings.TrimPrefix(login, "@")) {
			return &m
		}
	}
	return nil
}

// teamAdmin names the admin of an organization's team by hand, when the organization refuses the
// OAuth app the relay proves its admins with.
func (a *admin) teamAdmin(org, login string) error {
	sp := a.sponsorByLogin(strings.TrimPrefix(org, "@"))
	if sp == nil || !sp.Org {
		return fmt.Errorf("%s is not an organization sponsoring the project", org)
	}
	id, exact, err := a.githubUser(login)
	if err != nil {
		return err
	}
	if err := a.st.PutTeam(Team{OwnerID: sp.ID, Org: true, Name: sp.Login, AdminID: id}); err != nil {
		return err
	}
	fmt.Fprintf(a.out, "@%s manages the team %s (%d seats now)\n", exact, sp.Login, seats(*sp))
	return nil
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
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	id, exact, err = githubAccount(ctx, a.client, a.apiBase, "users", login, "")
	if err != nil && strings.Contains(err.Error(), "no GitHub account") {
		return "", "", fmt.Errorf("no GitHub user %q (an account id is 43 characters)", strings.TrimPrefix(login, "@"))
	}
	return id, exact, err
}
