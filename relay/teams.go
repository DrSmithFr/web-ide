package relay

import (
	"database/sql"
	"errors"
	"time"

	"antennae.space/ide/lib/i18n"
)

// Teams (#88): a monthly sponsorship of $50 or more is the Team tier, $5 a seat (floor(amount /
// $5): $50 is 10 seats, $100 is 20). Its owner — the person who sponsors, or the organization —
// is VIP by the sponsorship itself and takes no seat; the admin of the team (the owner, or a
// proven admin of the organization) adds members by GitHub id or by account id, and the members
// within the seats get the VIP level while the sponsorship stays at that tier. When the count
// falls, the members added last lose the level first, until the admin removes some.

const seatCents = 500

// seats is the number of seats a sponsorship gives now: none below $50 a month, none for a
// one-time payment.
func seats(sp Sponsor) int {
	if sp.OneTime || sp.Cents < teamCents {
		return 0
	}
	return int(sp.Cents / seatCents)
}

// Team is one team: its owner is a GitHub id (a person or an organization) whose sponsorship
// gives the seats; the admin is the GitHub id that manages the members.
type Team struct {
	OwnerID string
	Org     bool
	Name    string // the GitHub login of the owner
	AdminID string
	Created int64
}

// TeamMember is one member: a GitHub id (a login resolved once) or an account id.
type TeamMember struct {
	Kind  string // vipGitHub or vipAccount
	ID    string
	Login string
	Added int64
}

func migrateTeams(db *sql.DB) error {
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS teams (
			owner_id TEXT PRIMARY KEY,
			org INTEGER NOT NULL DEFAULT 0,
			name TEXT NOT NULL DEFAULT '',
			admin_github_id TEXT NOT NULL DEFAULT '',
			created INTEGER NOT NULL DEFAULT 0)`); err != nil {
		return err
	}
	_, err := db.Exec(`CREATE TABLE IF NOT EXISTS team_members (
			owner_id TEXT NOT NULL,
			kind TEXT NOT NULL,
			id TEXT NOT NULL,
			login TEXT NOT NULL DEFAULT '',
			added INTEGER NOT NULL DEFAULT 0,
			PRIMARY KEY (owner_id, kind, id))`)
	return err
}

// PutTeam creates a team or sets its name and admin.
func (s *Store) PutTeam(t Team) error {
	if t.OwnerID == "" || t.AdminID == "" {
		return errors.New("relay: a team needs an owner and an admin")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(`INSERT INTO teams (owner_id, org, name, admin_github_id, created) VALUES (?,?,?,?,?)
			ON CONFLICT (owner_id) DO UPDATE SET org=excluded.org, name=excluded.name, admin_github_id=excluded.admin_github_id`,
		t.OwnerID, t.Org, t.Name, t.AdminID, time.Now().Unix())
	return err
}

// Teams lists the teams, or the ones a GitHub id is the admin of.
func (s *Store) Teams(adminID string) ([]Team, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	q, args := "SELECT owner_id, org, name, admin_github_id, created FROM teams", []any{}
	if adminID != "" {
		q, args = q+" WHERE admin_github_id=?", []any{adminID}
	}
	rows, err := s.db.Query(q+" ORDER BY name", args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Team
	for rows.Next() {
		var t Team
		if err := rows.Scan(&t.OwnerID, &t.Org, &t.Name, &t.AdminID, &t.Created); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// Team is the team of an owner (nil when none).
func (s *Store) Team(ownerID string) (*Team, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t := Team{OwnerID: ownerID}
	err := s.db.QueryRow("SELECT org, name, admin_github_id, created FROM teams WHERE owner_id=?", ownerID).
		Scan(&t.Org, &t.Name, &t.AdminID, &t.Created)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &t, nil
}

// TeamMembers lists the members of a team, the first added first (the order the seats follow).
func (s *Store) TeamMembers(ownerID string) ([]TeamMember, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rows, err := s.db.Query("SELECT kind, id, login, added FROM team_members WHERE owner_id=? ORDER BY added, rowid", ownerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []TeamMember
	for rows.Next() {
		var m TeamMember
		if err := rows.Scan(&m.Kind, &m.ID, &m.Login, &m.Added); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// TeamSeats is what the sponsorship of an owner gives now.
func (s *Store) TeamSeats(ownerID string) int {
	sp, err := s.Sponsor(ownerID)
	if err != nil || sp == nil {
		return 0
	}
	return seats(*sp)
}

// AddTeamMember adds a member within the seats (added again: its login is replaced, its place
// kept).
func (s *Store) AddTeamMember(ownerID string, m TeamMember) error {
	if (m.Kind != vipGitHub && m.Kind != vipAccount) || m.ID == "" {
		return errors.New("relay: a member is a GitHub id or an account id")
	}
	if m.Kind == vipGitHub && m.ID == ownerID {
		return i18n.New("the owner of a team takes no seat")
	}
	members, err := s.TeamMembers(ownerID)
	if err != nil {
		return err
	}
	known := false
	for _, x := range members {
		known = known || (x.Kind == m.Kind && x.ID == m.ID)
	}
	if n := s.TeamSeats(ownerID); !known && len(members) >= n {
		return i18n.Errorf("the team has no seat left (%d of %d)", len(members), n)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err = s.db.Exec(`INSERT INTO team_members (owner_id, kind, id, login, added) VALUES (?,?,?,?,?)
			ON CONFLICT (owner_id, kind, id) DO UPDATE SET login=excluded.login`,
		ownerID, m.Kind, m.ID, m.Login, time.Now().Unix())
	return err
}

// RemoveTeamMember takes a member off a team; false when it was not on it.
func (s *Store) RemoveTeamMember(ownerID, kind, id string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	res, err := s.db.Exec("DELETE FROM team_members WHERE owner_id=? AND kind=? AND id=?", ownerID, kind, id)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// teamFor is the team that makes an account a VIP now, by its id or its linked GitHub id: one
// whose owner has seats, the member within them ("" when none).
func (s *Store) teamFor(account, githubID string) string {
	s.mu.Lock()
	rows, err := s.db.Query(`SELECT owner_id FROM team_members WHERE (kind=? AND id=?) OR (kind=? AND id=? AND id<>'')`,
		vipAccount, account, vipGitHub, githubID)
	var owners []string
	if err == nil {
		for rows.Next() {
			var o string
			if rows.Scan(&o) == nil {
				owners = append(owners, o)
			}
		}
		rows.Close()
	}
	s.mu.Unlock()
	for _, owner := range owners {
		n := s.TeamSeats(owner)
		if n == 0 {
			continue
		}
		members, _ := s.TeamMembers(owner)
		for i, m := range members {
			if i >= n {
				break
			}
			if (m.Kind == vipAccount && m.ID == account) || (m.Kind == vipGitHub && githubID != "" && m.ID == githubID) {
				if t, _ := s.Team(owner); t != nil {
					return t.Name
				}
			}
		}
	}
	return ""
}

// teamAccounts lists the members by account id of every team: the tick recomputes them, since a
// webhook only tells the accounts of the owner.
func (s *Store) teamAccounts() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	rows, err := s.db.Query("SELECT DISTINCT id FROM team_members WHERE kind=?", vipAccount)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var a string
		if rows.Scan(&a) == nil {
			out = append(out, a)
		}
	}
	return out
}
