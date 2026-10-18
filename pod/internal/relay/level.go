package relay

import (
	"context"
	"log"
	"sort"
	"time"
)

// The level of an account (#87): what it gives to the project, read from its GitHub sponsorship
// and from the VIP list of the relay (vips.go). It sets the TURN quota and the badge of the panel.
// The vault stays free at every level.
//
//   - monthly: $2 Backer (the quota raised, -quota-backer), $5 Supporter (no quota), $10 or more
//     VIP (no quota, the badge); between two tiers the lower one counts ($7 is a Supporter), and
//     the $50 Team tier is a VIP for the sponsor (its seats are #88);
//   - one-time: $5 is a Supporter for 30 days, $10 a VIP for 90 days, $50 a VIP for 360 days;
//     below $5, nothing. They do not add up: the best live one counts.
type Level int

const (
	LevelFree Level = iota
	LevelBacker
	LevelSupporter
	LevelVIP
)

var levelNames = [...]string{"free", "backer", "supporter", "vip"}

func (l Level) String() string {
	if l < LevelFree || l > LevelVIP {
		return "free"
	}
	return levelNames[l]
}

// wire is the name a message carries: "" for free, so an old reader sees nothing new.
func (l Level) wire() string {
	if l == LevelFree {
		return ""
	}
	return l.String()
}

// The tier grid, in cents.
const (
	backerCents    = 200
	supporterCents = 500
	vipCents       = 1000
	teamCents      = 5000
	day            = 24 * time.Hour
)

// levelOf applies the grid to one sponsorship: the level it gives now, and when it ends (0:
// never, a monthly one lasts as long as it is active).
func levelOf(sp Sponsor, now time.Time) (Level, int64) {
	if !sp.OneTime {
		switch {
		case sp.Cents >= vipCents:
			return LevelVIP, 0
		case sp.Cents >= supporterCents:
			return LevelSupporter, 0
		case sp.Cents >= backerCents:
			return LevelBacker, 0
		}
		return LevelFree, 0
	}
	var lv Level
	var lasts time.Duration
	switch {
	case sp.Cents >= teamCents:
		lv, lasts = LevelVIP, 360*day
	case sp.Cents >= vipCents:
		lv, lasts = LevelVIP, 90*day
	case sp.Cents >= supporterCents:
		lv, lasts = LevelSupporter, 30*day
	default:
		return LevelFree, 0
	}
	end := time.Unix(sp.Since, 0).Add(lasts)
	if !now.Before(end) {
		return LevelFree, 0
	}
	return lv, end.Unix()
}

// better orders two (level, end) pairs: the higher level, then the one that lasts longer (0 is
// no end).
func better(la Level, ua int64, lb Level, ub int64) bool {
	if la != lb {
		return la > lb
	}
	if (ua == 0) != (ub == 0) {
		return ua == 0
	}
	return ua > ub
}

// AccountLevel is the level of an account and where it comes from: the sponsorship of its linked
// GitHub login, and the VIP list. Until is the end of the level (0: none).
type AccountLevel struct {
	Level        Level
	Until        int64
	Sponsor      Level // what the sponsorship alone gives
	SponsorUntil int64
	VIP          bool // on the VIP list, live
	VIPUntil     int64
}

// Level reads the level of an account (or of a TURN key that is a pod id: free). It needs no
// GitHub app: a self-hosted relay lists its VIPs by account id.
func (s *Store) Level(account string, now time.Time) AccountLevel {
	var al AccountLevel
	if account == "" {
		return al
	}
	githubID, _, _ := s.GitHubLink(account)
	if githubID != "" {
		if sp, err := s.Sponsor(githubID); err == nil && sp != nil {
			al.Sponsor, al.SponsorUntil = levelOf(*sp, now)
		}
	}
	if v := s.vipFor(account, githubID, now); v != nil {
		al.VIP, al.VIPUntil = true, v.Until
	}
	al.Level, al.Until = al.Sponsor, al.SponsorUntil
	if al.VIP && better(LevelVIP, al.VIPUntil, al.Level, al.Until) {
		al.Level, al.Until = LevelVIP, al.VIPUntil
	}
	return al
}

// levelPollEvery is how often the relay looks for a level that moved without it: a row the CLI
// wrote next to the live relay (relay vip), or an end that passed.
const levelPollEvery = 30 * time.Second

// watchLevels tells the pods of the accounts whose level moved. The CLI writes relay.db from
// another process, so the relay reads PRAGMA data_version, which moves on a commit of any other
// connection. It is per connection, hence a connection held for it alone (two connections of the
// pool give values that do not compare); the relay's own writes move it too (the TURN usage each
// minute), so a change only recomputes the levels, and the pods are told only when theirs differ.
func (s *Server) watchLevels(ctx context.Context, every time.Duration) {
	if every <= 0 {
		every = levelPollEvery
	}
	conn, err := s.store.db.Conn(ctx)
	if err != nil {
		log.Printf("relay: levels: %v", err)
		return
	}
	defer conn.Close()
	version := func() int64 {
		var v int64
		if err := conn.QueryRowContext(ctx, "PRAGMA data_version").Scan(&v); err != nil {
			return -1
		}
		return v
	}
	last := version()
	levels, next := s.store.levels(time.Now())
	tick := time.NewTicker(every)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		now := time.Now()
		v := version()
		if v == last && (next == 0 || now.Unix() < next) {
			continue
		}
		last = v
		var cur map[string]AccountLevel
		cur, next = s.store.levels(now)
		var moved []string
		for a, al := range cur {
			if levels[a] != al {
				moved = append(moved, a)
			}
		}
		for a := range levels {
			if _, ok := cur[a]; !ok {
				moved = append(moved, a)
			}
		}
		levels = cur
		sort.Strings(moved)
		s.tellQuota(moved)
	}
}

// levels is the level of every account above free (the linked ones and the VIPs by account),
// and the first end still to come (0: none), when one of them moves on its own.
func (s *Store) levels(now time.Time) (map[string]AccountLevel, int64) {
	accounts, _ := s.LinkedAccounts("")
	for _, v := range s.liveVIPs(now) {
		if v.Kind == vipAccount {
			accounts = append(accounts, v.ID)
		}
	}
	out := map[string]AccountLevel{}
	var next int64
	for _, a := range accounts {
		al := s.Level(a, now)
		if al.Level == LevelFree && !al.VIP {
			continue
		}
		out[a] = al
		for _, u := range []int64{al.SponsorUntil, al.VIPUntil} {
			if u > 0 && (next == 0 || u < next) {
				next = u
			}
		}
	}
	return out, next
}
