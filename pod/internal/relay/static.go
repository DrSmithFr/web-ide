package relay

import (
	"embed"
	"io"
	"net/http"
	"strings"
)

//go:embed all:web
var webFS embed.FS

var (
	bootstrapHTML []byte
	bootstrapJS   []byte
	swJS          []byte
)

func init() {
	bootstrapHTML, _ = io.ReadAll(mustRead("bootstrap.html"))
	bootstrapJS, _ = io.ReadAll(mustRead("bootstrap.js"))
	swJS, _ = io.ReadAll(mustRead("sw.js"))
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
	mux.HandleFunc("/_relay/bootstrap.js", staticJS(bootstrapJS))
	mux.HandleFunc("/sw.js", staticJS(swJS))
	mux.HandleFunc("/", s.bootstrapPage)
	return mux
}

// bootstrapPage serves the bootstrap HTML for the app routes (no file extension). Asset
// paths (extensions) are not served: they are answered by the Service Worker from the pod.
func (s *Server) bootstrapPage(w http.ResponseWriter, r *http.Request) {
	if p := r.URL.Path; strings.Contains(p[strings.LastIndexByte(p, '/')+1:], ".") {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = io.Copy(w, strings.NewReader(string(bootstrapHTML)))
}

func staticJS(b []byte) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
		w.Header().Set("Service-Worker-Allowed", "/")
		_, _ = w.Write(b)
	}
}
