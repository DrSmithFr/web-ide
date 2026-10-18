package lan

import (
	"html/template"
	"net/http"

	"github.com/DrSmithFr/web-ide/pod/internal/i18n"
)

// A gateway cannot serve the page of a pod that does not speak its multi-pod protocol: the front
// end of one pod talks to its own pod, but the channels of a window would not agree on the other
// side. Under the path of such a pod, the gateway answers this page instead of the app.
var updatePage = template.Must(template.New("update").Parse(`<!doctype html>
<html lang="{{.Lang}}"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1,maximum-scale=1,viewport-fit=cover">
<title>Web IDE · {{.Title}}</title>
<style>
:root{color-scheme:light dark;--bg:#f6f6f4;--fg:#1d1d1b;--muted:#6b6b66;--line:#d8d8d2}
@media (prefers-color-scheme:dark){:root{--bg:#18191b;--fg:#e6e6e3;--muted:#9a9a95;--line:#34363a}}
body{margin:0;min-height:100vh;display:grid;place-items:center;background:var(--bg);color:var(--fg);font:15px/1.5 system-ui,sans-serif;touch-action:manipulation}
main{width:min(460px,calc(100vw - 32px));display:grid;gap:12px}
h1{font-size:20px;margin:0}p{margin:0;color:var(--muted)}
a{color:inherit}
</style></head><body>
<main>
<h1>{{.Title}}</h1>
<p>{{.Text}}</p>
<p><a href="/">← {{.Back}}</a></p>
</main></body></html>`))

// UpdatePage answers the path of a pod the gateway cannot route to (multipod.Mismatch).
func UpdatePage(w http.ResponseWriter, r *http.Request, name, version string, outdated bool) {
	lang := i18n.Detect(r.Header.Get("Accept-Language"))
	text := i18n.T(lang, "%s runs a newer version (%s): this pod is the one to update.", name, version)
	if outdated {
		text = i18n.T(lang, "%s runs an older version (%s): its projects cannot be opened from this pod.", name, version)
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusBadGateway)
	_ = updatePage.Execute(w, struct {
		Lang, Title, Text, Back string
	}{Lang: lang, Title: i18n.T(lang, "Update this pod"), Text: text, Back: i18n.T(lang, "Back to the projects")})
}
