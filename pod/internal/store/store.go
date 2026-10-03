// Package store persists the pod state as JSON files under ~/.web-ide.
package store

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
)

type Store struct {
	dir string
	mu  sync.Mutex
}

func Open(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	return &Store{dir: dir}, nil
}

func (s *Store) Dir() string { return s.dir }

// Path returns an absolute path inside the store.
func (s *Store) Path(parts ...string) string {
	return filepath.Join(append([]string{s.dir}, parts...)...)
}

// ReadJSON decodes name into v. A missing file returns an error matching os.ErrNotExist.
func (s *Store) ReadJSON(name string, v any) error {
	data, err := os.ReadFile(s.Path(name))
	if err != nil {
		return err
	}
	return json.Unmarshal(data, v)
}

// WriteJSON atomically replaces name with the JSON encoding of v.
func (s *Store) WriteJSON(name string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return s.WriteFile(name, data)
}

func (s *Store) ReadFile(name string) ([]byte, error) { return os.ReadFile(s.Path(name)) }

func (s *Store) WriteFile(name string, data []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	path := s.Path(name)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func (s *Store) Remove(name string) error {
	err := os.Remove(s.Path(name))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func IsNotExist(err error) bool { return errors.Is(err, os.ErrNotExist) }
