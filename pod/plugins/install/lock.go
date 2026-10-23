package install

import (
	"github.com/DrSmithFr/web-ide/pod/store"
)

// LockFile is the lock of the external plugins of the pod, in its data folder: per pod, not
// synced (a project opened on a pod lacking one of its plugins says so).
const LockFile = "plugins.lock"

// Lock is plugins.lock: what is installed, from where, at which version and hash, with the
// grants the user accepted.
type Lock struct {
	Plugins map[string]Locked `json:"plugins"`
}

// Locked is an installed external plugin. SHA256 is the hash of its release archive: a
// version is never taken again under the same tag with another hash.
type Locked struct {
	URL        string   `json:"url"`
	Constraint string   `json:"constraint"`
	Version    string   `json:"version"`
	SHA256     string   `json:"sha256"`
	Grants     []string `json:"grants"`
}

// ReadLock reads the lock of the pod (empty when there is none).
func ReadLock(st *store.Store) (Lock, error) {
	l := Lock{Plugins: map[string]Locked{}}
	if err := st.ReadJSON(LockFile, &l); err != nil && !store.IsNotExist(err) {
		return l, err
	}
	if l.Plugins == nil {
		l.Plugins = map[string]Locked{}
	}
	return l, nil
}

// WriteLock replaces the lock (an atomic write).
func WriteLock(st *store.Store, l Lock) error { return st.WriteJSON(LockFile, l) }
