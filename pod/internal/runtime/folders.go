package runtime

import (
	"encoding/json"
	"path"
	"sort"
	"strings"

	"github.com/DrSmithFr/web-ide/pod/internal/fsx"
	"github.com/DrSmithFr/web-ide/pod/internal/i18n"
)

// Folder marks: source, test and excluded folders, kept in .ide/folders.json (shared
// with the repository). Excluded folders are skipped by the search and "go to file".
const (
	MarkSource   = "source"
	MarkTests    = "tests"
	MarkExcluded = "excluded"
)

type folderFile struct {
	Folders map[string]string `json:"folders"` // path relative to the root → mark
}

func (r *Runtime) foldersPath() string { return path.Join(r.Root, ".ide", "folders.json") }

// Folders returns the marked folders, by path relative to the project root.
func (r *Runtime) Folders() map[string]string {
	var f folderFile
	if data, err := r.FS.Read(r.foldersPath()); err == nil {
		_ = json.Unmarshal(data, &f)
	}
	if f.Folders == nil {
		f.Folders = map[string]string{}
	}
	return f.Folders
}

// MarkFolder sets the mark of a folder ("" removes it) and returns every mark.
func (r *Runtime) MarkFolder(abs, mark string) (map[string]string, error) {
	switch mark {
	case "", MarkSource, MarkTests, MarkExcluded:
	default:
		return nil, i18n.Errorf("unknown folder mark: %s", mark)
	}
	abs = path.Clean(abs)
	if abs == r.Root || !fsx.Within(r.Root, abs) {
		return nil, i18n.New("only a folder of the project can be marked")
	}
	rel := strings.TrimPrefix(abs, strings.TrimSuffix(r.Root, "/")+"/")
	m := r.Folders()
	if mark == "" {
		delete(m, rel)
	} else {
		m[rel] = mark
	}
	data, _ := json.MarshalIndent(folderFile{Folders: m}, "", "  ")
	if err := r.FS.Write(r.foldersPath(), append(data, '\n')); err != nil {
		return nil, err
	}
	return m, nil
}

// Excluded returns the absolute paths of the excluded folders, sorted.
func (r *Runtime) Excluded() []string {
	var out []string
	for rel, mark := range r.Folders() {
		if mark == MarkExcluded {
			out = append(out, path.Join(r.Root, rel))
		}
	}
	sort.Strings(out)
	return out
}
