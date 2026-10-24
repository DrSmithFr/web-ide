package services

import (
	"io"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"

	"github.com/DrSmithFr/web-ide/lib/identity"
)

// Handler serves the requests of the other pods (Host.HandlePeer: the core let only a pod of
// the account through, its certificate in the context):
//
//	POST /<id>/$attach, /<id>/$detach  the pod links or unlinks the service
//	/<id>/<path>                       a request to the service, for an attached pod
func (m *Manager) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pod := identity.PeerOf(r.Context())
		if pod == nil {
			http.Error(w, "for the pods of the account only", http.StatusForbidden)
			return
		}
		id, rest, _ := strings.Cut(strings.TrimPrefix(r.URL.Path, "/"), "/")
		switch rest {
		case "$attach", "$detach":
			if r.Method != http.MethodPost {
				http.Error(w, "POST only", http.StatusMethodNotAllowed)
				return
			}
			if rest == "$detach" {
				m.Detach(id, pod.Key)
			} else if err := m.Attach(id, pod); err != nil {
				http.Error(w, err.Error(), http.StatusNotFound)
				return
			}
			w.WriteHeader(http.StatusNoContent)
			return
		}
		port, err := m.target(id, pod.Key)
		if err == errOff {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		if err != nil {
			http.Error(w, err.Error(), http.StatusForbidden)
			return
		}
		m.proxy(w, r, id, pod.Key, port, "/"+rest)
	})
}

// proxy sends a request to 127.0.0.1:port, its answer streamed back as it comes (an Ollama
// generation token by token), a WebSocket upgraded.
func (m *Manager) proxy(w http.ResponseWriter, r *http.Request, id, key string, port int, path string) {
	host := "127.0.0.1:" + strconv.Itoa(port)
	var bytes atomic.Int64
	rp := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(&url.URL{Scheme: "http", Host: host})
			pr.Out.URL.Path, pr.Out.URL.RawPath = path, ""
			pr.Out.Host = host
			// The cookie of this pod (ServeTrusted sets it) and its headers are not the service's.
			pr.Out.Header.Del("Cookie")
			pr.Out.Header.Del("X-Webide-Base")
		},
		FlushInterval: -1,
		ModifyResponse: func(resp *http.Response) error {
			resp.Body = &countBody{ReadCloser: resp.Body, n: &bytes}
			return nil
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			http.Error(w, "the service does not answer on "+host, http.StatusBadGateway)
		},
	}
	rp.ServeHTTP(w, r)
	m.count(id, key, 1, bytes.Load()+max(r.ContentLength, 0))
}

// countBody counts the bytes of an answer.
type countBody struct {
	io.ReadCloser
	n *atomic.Int64
}

func (b *countBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	b.n.Add(int64(n))
	return n, err
}

// Write is the upgraded connection of a WebSocket (ReverseProxy copies to it).
func (b *countBody) Write(p []byte) (int, error) {
	w, ok := b.ReadCloser.(io.Writer)
	if !ok {
		return 0, io.ErrClosedPipe
	}
	return w.Write(p)
}
