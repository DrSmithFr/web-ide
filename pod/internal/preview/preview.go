// Package preview serves the app of a project being developed on a temporary URL: a
// listener of the pod proxies to the port of the app (through SSH for a remote project),
// and Tailscale exposes it on the tailnet (serve), or on the internet (funnel) once public.
// A preview lives while the command of the app runs, 24 hours at most.
package preview

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/DrSmithFr/web-ide/pod/internal/i18n"
)

// MaxAge is how long a preview lives at most.
var MaxAge = 24 * time.Hour

// Serve ports of the private previews on the tailnet, and the ports Funnel allows for the
// public ones (443 is the IDE).
var (
	ServePorts  = [2]int{8401, 8499}
	FunnelPorts = []int{8443, 10000}
)

// Spec is what starts a preview: the command of the app and its port.
type Spec struct {
	Project string `json:"project"`
	Title   string `json:"title"`
	Command string `json:"command"`
	Cwd     string `json:"cwd"`
	AppPort int    `json:"port"`
}

func (s Spec) same(o Spec) bool {
	return s.Project == o.Project && s.Command == o.Command && s.Cwd == o.Cwd && s.AppPort == o.AppPort
}

// State is a running preview.
type State struct {
	Spec
	ID      string `json:"id"`
	Console string `json:"console"`
	URL     string `json:"url"`
	// PublicURL carries the token of the preview; empty while private.
	PublicURL string `json:"publicUrl,omitempty"`
	Public    bool   `json:"public"`
	// Tailscale is false when the preview is only on 127.0.0.1 of the pod.
	Tailscale bool  `json:"tailscale"`
	Started   int64 `json:"started"`
}

// Tailscale exposes the local listeners (a fake in tests).
type Tailscale interface {
	// Host is the DNS name of the machine on the tailnet; false without Tailscale.
	Host() (string, bool)
	Serve(port int, target string) error
	Funnel(port int, target string) error
	Off(port int, funnel bool) error
}

// Dialer reaches the app (through SSH for a remote project).
type Dialer func(ctx context.Context, network, addr string) (net.Conn, error)

type preview struct {
	State
	token  string
	l      net.Listener
	srv    *http.Server
	serve  int // tailnet port, 0 without Tailscale
	funnel int
	alive  func() bool
}

type Manager struct {
	// Authorized tells whether a request carries the cookie of the IDE.
	Authorized func(*http.Request) bool
	TS         Tailscale

	mu       sync.Mutex
	previews map[string]*preview
	notify   func()
	stop     chan struct{}
}

// New returns a manager calling changed after every change; it checks every second that
// the command of each preview still runs.
func New(ts Tailscale, authorized func(*http.Request) bool, changed func()) *Manager {
	m := &Manager{TS: ts, Authorized: authorized, previews: map[string]*preview{}, notify: changed, stop: make(chan struct{})}
	go m.watch()
	return m
}

func (m *Manager) changed() {
	if m.notify != nil {
		m.notify()
	}
}

func (m *Manager) watch() {
	t := time.NewTicker(time.Second)
	defer t.Stop()
	for {
		select {
		case <-m.stop:
			return
		case <-t.C:
			m.Check()
		}
	}
}

// Check stops the previews whose command has ended, or older than MaxAge.
func (m *Manager) Check() {
	m.mu.Lock()
	list := make([]*preview, 0, len(m.previews))
	for _, p := range m.previews {
		list = append(list, p)
	}
	m.mu.Unlock()
	var dead []string
	for _, p := range list {
		if (p.alive != nil && !p.alive()) || time.Since(time.UnixMilli(p.Started)) > MaxAge {
			dead = append(dead, p.ID)
		}
	}
	for _, id := range dead {
		m.Close(id)
	}
}

func newToken(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// Find returns the running preview of a spec.
func (m *Manager) Find(s Spec) (State, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, p := range m.previews {
		if p.same(s) {
			return p.State, true
		}
	}
	return State{}, false
}

// Start serves a preview of the app of spec, run by console; alive tells whether its
// command still runs.
func (m *Manager) Start(s Spec, console string, dial Dialer, alive func() bool) (State, error) {
	if s.AppPort < 1 || s.AppPort > 65535 {
		return State{}, i18n.New("ports go from 1 to 65535")
	}
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return State{}, err
	}
	p := &preview{State: State{Spec: s, ID: newToken(4), Console: console, Started: time.Now().UnixMilli()},
		token: newToken(16), l: l, alive: alive}
	p.srv = &http.Server{Handler: m.handler(p, dial), ReadHeaderTimeout: 30 * time.Second}
	go func() { _ = p.srv.Serve(l) }()
	local := "http://" + l.Addr().String()
	p.URL = local
	if host, ok := m.ts(); ok {
		m.mu.Lock()
		p.serve = m.freePort(ServePorts[0], ServePorts[1], false)
		m.mu.Unlock()
		if p.serve == 0 {
			_ = p.srv.Close()
			return State{}, i18n.New("no free port for a preview")
		}
		if err := m.TS.Serve(p.serve, local); err != nil {
			_ = p.srv.Close()
			return State{}, i18n.Errorf("tailscale serve failed: %w", err)
		}
		p.URL = fmt.Sprintf("https://%s:%d/", host, p.serve)
		p.Tailscale = true
	}
	m.mu.Lock()
	m.previews[p.ID] = p
	m.mu.Unlock()
	m.changed()
	return p.State, nil
}

func (m *Manager) ts() (string, bool) {
	if m.TS == nil {
		return "", false
	}
	return m.TS.Host()
}

// freePort is the first port of [lo, hi] (or of FunnelPorts) no preview uses. Called with m.mu held.
func (m *Manager) freePort(lo, hi int, funnel bool) int {
	used := map[int]bool{}
	for _, p := range m.previews {
		used[p.serve], used[p.funnel] = true, true
	}
	if funnel {
		for _, port := range FunnelPorts {
			if !used[port] {
				return port
			}
		}
		return 0
	}
	for port := lo; port <= hi; port++ {
		if !used[port] {
			return port
		}
	}
	return 0
}

// SetPublic opens a preview to the internet through Funnel (with its token), or closes it.
func (m *Manager) SetPublic(id string, public bool) (State, error) {
	m.mu.Lock()
	p := m.previews[id]
	if p == nil {
		m.mu.Unlock()
		return State{}, i18n.New("preview not found")
	}
	if p.Public == public {
		m.mu.Unlock()
		return p.State, nil
	}
	host, ok := m.ts()
	if public && !ok {
		m.mu.Unlock()
		return State{}, i18n.New("a public preview needs Tailscale Funnel")
	}
	if !public {
		port := p.funnel
		p.Public, p.funnel, p.PublicURL = false, 0, ""
		m.mu.Unlock()
		_ = m.TS.Off(port, true)
		m.changed()
		return p.State, nil
	}
	port := m.freePort(0, 0, true)
	if port == 0 {
		m.mu.Unlock()
		return State{}, i18n.New("two previews are public already (Funnel allows ports 8443 and 10000)")
	}
	p.funnel = port // reserved
	m.mu.Unlock()
	if err := m.TS.Funnel(port, "http://"+p.l.Addr().String()); err != nil {
		m.mu.Lock()
		p.funnel = 0
		m.mu.Unlock()
		return State{}, i18n.Errorf("tailscale funnel failed (is Funnel allowed in the tailnet policy?): %w", err)
	}
	m.mu.Lock()
	p.Public = true
	p.PublicURL = fmt.Sprintf("https://%s:%d/?t=%s", host, port, p.token)
	st := p.State
	m.mu.Unlock()
	m.changed()
	return st, nil
}

// Close stops a preview: listener, then its Tailscale entries.
func (m *Manager) Close(id string) {
	m.mu.Lock()
	p := m.previews[id]
	delete(m.previews, id)
	m.mu.Unlock()
	if p == nil {
		return
	}
	_ = p.srv.Close()
	if p.serve != 0 {
		_ = m.TS.Off(p.serve, false)
	}
	if p.funnel != 0 {
		_ = m.TS.Off(p.funnel, true)
	}
	m.changed()
}

// CloseAll stops every preview (pod shutdown).
func (m *Manager) CloseAll() {
	for _, st := range m.List() {
		m.Close(st.ID)
	}
}

// Shutdown stops the previews and the watch.
func (m *Manager) Shutdown() {
	select {
	case <-m.stop:
	default:
		close(m.stop)
	}
	m.CloseAll()
}

// List returns the previews, oldest first.
func (m *Manager) List() []State {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []State{}
	for _, p := range m.previews {
		out = append(out, p.State)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Started < out[j].Started })
	return out
}

// cookieName holds the token of a public preview in the browser of a visitor.
func (p *preview) cookieName() string { return "webide_preview_" + p.ID }

// handler checks the access, then proxies to the app.
func (m *Manager) handler(p *preview, dial Dialer) http.Handler {
	appHost := "localhost:" + strconv.Itoa(p.AppPort)
	target := &url.URL{Scheme: "http", Host: "127.0.0.1:" + strconv.Itoa(p.AppPort)}
	tr := http.DefaultTransport.(*http.Transport).Clone()
	if dial != nil {
		tr.DialContext = dial
	}
	own := appOrigins(p.AppPort)
	proxy := &httputil.ReverseProxy{
		Transport: tr,
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(target)
			pr.SetXForwarded()
			// Dev servers check the host (and the origin of their WebSocket).
			pr.Out.Host = appHost
			if pr.Out.Header.Get("Origin") != "" {
				pr.Out.Header.Set("Origin", "http://"+appHost)
			}
			stripCookies(pr.Out, p.cookieName())
		},
		// A redirection to the app's own address stays on the preview.
		ModifyResponse: func(res *http.Response) error {
			if loc := res.Header.Get("Location"); loc != "" {
				for _, o := range own {
					if strings.HasPrefix(loc, o) {
						rest := strings.TrimPrefix(loc, o)
						if rest == "" || rest[0] != '/' {
							rest = "/" + rest
						}
						res.Header.Set("Location", rest)
						break
					}
				}
			}
			return nil
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			page(w, r, http.StatusBadGateway, "The app does not answer yet on port {port}.", map[string]any{"port": p.AppPort})
		},
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if m.Authorized != nil && m.Authorized(r) {
			proxy.ServeHTTP(w, r)
			return
		}
		m.mu.Lock()
		public, token := p.Public, p.token
		m.mu.Unlock()
		if public {
			if t := r.URL.Query().Get("t"); t != "" && subtle.ConstantTimeCompare([]byte(t), []byte(token)) == 1 {
				host, _, _ := net.SplitHostPort(r.Host)
				secure := host != "127.0.0.1" && host != "localhost" && r.Host != "127.0.0.1" && r.Host != "localhost"
				http.SetCookie(w, &http.Cookie{Name: p.cookieName(), Value: token, Path: "/", HttpOnly: true,
					Secure: secure, SameSite: http.SameSiteLaxMode, MaxAge: int(MaxAge / time.Second)})
				q := r.URL.Query()
				q.Del("t")
				u := *r.URL
				u.RawQuery = q.Encode()
				http.Redirect(w, r, u.RequestURI(), http.StatusFound)
				return
			}
			if c, err := r.Cookie(p.cookieName()); err == nil && subtle.ConstantTimeCompare([]byte(c.Value), []byte(token)) == 1 {
				proxy.ServeHTTP(w, r)
				return
			}
		}
		page(w, r, http.StatusUnauthorized, "This preview is private: open it from the IDE.", nil)
	})
}

// appOrigins are the addresses the app may write in its redirections.
func appOrigins(port int) []string {
	ps := strconv.Itoa(port)
	return []string{"http://localhost:" + ps, "http://127.0.0.1:" + ps}
}

// stripCookies keeps the cookie of the preview from the app. The cookie of the IDE goes
// through: the app may be a Web IDE in development, and it runs on the machine of the user.
func stripCookies(r *http.Request, preview string) {
	cs := r.Cookies()
	if len(cs) == 0 {
		return
	}
	var kept []string
	for _, c := range cs {
		if c.Name != preview {
			kept = append(kept, c.Name+"="+c.Value)
		}
	}
	r.Header.Del("Cookie")
	if len(kept) > 0 {
		r.Header.Set("Cookie", strings.Join(kept, "; "))
	}
}

// page answers a short message in the language of the browser.
func page(w http.ResponseWriter, r *http.Request, code int, text string, vars map[string]any) {
	lang := "en"
	if strings.HasPrefix(strings.ToLower(r.Header.Get("Accept-Language")), "fr") {
		lang = "fr"
	}
	msg := i18n.T(lang, text)
	for k, v := range vars {
		msg = strings.ReplaceAll(msg, "{"+k+"}", fmt.Sprint(v))
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(code)
	fmt.Fprintf(w, `<!doctype html><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>Web IDE</title>
<style>:root{color-scheme:light dark}body{margin:0;min-height:100vh;display:grid;place-items:center;font:15px/1.5 system-ui,sans-serif}</style>
<p>%s</p>`, htmlEscape(msg))
}

func htmlEscape(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;").Replace(s)
}
