package services

import (
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"antennae.space/ide/lib/tunnel"
	"antennae.space/ide/pod/plugins"
	"antennae.space/ide/pod/store"
)

// fakePeers reaches one sharing pod (bravo, a real server of its routes) as alpha, or nothing
// while offline.
type fakePeers struct {
	mu      sync.Mutex
	bravo   *httptest.Server
	offline bool
}

func (f *fakePeers) Transport() http.RoundTripper {
	return roundTrip(func(r *http.Request) (*http.Response, error) {
		f.mu.Lock()
		srv, off := f.bravo, f.offline
		f.mu.Unlock()
		if off || r.URL.Host != bravo.Key {
			return nil, errors.New("offline")
		}
		u, _ := url.Parse(srv.URL)
		out := r.Clone(r.Context())
		out.URL.Scheme, out.URL.Host = "http", u.Host
		out.URL.Path = strings.TrimPrefix(out.URL.Path, "/_peer/services")
		return http.DefaultTransport.RoundTrip(out)
	})
}
func (f *fakePeers) Route(string) tunnel.Route { return tunnel.Route{Name: tunnel.RouteLAN} }
func (f *fakePeers) List() []plugins.PeerPod   { return nil }

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// sharing starts bravo: its services, its routes as alpha reaches them.
func sharing(t *testing.T, st *store.Store) (*Manager, *httptest.Server) {
	m, err := Load(st, func() {})
	if err != nil {
		t.Fatal(err)
	}
	srv := serve(m.Handler(), alpha)
	t.Cleanup(srv.Close)
	return m, srv
}

func getBody(t *testing.T, port int) (int, string) {
	t.Helper()
	resp, err := http.Get("http://127.0.0.1:" + strconv.Itoa(port) + "/api/tags")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func view(l *Links, id string) LinkView {
	for _, v := range l.List() {
		if v.ID == id {
			return v
		}
	}
	return LinkView{}
}

func TestLinks(t *testing.T) {
	// The service of bravo, on this machine too: its port is taken here, the link takes the next.
	back := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "ollama "+r.URL.Path)
	}))
	defer back.Close()
	bravoSt, _ := store.Open(t.TempDir())
	share, srv := sharing(t, bravoSt)
	s, _ := share.Save(Service{Name: "Ollama", Port: portOf(t, back.URL), Enabled: true})
	peers := &fakePeers{bravo: srv}

	st, _ := store.Open(t.TempDir())
	links, _ := LoadLinks(st, peers, func() {})
	defer links.Stop()
	v, err := links.Set(Link{Pod: bravo.Key, PodName: "bravo", Service: s.ID, Name: "Ollama", On: true})
	if err != nil || v.State != StateOn || v.Listen <= s.Port || v.Listen > s.Port+20 {
		t.Fatalf("linked: %+v %v", v, err)
	}
	if code, body := getBody(t, v.Listen); code != 200 || body != "ollama /api/tags" {
		t.Fatalf("through the link: %d %q", code, body)
	}
	if a := share.List()[0].Attached; len(a) != 1 || a[0].Requests != 1 {
		t.Fatalf("bravo counts alpha: %+v", a)
	}
	if v = view(links, v.ID); v.Requests != 1 || v.Bytes == 0 || v.Route.Name != tunnel.RouteLAN {
		t.Fatalf("the link counts: %+v", v)
	}

	// bravo restarted: it forgot alpha, which attaches again by itself.
	share2, srv2 := sharing(t, bravoSt)
	peers.mu.Lock()
	peers.bravo = srv2
	peers.mu.Unlock()
	if code, body := getBody(t, v.Listen); code != 200 || body != "ollama /api/tags" {
		t.Fatalf("after a restart of bravo: %d %q", code, body)
	}

	// Offline: 502, the listener stays.
	peers.mu.Lock()
	peers.offline = true
	peers.mu.Unlock()
	if code, body := getBody(t, v.Listen); code != http.StatusBadGateway || !strings.Contains(body, "bravo is offline") {
		t.Fatalf("offline: %d %q", code, body)
	}
	if st := view(links, v.ID).State; st != StateOffline {
		t.Fatalf("state offline: %s", st)
	}
	peers.mu.Lock()
	peers.offline = false
	peers.mu.Unlock()

	// Cut by bravo: the next request turns the link off, its port is freed.
	share2.Cut(s.ID, alpha.Key)
	if code, _ := getBody(t, v.Listen); code != http.StatusForbidden {
		t.Fatalf("after Cut: %d", code)
	}
	deadline := time.Now().Add(3 * time.Second)
	for view(links, v.ID).State != StateCut && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if got := view(links, v.ID); got.State != StateCut || got.On {
		t.Fatalf("cut: %+v", got)
	}
	if ln, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(v.Listen)); err != nil {
		t.Fatalf("the port of a cut link is not freed: %v", err)
	} else {
		ln.Close()
	}
	// Turned on again by the user: bravo takes it back.
	if v, _ = links.Set(Link{Pod: bravo.Key, PodName: "bravo", Service: s.ID, Name: "Ollama", On: true}); v.State != StateOn {
		t.Fatalf("on again after a cut: %+v", v)
	}

	// A port typed and taken.
	taken, _ := net.Listen("tcp", "127.0.0.1:0")
	defer taken.Close()
	tp := taken.Addr().(*net.TCPAddr).Port
	if v, _ = links.Set(Link{Pod: bravo.Key, PodName: "bravo", Service: s.ID, Name: "Ollama", Port: tp, On: true}); v.State != StatePortTaken {
		t.Fatalf("a typed port taken: %+v", v)
	}

	// Off: the port freed, the pod told.
	if v, _ = links.Set(Link{Pod: bravo.Key, PodName: "bravo", Service: s.ID, Name: "Ollama", On: false}); v.State != StateOff {
		t.Fatalf("off: %+v", v)
	}
	if a := share2.List()[0].Attached; len(a) != 0 {
		t.Fatalf("bravo still has alpha attached: %+v", a)
	}

	// On across a restart of this pod.
	links.Set(Link{Pod: bravo.Key, PodName: "bravo", Service: s.ID, Name: "Ollama", On: true})
	links.Stop()
	again, _ := LoadLinks(st, peers, func() {})
	again.Start()
	defer again.Stop()
	deadline = time.Now().Add(3 * time.Second)
	for view(again, v.ID).State != StateOn && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if got := view(again, v.ID); got.State != StateOn {
		t.Fatalf("after a restart of this pod: %+v", got)
	} else if code, _ := getBody(t, got.Listen); code != 200 {
		t.Fatalf("its listener after a restart: %d", code)
	}
}
