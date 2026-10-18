// Package docsdist embeds a copy of the documentation (docs/, GLOSSARY.md and the READMEs of the
// code, with their repo paths so relative links work), made into content/ by scripts/embed-docs.sh.
package docsdist

import (
	"embed"
	"io/fs"
)

//go:embed all:content
var files embed.FS

func FS() fs.FS {
	sub, _ := fs.Sub(files, "content")
	return sub
}
