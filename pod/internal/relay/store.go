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

const dbVersion = 1

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

func migrate(db *sql.DB) error {
	var ver int
	if err := db.QueryRow("PRAGMA user_version").Scan(&ver); err != nil {
		return err
	}
	if ver < dbVersion {
		if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS pods (
				id TEXT PRIMARY KEY,
				secret TEXT NOT NULL,
				name TEXT NOT NULL DEFAULT '',
				online INTEGER NOT NULL DEFAULT 0,
				last_seen INTEGER NOT NULL DEFAULT 0)`); err != nil {
			return err
		}
		if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS usage (
				id INTEGER PRIMARY KEY AUTOINCREMENT,
				pod_id TEXT NOT NULL,
				ts INTEGER NOT NULL,
				in_bytes INTEGER NOT NULL DEFAULT 0,
				out_bytes INTEGER NOT NULL DEFAULT 0)`); err != nil {
			return err
		}
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

// Usage records a TURN allocation of a pod.
func (s *Store) Usage(podID string, inBytes, outBytes int64) error {
	_, err := s.db.Exec("INSERT INTO usage (pod_id, ts, in_bytes, out_bytes) VALUES (?,?,?,?)",
		podID, time.Now().Unix(), inBytes, outBytes)
	return err
}

// Close closes the database.
func (s *Store) Close() error { return s.db.Close() }
