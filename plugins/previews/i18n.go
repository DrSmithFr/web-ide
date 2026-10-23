package previews

import (
	"embed"
	"io/fs"

	"github.com/DrSmithFr/web-ide/lib/i18n"
)

// The texts of the plugin (i18n/<lang>.json), shared by its messages and its page module.
//
//go:embed i18n/*.json
var catalogFiles embed.FS

func init() {
	sub, _ := fs.Sub(catalogFiles, "i18n")
	i18n.Add(sub)
}
