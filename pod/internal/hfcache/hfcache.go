// Package hfcache serves model files of the Hugging Face hub from a cache on disk, filled
// on first use. The page (speech recognition in the browser) then never contacts an
// outside service itself, and works offline once the model has been downloaded.
package hfcache

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Prefix of the URLs handled: /models/hf/<org>/<name>/resolve/<rev>/<file path>.
const Prefix = "/models/hf/"

var segment = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

type Cache struct {
	Dir    string // e.g. ~/.web-ide/models/hf
	Remote string // https://huggingface.co
	Client *http.Client

	mu    sync.Mutex
	locks map[string]*sync.Mutex
}

func New(dir string) *Cache {
	return &Cache{Dir: dir, Remote: "https://huggingface.co", Client: &http.Client{Timeout: 0}, locks: map[string]*sync.Mutex{}}
}

// parse returns the repository ("org/name"), the revision and the file path of a URL path.
func parse(p string) (repo, rev, file string, err error) {
	parts := strings.Split(strings.TrimPrefix(p, Prefix), "/")
	if len(parts) < 5 || parts[2] != "resolve" {
		return "", "", "", errors.New("chemin de modèle invalide")
	}
	for _, s := range parts {
		if s != "resolve" && !segment.MatchString(s) {
			return "", "", "", errors.New("chemin de modèle invalide")
		}
	}
	return parts[0] + "/" + parts[1], parts[3], strings.Join(parts[4:], "/"), nil
}

func (c *Cache) lock(key string) *sync.Mutex {
	c.mu.Lock()
	defer c.mu.Unlock()
	l := c.locks[key]
	if l == nil {
		l = &sync.Mutex{}
		c.locks[key] = l
	}
	return l
}

func (c *Cache) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "méthode non permise", http.StatusMethodNotAllowed)
		return
	}
	repo, rev, file, err := parse(r.URL.Path)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	local := filepath.Join(c.Dir, filepath.FromSlash(repo), rev, filepath.FromSlash(file))
	// One download per file: a second request waits for it, then reads the cache.
	l := c.lock(local)
	l.Lock()
	defer l.Unlock()
	if _, err := os.Stat(local); err != nil {
		if err := c.download(r.Context(), repo, rev, file, local, w); err != nil {
			var nf notFound
			if errors.As(err, &nf) {
				http.Error(w, "fichier absent du modèle", http.StatusNotFound)
			} else if !errors.Is(err, errStreamed) {
				http.Error(w, err.Error(), http.StatusBadGateway)
			}
		}
		return
	}
	w.Header().Set("Cache-Control", "no-cache")
	http.ServeFile(w, r, local)
}

type notFound struct{}

func (notFound) Error() string { return "absent" }

// errStreamed: the download failed after the answer had started (nothing more to send).
var errStreamed = errors.New("transfert interrompu")

// download fetches the file and streams it to the page while writing the cache.
func (c *Cache) download(ctx context.Context, repo, rev, file, local string, w http.ResponseWriter) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("%s/%s/resolve/%s/%s", c.Remote, repo, rev, file), nil)
	if err != nil {
		return err
	}
	resp, err := c.Client.Do(req)
	if err != nil {
		return fmt.Errorf("téléchargement de %s impossible : %v", file, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusUnauthorized {
		// The hub answers 401 for missing repositories.
		return notFound{}
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("téléchargement de %s : HTTP %d", file, resp.StatusCode)
	}
	if err := os.MkdirAll(filepath.Dir(local), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(local), ".part-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if resp.ContentLength >= 0 {
		w.Header().Set("Content-Length", strconv.FormatInt(resp.ContentLength, 10))
	}
	if ct := resp.Header.Get("Content-Type"); ct != "" {
		w.Header().Set("Content-Type", ct)
	}
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	n, err := io.Copy(io.MultiWriter(tmp, w), resp.Body)
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err == nil && resp.ContentLength >= 0 && n != resp.ContentLength {
		err = io.ErrUnexpectedEOF
	}
	if err != nil {
		return errStreamed
	}
	return os.Rename(tmp.Name(), local)
}

// Entry is a cached model with its size on disk.
type Entry struct {
	Repo     string `json:"repo"`
	Size     int64  `json:"size"`
	Modified int64  `json:"modified"`
}

// List returns the cached repositories.
func (c *Cache) List() ([]Entry, error) {
	orgs, err := os.ReadDir(c.Dir)
	if errors.Is(err, fs.ErrNotExist) {
		return []Entry{}, nil
	}
	if err != nil {
		return nil, err
	}
	out := []Entry{}
	for _, o := range orgs {
		if !o.IsDir() {
			continue
		}
		names, _ := os.ReadDir(filepath.Join(c.Dir, o.Name()))
		for _, n := range names {
			if !n.IsDir() {
				continue
			}
			e := Entry{Repo: o.Name() + "/" + n.Name()}
			var latest time.Time
			_ = filepath.WalkDir(filepath.Join(c.Dir, o.Name(), n.Name()), func(_ string, d fs.DirEntry, err error) error {
				if err == nil && !d.IsDir() {
					if fi, err := d.Info(); err == nil {
						e.Size += fi.Size()
						if fi.ModTime().After(latest) {
							latest = fi.ModTime()
						}
					}
				}
				return nil
			})
			e.Modified = latest.UnixMilli()
			out = append(out, e)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Repo < out[j].Repo })
	return out, nil
}

// Delete removes a cached repository.
func (c *Cache) Delete(repo string) error {
	parts := strings.Split(repo, "/")
	if len(parts) != 2 || !segment.MatchString(parts[0]) || !segment.MatchString(parts[1]) {
		return errors.New("modèle invalide")
	}
	if err := os.RemoveAll(filepath.Join(c.Dir, parts[0], parts[1])); err != nil {
		return err
	}
	_ = os.Remove(filepath.Join(c.Dir, parts[0])) // only when empty
	return nil
}
