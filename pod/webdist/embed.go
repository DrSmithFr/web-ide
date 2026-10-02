// Package webdist embeds the compiled web app (built by Vite into dist/).
package webdist

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var files embed.FS

func FS() fs.FS {
	sub, _ := fs.Sub(files, "dist")
	return sub
}
