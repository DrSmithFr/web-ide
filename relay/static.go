package relay

import (
	"bytes"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"io"
	"io/fs"
	"maps"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"antennae.space/ide/lib/identity"
)

//go:embed all:web
var webFS embed.FS

var (
	bootstrapHTML []byte
	bootstrapJS   []byte
	bootstrapCSS  []byte
	routesJS      []byte
	swJS          []byte
	githubDoneJS  []byte
)

func init() {
	bootstrapHTML, _ = io.ReadAll(mustRead("bootstrap.html"))
	bootstrapJS, _ = io.ReadAll(mustRead("bootstrap.js"))
	bootstrapCSS, _ = io.ReadAll(mustRead("bootstrap.css"))
	routesJS, _ = io.ReadAll(mustRead("routes.js"))
	swJS, _ = io.ReadAll(mustRead("sw.js"))
	githubDoneJS, _ = io.ReadAll(mustRead("github-done.js"))
}

func mustRead(name string) io.Reader {
	b, err := webFS.ReadFile("web/" + name)
	if err != nil {
		panic("relay: " + name + ": " + err.Error())
	}
	return strings.NewReader(string(b))
}

// Handler is the relay's HTTP handler: signaling, the bootstrap page and the Service
// Worker. The app itself is never served here: the browser loads it from the pod through
// the tunnel (the Service Worker forwards the fetches), so only the bootstrap paths and
// the pod routes are handled.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/_relay/pod", s.servePod)
	mux.HandleFunc("/_relay/signal", s.serveSignal)
	mux.HandleFunc("/_relay/identity", s.serveIdentity)
	mux.HandleFunc("/_relay/bootstrap.js", staticJS(bootstrapJS))
	// The styles of the page in a file of their own: nothing inline, so a strict CSP fits.
	mux.HandleFunc("/_relay/bootstrap.css", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/css; charset=utf-8")
		_, _ = w.Write(bootstrapCSS)
	})
	// The routing decisions of the bootstrap page, in their own module: the same file is run by
	// node --test (scripts/routes-test.mjs) without a browser.
	mux.HandleFunc("/_relay/routes.js", staticJS(routesJS))
	// The identity module and the BIP39 wordlist come from the identity package: the bootstrap
	// page and the app import the same file, so the browser and the pod cannot disagree on a
	// format.
	mux.HandleFunc("/_relay/identity.js", staticJS(identity.ModuleJS()))
	mux.HandleFunc("/_relay/bip39.txt", s.serveWordlist)
	// The anonymous status of the relay (status.go): up, version, pods online, accounts seen.
	mux.HandleFunc("/_relay/status", s.serveStatus)
	mux.HandleFunc("/_relay/push/key", s.servePushKey)
	mux.HandleFunc("/_relay/push/subscribe", s.servePushSubscribe)
	mux.HandleFunc("/_relay/push/unsubscribe", s.servePushUnsubscribe)
	mux.HandleFunc("/sw.js", staticJS(s.serviceWorker()))
	// The import path of the Go module (goimport.go).
	mux.HandleFunc("/ide", serveGoImport)
	mux.HandleFunc("/ide/", serveGoImport)
	// The GitHub link of an account (milestone 5): the relay's own login page, the callback
	// GitHub sends the browser back to, and the sponsorship webhook. Served only when the relay
	// was given a GitHub app.
	if s.GitHub != nil {
		mux.HandleFunc("/_relay/github/login", s.serveGitHubLogin)
		mux.HandleFunc("/_relay/github/callback", s.serveGitHubCallback)
		mux.HandleFunc("/_relay/github/webhook", s.serveGitHubWebhook)
		mux.HandleFunc("/_relay/github/done.js", staticJS(githubDoneJS))
	}
	for p := range appFiles {
		mux.HandleFunc(p, s.serveAppFile)
	}
	mux.HandleFunc("/", s.bootstrapPage)
	return csp(mux)
}

// cspPolicy is the Content-Security-Policy of the pages of the relay: everything they load comes
// from this origin, nothing inline, no object, no framing. The bootstrap page is written to fit
// it — its styles, its script and its routing module are files of their own.
const cspPolicy = "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; " +
	"connect-src 'self'; worker-src 'self'; object-src 'none'; base-uri 'self'; form-action 'self'; frame-ancestors 'none'"

// csp puts the policy on every answer of the relay, and HSTS on its TLS answers: the app of the
// user is opened over https, a downgrade would carry their token.
func csp(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Security-Policy", cspPolicy)
		if r.TLS != nil {
			w.Header().Set("Strict-Transport-Security", "max-age=31536000")
		}
		h.ServeHTTP(w, r)
	})
}

// bootstrapPage serves the bootstrap HTML for the app routes (no file extension). Asset
// paths (extensions) are not served: they are answered by the Service Worker from the pod.
func (s *Server) bootstrapPage(w http.ResponseWriter, r *http.Request) {
	if p := r.URL.Path; strings.Contains(p[strings.LastIndexByte(p, '/')+1:], ".") {
		http.NotFound(w, r)
		return
	}
	// A link to a page of a pod in a browser whose Service Worker is not installed yet: the
	// bootstrap page opens that path in its frame once the tunnel is up.
	if strings.HasPrefix(r.URL.Path, "/pod/") {
		http.Redirect(w, r, "/#open="+url.QueryEscape(r.URL.RequestURI()), http.StatusFound)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = io.Copy(w, strings.NewReader(string(bootstrapHTML)))
}

// appFiles are the manifest of the installable app (PWA) and its icons: the same files as the
// pod's (web/public), taken from its front end, so the bootstrap page installs as the app.
var appFiles = map[string]string{
	"/manifest.webmanifest": "application/manifest+json",
	"/icon.svg":             "image/svg+xml",
	"/icon-192.png":         "image/png",
	"/icon-512.png":         "image/png",
}

func (s *Server) serveAppFile(w http.ResponseWriter, r *http.Request) {
	if s.App == nil {
		http.NotFound(w, r)
		return
	}
	b, err := fs.ReadFile(s.App, strings.TrimPrefix(r.URL.Path, "/"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", appFiles[r.URL.Path])
	_, _ = w.Write(b)
}

// serviceWorker is sw.js with the hash of the shell it keeps (#73): the bootstrap page, its
// scripts, the identity module, the wordlist and the installable app. A relay whose shell changed
// serves another Service Worker, so the browser installs it and fetches the new shell.
func (s *Server) serviceWorker() []byte {
	h := sha256.New()
	for _, b := range [][]byte{bootstrapHTML, bootstrapJS, bootstrapCSS, routesJS, identity.ModuleJS(), identity.WordlistBytes()} {
		h.Write(b)
	}
	for _, p := range slices.Sorted(maps.Keys(appFiles)) {
		if s.App != nil {
			b, _ := fs.ReadFile(s.App, strings.TrimPrefix(p, "/"))
			h.Write(b)
		}
	}
	shell := hex.EncodeToString(h.Sum(nil)[:8])
	return bytes.Replace(swJS, []byte("'__SHELL__'"), []byte("'"+shell+"'"), 1)
}

func staticJS(b []byte) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
		w.Header().Set("Service-Worker-Allowed", "/")
		_, _ = w.Write(b)
	}
}

// serveWordlist serves the BIP39 English list: the browser validates a phrase with the same
// 2048 words the pod embeds, so a phrase accepted on one side is accepted on the other.
func (s *Server) serveWordlist(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write(identity.WordlistBytes())
}
