package services

import (
	"bufio"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/DrSmithFr/web-ide/lib/identity"
	"github.com/DrSmithFr/web-ide/pod/store"
)

func newManager(t *testing.T) (*Manager, *store.Store, *int) {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	m, err := Load(st, func() { n++ })
	if err != nil {
		t.Fatal(err)
	}
	return m, st, &n
}

func TestSave(t *testing.T) {
	m, st, changed := newManager(t)
	for _, bad := range []Service{{Name: " ", Port: 80}, {Name: "a", Port: 0}, {Name: "a", Port: 70000}} {
		if _, err := m.Save(bad); err == nil {
			t.Fatalf("accepted %+v", bad)
		}
	}
	s, err := m.Save(Service{Name: "Ollama", Port: 11434, Enabled: true})
	if err != nil || s.ID == "" {
		t.Fatalf("save: %+v %v", s, err)
	}
	if _, err := m.Save(Service{Name: "Other", Port: 11434}); err == nil || !strings.Contains(err.Error(), "Ollama") {
		t.Fatalf("a port shared twice: %v", err)
	}
	if _, err := m.Save(Service{Name: "Cooling", Port: 8080}); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(st.Path(File))
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("services.json: %v %v", fi, err)
	}
	if p := m.Published(); len(p) != 1 || p[0].Name != "Ollama" {
		t.Fatalf("published, the ones on only: %+v", p)
	}
	again, _ := Load(st, func() {})
	if l := again.List(); len(l) != 2 || l[0].Name != "Cooling" || l[1].Port != 11434 {
		t.Fatalf("read back: %+v", l)
	}
	if err := m.Delete(s.ID); err != nil || len(m.List()) != 1 {
		t.Fatalf("delete: %v %+v", err, m.List())
	}
	if *changed != 3 {
		t.Fatalf("changes told: %d", *changed)
	}
}

var (
	alpha = &identity.Cert{Key: "alpha-key", Name: "alpha", Kind: identity.KindPod}
	bravo = &identity.Cert{Key: "bravo-key", Name: "bravo", Kind: identity.KindPod}
)

// ask sends a request to the handler as the core does: the peer in the context.
func ask(h http.Handler, peer *identity.Cert, method, path string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, nil)
	r = r.WithContext(identity.WithPeer(r.Context(), peer))
	r.Header.Set("Cookie", "webide=secret")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func portOf(t *testing.T, u string) int {
	p, _ := url.Parse(u)
	n, _ := strconv.Atoi(p.Port())
	return n
}

func TestHandler(t *testing.T) {
	var cookie, host string
	back := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie, host = r.Header.Get("Cookie"), r.Host
		fmt.Fprintf(w, "hello %s?%s", r.URL.Path, r.URL.RawQuery)
	}))
	defer back.Close()
	m, _, _ := newManager(t)
	s, _ := m.Save(Service{Name: "web", Port: portOf(t, back.URL), Enabled: true})
	h := m.Handler()

	if w := ask(h, nil, "GET", "/"+s.ID+"/x"); w.Code != http.StatusForbidden {
		t.Fatalf("no pod: %d", w.Code)
	}
	if w := ask(h, alpha, "GET", "/"+s.ID+"/x"); w.Code != http.StatusForbidden {
		t.Fatalf("not attached: %d", w.Code)
	}
	if w := ask(h, alpha, "POST", "/nope/$attach"); w.Code != http.StatusNotFound {
		t.Fatalf("attach to no service: %d", w.Code)
	}
	if w := ask(h, alpha, "POST", "/"+s.ID+"/$attach"); w.Code != http.StatusOK || w.Body.String() != `{"port":`+strconv.Itoa(s.Port)+`}` {
		t.Fatalf("attach: %d %s", w.Code, w.Body)
	}
	w := ask(h, alpha, "GET", "/"+s.ID+"/api/tags?a=1")
	if w.Code != 200 || w.Body.String() != "hello /api/tags?a=1" {
		t.Fatalf("proxied: %d %q", w.Code, w.Body)
	}
	if cookie != "" || host != "127.0.0.1:"+strconv.Itoa(s.Port) {
		t.Fatalf("the service saw cookie %q, host %q", cookie, host)
	}
	if w := ask(h, bravo, "GET", "/"+s.ID+"/x"); w.Code != http.StatusForbidden {
		t.Fatalf("another pod, not attached: %d", w.Code)
	}
	l := m.List()[0].Attached
	if len(l) != 1 || l[0].Name != "alpha" || l[0].Requests != 1 || l[0].Bytes != int64(len("hello /api/tags?a=1")) || l[0].Last == 0 {
		t.Fatalf("counters: %+v", l)
	}

	// Cut: the next request is refused, and so is an attach the pod makes by itself; one its
	// user asked brings it back.
	m.Cut(s.ID, alpha.Key)
	if w := ask(h, alpha, "GET", "/"+s.ID+"/x"); w.Code != http.StatusForbidden {
		t.Fatalf("after Cut: %d", w.Code)
	}
	if w := ask(h, alpha, "POST", "/"+s.ID+"/$attach"); w.Code != http.StatusConflict {
		t.Fatalf("attach by itself after Cut: %d", w.Code)
	}
	r := httptest.NewRequest("POST", "/"+s.ID+"/$attach", nil)
	r.Header.Set(RelinkHeader, "1")
	rw := httptest.NewRecorder()
	h.ServeHTTP(rw, r.WithContext(identity.WithPeer(r.Context(), alpha)))
	if rw.Code != http.StatusOK {
		t.Fatalf("attach the user asked after Cut: %d", rw.Code)
	}
	// Off: refused, and attaching too.
	s.Enabled = false
	if _, err := m.Save(s); err != nil {
		t.Fatal(err)
	}
	if w := ask(h, alpha, "GET", "/"+s.ID+"/x"); w.Code != http.StatusNotFound || w.Header().Get(ServiceHeader) != "off" {
		t.Fatalf("off: %d", w.Code)
	}
	if w := ask(h, alpha, "POST", "/"+s.ID+"/$attach"); w.Code != http.StatusNotFound {
		t.Fatalf("attach to an off service: %d", w.Code)
	}
}

// serve runs the handler as a real server, a pod's certificate in each request.
func serve(h http.Handler, peer *identity.Cert) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.ServeHTTP(w, r.WithContext(identity.WithPeer(r.Context(), peer)))
	}))
}

func TestStreamAndWebSocket(t *testing.T) {
	back := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/ws" {
			c, err := websocket.Accept(w, r, nil)
			if err != nil {
				return
			}
			defer c.CloseNow()
			for {
				typ, b, err := c.Read(r.Context())
				if err != nil {
					return
				}
				_ = c.Write(r.Context(), typ, append([]byte("echo "), b...))
			}
		}
		for i := range 3 {
			fmt.Fprintf(w, "token %d\n", i)
			w.(http.Flusher).Flush()
			time.Sleep(150 * time.Millisecond)
		}
	}))
	defer back.Close()
	m, _, _ := newManager(t)
	s, _ := m.Save(Service{Name: "ollama", Port: portOf(t, back.URL), Enabled: true})
	front := serve(m.Handler(), alpha)
	defer front.Close()
	if resp, err := http.Post(front.URL+"/"+s.ID+"/$attach", "", nil); err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("attach: %v %v", resp, err)
	}

	// Each token arrives on its own, before the next one is written.
	resp, err := http.Get(front.URL + "/" + s.ID + "/api/generate")
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	rd := bufio.NewReader(resp.Body)
	if line, _ := rd.ReadString('\n'); line != "token 0\n" || time.Since(start) > 120*time.Millisecond {
		t.Fatalf("first token %q after %v: not streamed", line, time.Since(start))
	}
	resp.Body.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(front.URL, "http")+"/"+s.ID+"/ws", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.CloseNow()
	if err := c.Write(ctx, websocket.MessageText, []byte("hi")); err != nil {
		t.Fatal(err)
	}
	if _, b, err := c.Read(ctx); err != nil || string(b) != "echo hi" {
		t.Fatalf("websocket: %q %v", b, err)
	}
}
