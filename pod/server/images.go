package server

import (
	"encoding/base64"
	"path"
	"strings"

	"github.com/DrSmithFr/web-ide/pod/assistant"

	"github.com/DrSmithFr/web-ide/lib/i18n"
	"github.com/DrSmithFr/web-ide/pod/fsx"
)

// readImage gives an image file as a data URL (the page shows it in an img, where an SVG
// runs no script). Other files and images over assistant.MaxImage are refused.
func readImage(fs fsx.FS, p string) (map[string]string, error) {
	mime, ok := assistant.ImageTypes[strings.ToLower(path.Ext(p))]
	if !ok {
		return nil, i18n.New("not an image")
	}
	st, err := fs.Stat(p)
	if err != nil {
		return nil, err
	}
	if st.Dir || st.Size > assistant.MaxImage {
		return nil, i18n.New("image too large")
	}
	data, err := fs.Read(p)
	if err != nil {
		return nil, err
	}
	return map[string]string{"url": "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(data)}, nil
}
