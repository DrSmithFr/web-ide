package relay

import (
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/DrSmithFr/web-ide/lib/i18n"
	"github.com/DrSmithFr/web-ide/lib/identity"
)

// The sync tables (migration 3): one encrypted blob per account and kind, the key of the current
// epoch wrapped for each device, and the X25519 key each device published. The relay holds no sync
// key, so it stores ciphertext only: it can check a signature against a device id it knows belongs
// to the account, it cannot read a value.

// maxBlobSize is the cap of a stored blob, in characters of base64 ciphertext.
const maxBlobSize = 64 << 10

func migrateSync(db *sql.DB) error {
	// accounts.epoch: the epoch of the sync key, moved by a revocation list.
	if err := addColumn(db, "accounts", "epoch", "INTEGER NOT NULL DEFAULT 0"); err != nil {
		return err
	}
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS blobs (
			account TEXT NOT NULL,
			kind TEXT NOT NULL,
			seq INTEGER NOT NULL,
			epoch INTEGER NOT NULL,
			device TEXT NOT NULL,
			op TEXT NOT NULL DEFAULT 'put',
			nonce TEXT NOT NULL,
			data TEXT NOT NULL,
			updated INTEGER NOT NULL,
			PRIMARY KEY (account, kind))`); err != nil {
		return err
	}
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS synckeys (
			account TEXT NOT NULL,
			device TEXT NOT NULL,
			key TEXT NOT NULL,
			sig TEXT NOT NULL,
			PRIMARY KEY (account, device))`); err != nil {
		return err
	}
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS wraps (
			account TEXT NOT NULL,
			device TEXT NOT NULL,
			epoch INTEGER NOT NULL,
			key TEXT NOT NULL,
			nonce TEXT NOT NULL,
			data TEXT NOT NULL,
			sig TEXT NOT NULL,
			updated INTEGER NOT NULL,
			PRIMARY KEY (account, device))`); err != nil {
		return err
	}
	return nil
}

// addColumn adds a column to an existing table only when it is missing: the migrations run on a
// database created by an older version of the relay.
func addColumn(db *sql.DB, table, name, def string) error {
	var n int
	if err := db.QueryRow("SELECT COUNT(*) FROM pragma_table_info(?) WHERE name = ?", table, name).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	_, err := db.Exec(fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s %s", table, name, def))
	return err
}

// SyncEpoch is the epoch of the sync key of an account: the epoch of its stored revocation list.
func (s *Store) SyncEpoch(account string) (int64, error) {
	var epoch int64
	err := s.db.QueryRow("SELECT epoch FROM accounts WHERE id=?", account).Scan(&epoch)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	return epoch, err
}

// hasDevice checks that a key holds a stored certificate of the account: the relay can only check
// a signature against a device it knows belongs to the account, so a signature from an unknown key
// proves nothing.
func (s *Store) hasDevice(account, key string) error {
	var n int
	if err := s.db.QueryRow("SELECT COUNT(*) FROM devices WHERE account=? AND key=?", account, key).Scan(&n); err != nil {
		return err
	}
	if n == 0 {
		return i18n.New("member not paired")
	}
	return nil
}

// StoreSyncKey stores the X25519 key a device published. The signature must verify against the
// device id and the device must hold a certificate of the account: a key posted by a device the
// relay cannot verify is refused, and a revoked device cannot publish one.
func (s *Store) StoreSyncKey(r *identity.SyncKeyRecord) error {
	if err := identity.VerifySig(r.Device, r.Sig, r.Canonical()); err != nil {
		return err
	}
	if err := s.hasDevice(r.Account, r.Device); err != nil {
		return err
	}
	list, err := s.Revocations(r.Account)
	if err != nil {
		return err
	}
	if list.Has(r.Device) {
		return i18n.New("device revoked") // wire text, see identity.ErrRevoked
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err = s.db.Exec("INSERT INTO synckeys (account, device, key, sig) VALUES (?,?,?,?) ON CONFLICT (account, device) DO UPDATE SET key=excluded.key, sig=excluded.sig",
		r.Account, r.Device, r.Key, r.Sig)
	return err
}

// SyncKey returns the published X25519 key of a device (nil when it has none).
func (s *Store) SyncKey(account, device string) (*identity.SyncKeyRecord, error) {
	var r identity.SyncKeyRecord
	err := s.db.QueryRow("SELECT account, device, key, sig FROM synckeys WHERE account=? AND device=?", account, device).
		Scan(&r.Account, &r.Device, &r.Key, &r.Sig)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &r, nil
}

// SyncKeys lists the published keys of an account, so an admin can wrap for each device.
func (s *Store) SyncKeys(account string) ([]identity.SyncKeyRecord, error) {
	rows, err := s.db.Query("SELECT account, device, key, sig FROM synckeys WHERE account=? ORDER BY device", account)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []identity.SyncKeyRecord
	for rows.Next() {
		var r identity.SyncKeyRecord
		if err := rows.Scan(&r.Account, &r.Device, &r.Key, &r.Sig); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// PutBlob stores one write of a blob, or deletes it. The signature must verify against the device
// that wrote it, the device must be a paired device of the account and not revoked, the epoch must
// be the current one, and the seq must be stored+1 (compare-and-swap): a relay that replays or
// skips a write changes nothing. The ciphertext is stored as it came, the relay cannot read it.
func (s *Store) PutBlob(r *identity.BlobRecord) (bool, error) {
	if err := identity.VerifySig(r.Device, r.Sig, r.Canonical()); err != nil {
		return false, err
	}
	if r.Op != identity.OpPut && r.Op != identity.OpDel {
		return false, i18n.Errorf("unknown blob op %q", r.Op)
	}
	if err := s.hasDevice(r.Account, r.Device); err != nil {
		return false, err
	}
	list, err := s.Revocations(r.Account)
	if err != nil {
		return false, err
	}
	if list.Has(r.Device) {
		return false, i18n.New("device revoked")
	}
	epoch, err := s.SyncEpoch(r.Account)
	if err != nil {
		return false, err
	}
	if r.Epoch != epoch {
		return false, i18n.Errorf("the blob is for epoch %d, the account is at epoch %d", r.Epoch, epoch)
	}
	if len(r.Data) > maxBlobSize {
		return false, i18n.Errorf("blob too large (%d characters max)", maxBlobSize)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var stored int64
	err = s.db.QueryRow("SELECT seq FROM blobs WHERE account=? AND kind=?", r.Account, r.Kind).Scan(&stored)
	if err == sql.ErrNoRows {
		stored = 0
	} else if err != nil {
		return false, err
	}
	if r.Seq != stored+1 {
		return false, nil
	}
	if r.Op == identity.OpDel {
		_, err = s.db.Exec("DELETE FROM blobs WHERE account=? AND kind=?", r.Account, r.Kind)
	} else {
		_, err = s.db.Exec("INSERT INTO blobs (account, kind, seq, epoch, device, op, nonce, data, updated) VALUES (?,?,?,?,?,?,?,?,?) ON CONFLICT (account, kind) DO UPDATE SET seq=excluded.seq, epoch=excluded.epoch, device=excluded.device, op=excluded.op, nonce=excluded.nonce, data=excluded.data, updated=excluded.updated",
			r.Account, r.Kind, r.Seq, r.Epoch, r.Device, r.Op, r.Nonce, r.Data, time.Now().Unix())
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// Blob returns the stored write of a kind (nil when there is none), with the time it was written.
func (s *Store) Blob(account, kind string) (*identity.BlobRecord, int64, error) {
	var r identity.BlobRecord
	var updated int64
	err := s.db.QueryRow("SELECT account, kind, seq, epoch, device, op, nonce, data, updated FROM blobs WHERE account=? AND kind=?", account, kind).
		Scan(&r.Account, &r.Kind, &r.Seq, &r.Epoch, &r.Device, &r.Op, &r.Nonce, &r.Data, &updated)
	if err == sql.ErrNoRows {
		return nil, 0, nil
	}
	if err != nil {
		return nil, 0, err
	}
	return &r, updated, nil
}

// Blobs returns the stored writes whose kind starts with prefix: a pod reads the project lists of
// the whole account with the prefix "projects:". The relay still only passes ciphertext.
func (s *Store) Blobs(account, prefix string) ([]*identity.BlobRecord, map[string]int64, error) {
	rows, err := s.db.Query("SELECT account, kind, seq, epoch, device, op, nonce, data, updated FROM blobs WHERE account=? AND kind LIKE ? ESCAPE '\\' ORDER BY kind",
		account, escapeLike(prefix)+"%")
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	var out []*identity.BlobRecord
	updated := map[string]int64{}
	for rows.Next() {
		var r identity.BlobRecord
		var u int64
		if err := rows.Scan(&r.Account, &r.Kind, &r.Seq, &r.Epoch, &r.Device, &r.Op, &r.Nonce, &r.Data, &u); err != nil {
			return nil, nil, err
		}
		out = append(out, &r)
		updated[r.Kind] = u
	}
	return out, updated, rows.Err()
}

// escapeLike quotes the wildcards a kind may hold, so a prefix matches literally.
func escapeLike(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `%`, `\%`)
	return strings.ReplaceAll(s, `_`, `\_`)
}

// PutWrap stores the key of an epoch wrapped for a device. Only a signature that verifies against
// the account is kept: the relay has no account key, so a wrap can only come from a device that
// holds it.
func (s *Store) PutWrap(w *identity.WrapRecord) error {
	if err := identity.VerifySig(w.Account, w.Sig, w.Canonical()); err != nil {
		return err
	}
	if len(w.Data) > maxBlobSize {
		return i18n.Errorf("wrap too large (%d characters max)", maxBlobSize)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec("INSERT INTO wraps (account, device, epoch, key, nonce, data, sig, updated) VALUES (?,?,?,?,?,?,?,?) ON CONFLICT (account, device) DO UPDATE SET epoch=excluded.epoch, key=excluded.key, nonce=excluded.nonce, data=excluded.data, sig=excluded.sig, updated=excluded.updated",
		w.Account, w.Device, w.Epoch, w.Key, w.Nonce, w.Data, w.Sig, time.Now().Unix())
	return err
}

// Wrap returns the key stored for a device (nil when it has none), with the time it was written.
func (s *Store) Wrap(account, device string) (*identity.WrapRecord, int64, error) {
	var w identity.WrapRecord
	var updated int64
	err := s.db.QueryRow("SELECT account, device, epoch, key, nonce, data, sig, updated FROM wraps WHERE account=? AND device=?", account, device).
		Scan(&w.Account, &w.Device, &w.Epoch, &w.Key, &w.Nonce, &w.Data, &w.Sig, &updated)
	if err == sql.ErrNoRows {
		return nil, 0, nil
	}
	if err != nil {
		return nil, 0, err
	}
	return &w, updated, nil
}

// Wraps lists the wraps of an account.
func (s *Store) Wraps(account string) ([]identity.WrapRecord, error) {
	rows, err := s.db.Query("SELECT account, device, epoch, key, nonce, data, sig FROM wraps WHERE account=? ORDER BY device", account)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []identity.WrapRecord
	for rows.Next() {
		var w identity.WrapRecord
		if err := rows.Scan(&w.Account, &w.Device, &w.Epoch, &w.Key, &w.Nonce, &w.Data, &w.Sig); err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, rows.Err()
}
