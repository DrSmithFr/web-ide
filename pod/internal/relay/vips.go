package relay

import (
	"database/sql"
	"errors"
	"strings"
	"time"
)

// The VIP list (#87): people the admin of the relay gives the VIP level by hand, by GitHub id (a
// login resolved once by the CLI) or by relay account id, which needs no GitHub link nor app. A
// row may end; an ended row no longer counts but stays on the list until it is removed. The note
// is for the admin only: no message of the relay carries it.

const (
	vipGitHub  = "github"
	vipAccount = "account"
)

// VIP is one row of the list.
type VIP struct {
	Kind  string // vipGitHub or vipAccount
	ID    string // the GitHub id, or the account id
	Login string // the GitHub login when it was given ("" for an account)
	Note  string
	Until int64 // unix seconds, 0: no end
	Added int64
}

// live reports whether the row still gives the VIP level.
func (v VIP) live(now time.Time) bool { return v.Until == 0 || now.Unix() < v.Until }

func migrateVIPs(db *sql.DB) error {
	_, err := db.Exec(`CREATE TABLE IF NOT EXISTS vips (
			kind TEXT NOT NULL,
			id TEXT NOT NULL,
			login TEXT NOT NULL DEFAULT '',
			note TEXT NOT NULL DEFAULT '',
			until INTEGER NOT NULL DEFAULT 0,
			added INTEGER NOT NULL DEFAULT 0,
			PRIMARY KEY (kind, id))`)
	return err
}

// AddVIP puts a row on the list; adding it again replaces its login, note and end.
func (s *Store) AddVIP(v VIP) error {
	if (v.Kind != vipGitHub && v.Kind != vipAccount) || v.ID == "" {
		return errors.New("relay: a VIP is a GitHub id or an account id")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(`INSERT INTO vips (kind, id, login, note, until, added) VALUES (?,?,?,?,?,?)
			ON CONFLICT (kind, id) DO UPDATE SET login=excluded.login, note=excluded.note, until=excluded.until`,
		v.Kind, v.ID, v.Login, v.Note, v.Until, time.Now().Unix())
	return err
}

// RemoveVIP takes a row off the list; false when there was none.
func (s *Store) RemoveVIP(kind, id string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	res, err := s.db.Exec("DELETE FROM vips WHERE kind=? AND id=?", kind, id)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// VIPs is the whole list, ended rows too.
func (s *Store) VIPs() ([]VIP, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rows, err := s.db.Query("SELECT kind, id, login, note, until, added FROM vips ORDER BY kind, login, id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []VIP
	for rows.Next() {
		var v VIP
		if err := rows.Scan(&v.Kind, &v.ID, &v.Login, &v.Note, &v.Until, &v.Added); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// vipByLogin finds the GitHub row of a login (a login is case-insensitive on GitHub).
func (s *Store) vipByLogin(login string) *VIP {
	list, _ := s.VIPs()
	for _, v := range list {
		if v.Kind == vipGitHub && strings.EqualFold(v.Login, login) {
			return &v
		}
	}
	return nil
}

func (s *Store) liveVIPs(now time.Time) []VIP {
	list, _ := s.VIPs()
	out := list[:0]
	for _, v := range list {
		if v.live(now) {
			out = append(out, v)
		}
	}
	return out
}

// vipFor is the live row that makes an account a VIP, by its id or by its linked GitHub id: the
// one that lasts longest (nil when none).
func (s *Store) vipFor(account, githubID string, now time.Time) *VIP {
	var best *VIP
	for _, v := range s.liveVIPs(now) {
		if !(v.Kind == vipAccount && v.ID == account) && !(v.Kind == vipGitHub && githubID != "" && v.ID == githubID) {
			continue
		}
		if best == nil || better(LevelVIP, v.Until, LevelVIP, best.Until) {
			v := v
			best = &v
		}
	}
	return best
}
