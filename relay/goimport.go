package relay

import (
	"html"
	"net/http"
)

// The import path of the Go module of the IDE, a name of the hosted relay rather than of GitHub:
// the repository may move, the path stays.
const (
	goModule = "antennae.space/ide"
	goRepo   = "https://github.com/antennae-space/web-ide"
)

// serveGoImport answers `go get antennae.space/ide/...` (`?go-get=1`) with the go-import and
// go-source tags naming the repository; a browser is sent to the repository itself.
func serveGoImport(w http.ResponseWriter, r *http.Request) {
	if r.URL.Query().Get("go-get") != "1" {
		http.Redirect(w, r, goRepo, http.StatusFound)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	repo := html.EscapeString(goRepo)
	_, _ = w.Write([]byte(`<!doctype html><meta name="go-import" content="` + goModule + ` git ` + repo + `">` +
		`<meta name="go-source" content="` + goModule + ` ` + repo + ` ` + repo + `/tree/main{/dir} ` + repo + `/blob/main{/dir}/{file}#L{line}">`))
}
