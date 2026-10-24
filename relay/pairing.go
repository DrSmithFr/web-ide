package relay

import (
	"crypto/rand"
	"database/sql"
	"encoding/base32"
	"encoding/json"
	"fmt"
	"time"

	"antennae.space/ide/lib/identity"
)

// pairingTTL is how long a pairing code stays open.
const pairingTTL = 10 * time.Minute

// newCode returns a pairing code: 8 base32 characters (40 bits), read aloud, typed or
// scanned as a QR.
func newCode() (string, error) {
	b := make([]byte, 5)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base32.StdEncoding.EncodeToString(b), nil
}

// CreatePairing records a pairing request: the joining device posted its public key, an
// admin will read it, compare the fingerprint, and sign the certificate. The account is
// empty until an admin claims the code.
func (s *Store) CreatePairing(key, name, kind string) (*Pairing, error) {
	code, err := newCode()
	if err != nil {
		return nil, err
	}
	now := time.Now()
	p := &Pairing{Code: code, Key: key, Name: name, Kind: kind, Created: now.Unix(), Expires: now.Add(pairingTTL).Unix()}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.db.Exec("INSERT INTO pairings (code, key, name, kind, created, expires) VALUES (?,?,?,?,?,?)",
		p.Code, p.Key, p.Name, p.Kind, p.Created, p.Expires); err != nil {
		return nil, err
	}
	return p, nil
}

// Pairing returns one pairing by code (nil when the code is unknown).
func (s *Store) Pairing(code string) (*Pairing, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.scanPairing(s.db.QueryRow("SELECT code, account, key, name, kind, cert, created, expires, used FROM pairings WHERE code=?", code))
}

// Pairings lists the open pairings of an account (approved ones included, so an admin sees
// what it signed).
func (s *Store) Pairings(account string) ([]Pairing, error) {
	rows, err := s.db.Query("SELECT code, account, key, name, kind, cert, created, expires, used FROM pairings WHERE account=? AND expires > ? ORDER BY created", account, time.Now().Unix())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Pairing
	for rows.Next() {
		p, err := s.scanPairingRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *p)
	}
	return out, rows.Err()
}

// AttachPairing records the key of the device that opened the invite link for a code. The
// code is created by the device that shows the QR (a pod, or an admin inviting a phone): the
// joining device posts its own key there. A code that already holds a key refuses another
// one — that is the guard against a relay that swaps the key, the fingerprint the device
// shows must match the key the admin reads.
func (s *Store) AttachPairing(code, key, name, kind string) (*Pairing, error) {
	p, err := s.Pairing(code)
	if err != nil {
		return nil, err
	}
	if p == nil {
		return nil, fmt.Errorf("relay: no pairing %q", code)
	}
	if p.Used {
		return nil, fmt.Errorf("relay: pairing %q already used", code)
	}
	if time.Now().Unix() > p.Expires {
		return nil, fmt.Errorf("relay: pairing %q expired", code)
	}
	if p.Key != "" && p.Key != key {
		return nil, fmt.Errorf("relay: pairing %q already holds another key", code)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.db.Exec("UPDATE pairings SET key=?, name=?, kind=? WHERE code=?", key, name, kind, code); err != nil {
		return nil, err
	}
	p.Key, p.Name, p.Kind = key, name, kind
	return p, nil
}

// ClaimPairing gives a code to an account: the first admin that reads it becomes the one
// that can approve it. A code already claimed by another account is refused.
func (s *Store) ClaimPairing(code, account string) (*Pairing, error) {
	p, err := s.Pairing(code)
	if err != nil {
		return nil, err
	}
	if p == nil {
		return nil, fmt.Errorf("relay: no pairing %q", code)
	}
	if p.Account != "" && p.Account != account {
		return nil, fmt.Errorf("relay: pairing %q belongs to another account", code)
	}
	if p.Account == "" {
		s.mu.Lock()
		defer s.mu.Unlock()
		if _, err := s.db.Exec("UPDATE pairings SET account=? WHERE code=?", account, code); err != nil {
			return nil, err
		}
		p.Account = account
	}
	return p, nil
}

// ApprovePairing stores the certificate an admin signed for a pairing. The certificate must
// name the key the device posted (the guard against a relay that swapped the key) and verify
// against the account; a code already used or expired is refused.
func (s *Store) ApprovePairing(code, account string, cert *identity.Cert) (*Pairing, error) {
	p, err := s.Pairing(code)
	if err != nil {
		return nil, err
	}
	if p == nil {
		return nil, fmt.Errorf("relay: no pairing %q", code)
	}
	if p.Used {
		return nil, fmt.Errorf("relay: pairing %q already used", code)
	}
	if time.Now().Unix() > p.Expires {
		return nil, fmt.Errorf("relay: pairing %q expired", code)
	}
	if cert == nil || cert.Key != p.Key {
		return nil, fmt.Errorf("relay: the certificate does not match the pairing key")
	}
	if err := identity.VerifyCert(cert, account, nil); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.db.Exec("UPDATE pairings SET cert=?, used=1 WHERE code=?", jsonQuote(cert), code); err != nil {
		return nil, err
	}
	p.Cert = cert
	p.Used = true
	return p, nil
}

func (s *Store) scanPairing(row *sql.Row) (*Pairing, error) {
	var code, account, key, name, kind, cert string
	var created, expires int64
	var used int
	if err := row.Scan(&code, &account, &key, &name, &kind, &cert, &created, &expires, &used); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return pairingFromRow(code, account, key, name, kind, cert, created, expires, used), nil
}

func (s *Store) scanPairingRow(rows *sql.Rows) (*Pairing, error) {
	var code, account, key, name, kind, cert string
	var created, expires int64
	var used int
	if err := rows.Scan(&code, &account, &key, &name, &kind, &cert, &created, &expires, &used); err != nil {
		return nil, err
	}
	return pairingFromRow(code, account, key, name, kind, cert, created, expires, used), nil
}

func pairingFromRow(code, account, key, name, kind, cert string, created, expires int64, used int) *Pairing {
	p := &Pairing{Code: code, Account: account, Key: key, Name: name, Kind: kind, Created: created, Expires: expires, Used: used == 1}
	if cert != "" {
		var c identity.Cert
		if err := json.Unmarshal([]byte(cert), &c); err == nil {
			p.Cert = &c
		}
	}
	return p
}
