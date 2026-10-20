// Package fsx gives local and SSH projects a common file interface (list, read, write, watch).
package fsx

import (
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

type Entry struct {
	Name    string `json:"name"`
	Path    string `json:"path"`
	Dir     bool   `json:"dir"`
	Link    bool   `json:"link,omitempty"`
	Size    int64  `json:"size"`
	ModTime int64  `json:"mtime"`
}

type FS interface {
	List(p string) ([]Entry, error)
	Read(p string) ([]byte, error)
	Write(p string, data []byte) error
	Stat(p string) (Entry, error)
	Mkdir(p string) error
	Remove(p string) error
	Rename(from, to string) error
	// Abs resolves p against the home directory of the target.
	Abs(p string) (string, error)
	NewWatcher() (Watcher, error)
	Close() error
}

// SortEntries puts directories first, then sorts by name, case-insensitively.
func SortEntries(es []Entry) {
	sort.Slice(es, func(i, j int) bool {
		if es[i].Dir != es[j].Dir {
			return es[i].Dir
		}
		return strings.ToLower(es[i].Name) < strings.ToLower(es[j].Name)
	})
}

// Within reports whether p is root or inside root.
func Within(root, p string) bool {
	root, p = path.Clean(root), path.Clean(p)
	return p == root || strings.HasPrefix(p, strings.TrimSuffix(root, "/")+"/")
}

func entryOf(p string, fi fs.FileInfo) Entry {
	return Entry{
		Name:    fi.Name(),
		Path:    p,
		Dir:     fi.IsDir(),
		Link:    fi.Mode()&fs.ModeSymlink != 0,
		Size:    fi.Size(),
		ModTime: fi.ModTime().UnixMilli(),
	}
}

// Local is the file system of the machine running the pod.
type Local struct{}

func (Local) List(p string) ([]Entry, error) {
	des, err := os.ReadDir(p)
	if err != nil {
		return nil, err
	}
	out := make([]Entry, 0, len(des))
	for _, de := range des {
		full := filepath.Join(p, de.Name())
		fi, err := de.Info()
		if err != nil {
			continue
		}
		e := entryOf(full, fi)
		if e.Link {
			if target, err := os.Stat(full); err == nil {
				e.Dir = target.IsDir()
				e.Size = target.Size()
			}
		}
		out = append(out, e)
	}
	SortEntries(out)
	return out, nil
}

func (Local) Read(p string) ([]byte, error) { return os.ReadFile(p) }

func (Local) Write(p string, data []byte) error {
	mode := fs.FileMode(0o644)
	if fi, err := os.Stat(p); err == nil {
		mode = fi.Mode().Perm()
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	// Rewriting in place keeps the inode, hard links and the watches of other tools.
	return os.WriteFile(p, data, mode)
}

func (Local) Stat(p string) (Entry, error) {
	fi, err := os.Stat(p)
	if err != nil {
		return Entry{}, err
	}
	return entryOf(p, fi), nil
}

func (Local) Mkdir(p string) error         { return os.MkdirAll(p, 0o755) }
func (Local) Remove(p string) error        { return os.RemoveAll(p) }
func (Local) Rename(from, to string) error { return os.Rename(from, to) }
func (Local) NewWatcher() (Watcher, error) { return newLocalWatcher() }
func (Local) Close() error                 { return nil }
func (Local) Abs(p string) (string, error) {
	if strings.HasPrefix(p, "~") {
		home, _ := os.UserHomeDir()
		p = filepath.Join(home, strings.TrimPrefix(p, "~"))
	}
	return filepath.Abs(p)
}
