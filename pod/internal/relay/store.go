// Package relay is the "web-ide-pod relay" subcommand: a small server the pod dials and
// the browser opens. It does signaling (WebSocket), hosts an embedded Pion TURN server
// (STUN/TURN over UDP, TCP and TLS 443) and serves the bootstrap page and the Service
// Worker that load the app through the tunnel. The pod token never crosses this server:
// it is proved inside the WebRTC DataChannel, bound to both DTLS fingerprints.
package relay

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

// dbVersion 2 adds the identity tables (accounts, devices, pairings), see accounts.go and
// pairing.go; dbVersion 3 adds the sync tables (blobs, wraps, published X25519 keys), see
// syncstore.go. The relay stores certificates, signed revocation lists and encrypted blobs,
// never a private key, a phrase or a sync key; dbVersion 4 adds the push subscriptions
// (push_store.go); dbVersion 5 replaces the per-allocation usage rows (which held no byte
// count) with the TURN bytes aggregated per key and month (turn_usage, see turn.go).
const dbVersion = 5

type Pod struct {
	ID       string
	Name     string
	Online   bool
	LastSeen int64
}

type podRow struct {
	ID       string
	Secret   string
	Name     string
	Online   bool
	LastSeen int64
}

// Store is the SQLite-backed state of the relay (relay.db). It remembers which pods
// registered, and their TURN usage.
type Store struct {
	db   *sql.DB
	mu   sync.Mutex
	path string
	pods map[string]*Pod
}

// OpenStore opens (creating if needed) the relay database at path.
func OpenStore(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	if err := migrate(db); err != nil {
		db.Close()
		return nil, err
	}
	s := &Store{db: db, path: path, pods: map[string]*Pod{}}
	if err := s.loadPods(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

// DBPath is the path of the relay database file.
func (s *Store) DBPath() string { return s.path }

// migrate runs on every open. Each migration is idempotent (CREATE TABLE IF NOT EXISTS,
// addColumn), so a database left by an older version is brought up to date; the version is only
// written when it moved.
func migrate(db *sql.DB) error {
	var ver int
	if err := db.QueryRow("PRAGMA user_version").Scan(&ver); err != nil {
		return err
	}
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS pods (
			id TEXT PRIMARY KEY,
			secret TEXT NOT NULL,
			name TEXT NOT NULL DEFAULT '',
			online INTEGER NOT NULL DEFAULT 0,
			last_seen INTEGER NOT NULL DEFAULT 0)`); err != nil {
		return err
	}
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS turn_usage (
			key TEXT NOT NULL,
			month TEXT NOT NULL,
			in_bytes INTEGER NOT NULL DEFAULT 0,
			out_bytes INTEGER NOT NULL DEFAULT 0,
			updated INTEGER NOT NULL DEFAULT 0,
			PRIMARY KEY (key, month))`); err != nil {
		return err
	}
	if ver < dbVersion {
		// The old usage table held one row per TURN allocation with no byte count: the counting
		// of #29 replaces it, and nothing of value is lost (it never held a byte nor a name).
		if _, err := db.Exec(`DROP TABLE IF EXISTS usage`); err != nil {
			return err
		}
	}
	if err := migrateIdentity(db); err != nil {
		return err
	}
	if err := migrateSync(db); err != nil {
		return err
	}
	if err := migratePush(db); err != nil {
		return err
	}
	if ver < dbVersion {
		if _, err := db.Exec(fmt.Sprintf("PRAGMA user_version = %d", dbVersion)); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) loadPods() error {
	rows, err := s.db.Query("SELECT id, secret, name, online, last_seen FROM pods")
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var r podRow
		if err := rows.Scan(&r.ID, &r.Secret, &r.Name, &r.Online, &r.LastSeen); err != nil {
			return err
		}
		s.pods[r.ID] = &Pod{ID: r.ID, Name: r.Name, Online: r.Online, LastSeen: r.LastSeen}
	}
	return rows.Err()
}

// Register records a pod and marks it online. A new pod is stored with the secret it
// gave; an existing pod must match its stored secret (the relay trusts the first
// registration, then checks it afterwards).
func (s *Store) Register(id, secret, name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	var row podRow
	err := s.db.QueryRow("SELECT id, secret, name, online, last_seen FROM pods WHERE id=?", id).
		Scan(&row.ID, &row.Secret, &row.Name, &row.Online, &row.LastSeen)
	switch {
	case err == sql.ErrNoRows: // first sight: store the secret the pod gave
		if secret == "" {
			return fmt.Errorf("relay: new pod %q without a secret", id)
		}
		if _, err := s.db.Exec("INSERT INTO pods (id, secret, name, online, last_seen) VALUES (?,?,?,?,?)",
			id, secret, name, 1, time.Now().Unix()); err != nil {
			return err
		}
	case err != nil:
		return err
	default:
		if row.Secret != secret {
			return fmt.Errorf("relay: the pod %q does not match its registered secret", id)
		}
	}
	s.pods[id] = &Pod{ID: id, Name: row.Name, Online: true, LastSeen: time.Now().Unix()}
	if name != "" {
		s.pods[id].Name = name
		_, _ = s.db.Exec("UPDATE pods SET name=? WHERE id=?", name, id)
	}
	return nil
}

// VerifySecret reports whether the pod id and secret match the registered pod.
func (s *Store) VerifySecret(id, secret string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	var row podRow
	err := s.db.QueryRow("SELECT id, secret, name, online, last_seen FROM pods WHERE id=?", id).
		Scan(&row.ID, &row.Secret, &row.Name, &row.Online, &row.LastSeen)
	if err != nil {
		return false
	}
	return row.Secret == secret
}

// Online marks a pod online (a reconnected pod).
func (s *Store) Online(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if p, ok := s.pods[id]; ok {
		p.Online = true
		p.LastSeen = time.Now().Unix()
		_ = s.savePod(id, p)
	}
}

// Offline marks a pod offline.
func (s *Store) Offline(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if p, ok := s.pods[id]; ok {
		p.Online = false
		p.LastSeen = time.Now().Unix()
		_ = s.savePod(id, p)
	}
}

func (s *Store) savePod(id string, p *Pod) error {
	_, err := s.db.Exec("UPDATE pods SET online=?, last_seen=? WHERE id=?", p.Online, p.LastSeen, id)
	return err
}

// Name returns the display name of a pod (empty if none stored).
func (s *Store) Name(id string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if p, ok := s.pods[id]; ok {
		return p.Name
	}
	return ""
}

// OnlinePod reports whether a pod is registered and online (the gate for TURN creds).
func (s *Store) OnlinePod(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.pods[id]
	return ok && p.Online
}

// usageMonth is the month a usage row belongs to ("2026-10"): the quota is monthly.
func usageMonth(t time.Time) string { return t.UTC().Format("2006-01") }

// AddUsage adds bytes relayed through TURN to the total of a key for a month. The key is the
// account of a paired device, the pod id otherwise (turn.go): a month per key, one row, written
// by the flush of the live counters.
func (s *Store) AddUsage(key, month string, inBytes, outBytes int64) error {
	if key == "" || (inBytes == 0 && outBytes == 0) {
		return nil
	}
	_, err := s.db.Exec(`INSERT INTO turn_usage (key, month, in_bytes, out_bytes, updated)
			VALUES (?,?,?,?,?)
			ON CONFLICT (key, month) DO UPDATE SET
				in_bytes = in_bytes + excluded.in_bytes,
				out_bytes = out_bytes + excluded.out_bytes,
				updated = excluded.updated`,
		key, month, inBytes, outBytes, time.Now().Unix())
	return err
}

// TurnUsage is the number of bytes a key moved through TURN in a month (both directions).
func (s *Store) TurnUsage(key, month string) (int64, error) {
	var in, out int64
	err := s.db.QueryRow("SELECT in_bytes, out_bytes FROM turn_usage WHERE key=? AND month=?", key, month).
		Scan(&in, &out)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	return in + out, nil
}

// Close closes the database.
func (s *Store) Close() error { return s.db.Close() }
