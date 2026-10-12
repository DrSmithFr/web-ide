package relay

import (
	"database/sql"
	"encoding/json"

	"github.com/DrSmithFr/web-ide/pod/internal/identity"
)

// The identity tables (migration 2). The relay never holds a private key or the phrase: it
// stores the certificates of an account (public keys, names, kinds) to list the devices and
// route pairing, and the signed revocation list. The account id is the account public key,
// so the relay can check a signature against it without being able to make one: only a
// device that holds the account key can approve a pairing or push a revocation list.

// Device is one stored certificate of an account.
type Device struct {
	Account string         `json:"account"`
	Key     string         `json:"key"`
	Cert    *identity.Cert `json:"cert"`
}

// Pairing is one pairing request: the joining device posted its public key with a short
// code, an admin reads it, compares the fingerprint of the key, signs the certificate and
// posts it back. The code is single use and expires.
type Pairing struct {
	Code    string         `json:"code"`
	Account string         `json:"account"`
	Key     string         `json:"key"`
	Name    string         `json:"name"`
	Kind    string         `json:"kind"`
	Cert    *identity.Cert `json:"cert,omitempty"` // set once the admin approves
	Created int64          `json:"created"`
	Expires int64          `json:"expires"`
	Used    bool           `json:"used"`
}

func migrateIdentity(db *sql.DB) error {
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS accounts (
			id TEXT PRIMARY KEY,
			revocations TEXT NOT NULL DEFAULT '',
			seq INTEGER NOT NULL DEFAULT 0)`); err != nil {
		return err
	}
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS devices (
			account TEXT NOT NULL,
			key TEXT NOT NULL,
			cert TEXT NOT NULL,
			PRIMARY KEY (account, key))`); err != nil {
		return err
	}
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS pairings (
			code TEXT PRIMARY KEY,
			account TEXT NOT NULL DEFAULT '',
			key TEXT NOT NULL,
			name TEXT NOT NULL DEFAULT '',
			kind TEXT NOT NULL DEFAULT '',
			cert TEXT NOT NULL DEFAULT '',
			created INTEGER NOT NULL,
			expires INTEGER NOT NULL,
			used INTEGER NOT NULL DEFAULT 0)`); err != nil {
		return err
	}
	return nil
}

// StoreDevice checks a certificate against the account it claims and stores it. A signature
// the relay cannot verify is refused: the relay has no account key, so a certificate can
// only come from a device that holds it.
func (s *Store) StoreDevice(c *identity.Cert) error {
	if c == nil {
		return identity.ErrNoCert
	}
	if err := identity.VerifyCert(c, c.Account, nil); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec("INSERT INTO devices (account, key, cert) VALUES (?,?,?) ON CONFLICT (account, key) DO UPDATE SET cert=excluded.cert",
		c.Account, c.Key, jsonQuote(c))
	return err
}

// jsonQuote encodes a certificate or a revocation list for storage. The canonical form is
// what the signature covers, so the whole JSON is kept as the stored text.
func jsonQuote(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

// Devices lists the stored certificates of an account.
func (s *Store) Devices(account string) ([]Device, error) {
	rows, err := s.db.Query("SELECT account, key, cert FROM devices WHERE account=? ORDER BY key", account)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Device
	for rows.Next() {
		var d Device
		var raw string
		if err := rows.Scan(&d.Account, &d.Key, &raw); err != nil {
			return nil, err
		}
		var c identity.Cert
		if err := json.Unmarshal([]byte(raw), &c); err != nil {
			return nil, err
		}
		d.Cert = &c
		out = append(out, d)
	}
	return out, rows.Err()
}

// SaveRevocations stores a revocation list only when it verifies against the account and its
// seq is higher than the stored one: a relay that replays an older list changes nothing. It
// reports whether the list was kept.
func (s *Store) SaveRevocations(r *identity.Revocations) (bool, error) {
	if err := identity.VerifyRevocations(r, r.Account); err != nil {
		return false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var seq int64
	err := s.db.QueryRow("SELECT seq FROM accounts WHERE id=?", r.Account).Scan(&seq)
	if err == sql.ErrNoRows {
		if _, err := s.db.Exec("INSERT INTO accounts (id, revocations, seq) VALUES (?,?,?)", r.Account, jsonQuote(r), r.Seq); err != nil {
			return false, err
		}
		s.dropRevokedPush(r.Account, r.Revoked)
		return true, nil
	}
	if err != nil {
		return false, err
	}
	if r.Seq <= seq {
		return false, nil
	}
	if _, err := s.db.Exec("UPDATE accounts SET revocations=?, seq=? WHERE id=?", jsonQuote(r), r.Seq, r.Account); err != nil {
		return false, err
	}
	s.dropRevokedPush(r.Account, r.Revoked)
	return true, nil
}

// Revocations returns the stored list of an account (nil when none).
func (s *Store) Revocations(account string) (*identity.Revocations, error) {
	var raw string
	err := s.db.QueryRow("SELECT revocations FROM accounts WHERE id=?", account).Scan(&raw)
	if err == sql.ErrNoRows || raw == "" {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var r identity.Revocations
	if err := json.Unmarshal([]byte(raw), &r); err != nil {
		return nil, err
	}
	return &r, nil
}
