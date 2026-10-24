// Package idedir keeps the .ide folder of a project out of git: its .gitignore ignores
// everything (settings, bases, worktrees), so the IDE never adds files to the repository.
package idedir

import (
	"path"
	"strings"

	"antennae.space/ide/pod/fsx"
)

// Gitignore is the .ide/.gitignore the IDE writes.
const Gitignore = "# Settings and data of the Web IDE: local only\n*\n"

// Ignores tells whether a .gitignore text already ignores the whole folder.
func Ignores(text string) bool {
	for _, l := range strings.Split(text, "\n") {
		if strings.TrimSpace(l) == "*" {
			return true
		}
	}
	return false
}

// Ensure creates dir (a .ide folder) and its .gitignore ignoring everything; an older
// .gitignore listing some files only is replaced.
func Ensure(fs fsx.FS, dir string) error {
	gi := path.Join(dir, ".gitignore")
	if data, err := fs.Read(gi); err == nil && Ignores(string(data)) {
		return nil
	}
	if err := fs.Mkdir(dir); err != nil {
		return err
	}
	return fs.Write(gi, []byte(Gitignore))
}

// Local is Ensure on the local disk.
func Local(dir string) error { return Ensure(fsx.Local{}, dir) }
