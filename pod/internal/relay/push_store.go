package relay

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"time"
)

// The push subscriptions (migration 3): an opaque id, the account and the browser device that
// registered it, and its endpoint at the push service. No payload is ever stored.

func migratePush(db *sql.DB) error {
	_, err := db.Exec(`CREATE TABLE IF NOT EXISTS push_subs (
			id TEXT PRIMARY KEY,
			account TEXT NOT NULL,
			device TEXT NOT NULL,
			endpoint TEXT NOT NULL,
			created INTEGER NOT NULL,
			UNIQUE (account, endpoint))`)
	return err
}

// AddPushSub registers the endpoint of a device of an account and answers its id; an endpoint
// already registered for the account keeps its id.
func (s *Store) AddPushSub(account, device, endpoint string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var id string
	err := s.db.QueryRow("SELECT id FROM push_subs WHERE account=? AND endpoint=?", account, endpoint).Scan(&id)
	if err == nil {
		_, err = s.db.Exec("UPDATE push_subs SET device=? WHERE id=?", device, id)
		return id, err
	}
	if err != sql.ErrNoRows {
		return "", err
	}
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	id = hex.EncodeToString(b)
	_, err = s.db.Exec("INSERT INTO push_subs (id, account, device, endpoint, created) VALUES (?,?,?,?,?)",
		id, account, device, endpoint, time.Now().Unix())
	return id, err
}

// PushSub answers the account and the endpoint of a subscription.
func (s *Store) PushSub(id string) (account, endpoint string, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	err := s.db.QueryRow("SELECT account, endpoint FROM push_subs WHERE id=?", id).Scan(&account, &endpoint)
	return account, endpoint, err == nil
}

// DeletePushSub drops a subscription of an account (of one device when device is set).
func (s *Store) DeletePushSub(id, account, device string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if device != "" {
		_, err := s.db.Exec("DELETE FROM push_subs WHERE id=? AND account=? AND device=?", id, account, device)
		return err
	}
	_, err := s.db.Exec("DELETE FROM push_subs WHERE id=? AND account=?", id, account)
	return err
}

// dropRevokedPush deletes the subscriptions of the revoked devices of an account (s.mu held).
func (s *Store) dropRevokedPush(account string, revoked []string) {
	for _, key := range revoked {
		_, _ = s.db.Exec("DELETE FROM push_subs WHERE account=? AND device=?", account, key)
	}
}
