package runtime

import (
	"context"
	"io/fs"
	"path"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/DrSmithFr/web-ide/pod/execx"
)

// The list of the files of a project (Files in related.go caches it): the explorer, the Files tab
// of Search Everywhere (search.files), the find_files tool of the assistant. The text search
// is the Search plugin's.

// SkipDirs are the folders never listed nor searched.
var SkipDirs = map[string]bool{".git": true, "node_modules": true, ".ide": true, ".idea": true, "__pycache__": true, ".venv": true, ".cache": true}

// InExcluded tells whether a path is one of the folders, or inside one.
func InExcluded(p string, folders []string) bool {
	for _, f := range folders {
		if p == f || strings.HasPrefix(p, f+"/") {
			return true
		}
	}
	return false
}

// WithoutExcluded drops the paths inside the folders.
func WithoutExcluded(paths, folders []string) []string {
	if len(folders) == 0 {
		return paths
	}
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		if !InExcluded(p, folders) {
			out = append(out, p)
		}
	}
	return out
}

const maxFiles = 50000

// listLocal lists the files of a local project.
func listLocal(ctx context.Context, root string) ([]string, error) {
	var out []string
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if SkipDirs[d.Name()] && p != root {
				return filepath.SkipDir
			}
			return nil
		}
		out = append(out, p)
		if len(out) >= maxFiles {
			return filepath.SkipAll
		}
		return nil
	})
	return out, err
}

// listRemote lists the files of an SSH project with find.
func listRemote(ctx context.Context, r execx.Runner, root string) ([]string, error) {
	var prune []string
	for d := range SkipDirs {
		if len(prune) > 0 {
			prune = append(prune, "-o")
		}
		prune = append(prune, "-name", d)
	}
	args := append([]string{"find", ".", "-type", "d", "("}, prune...)
	args = append(args, ")", "-prune", "-o", "-type", "f", "-print")
	out, err := r.Output(ctx, []string{"sh", "-c", execx.Join(args) + " | head -n " + strconv.Itoa(maxFiles)}, root)
	if err != nil && len(out) == 0 {
		return nil, err
	}
	var files []string
	for _, l := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if l != "" {
			files = append(files, path.Join(root, l))
		}
	}
	return files, nil
}
