// Package server serves the embedded web app and the WebSocket protocol of the pod.
package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/DrSmithFr/web-ide/pod/internal/clipboard"
	"html/template"
	"io/fs"
	"log"
	"net"
	"net/http"
	"path"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"

	"github.com/DrSmithFr/web-ide/pod/internal/config"
	"github.com/DrSmithFr/web-ide/pod/internal/db"
	"github.com/DrSmithFr/web-ide/pod/internal/desks"
	"github.com/DrSmithFr/web-ide/pod/internal/forwards"
	"github.com/DrSmithFr/web-ide/pod/internal/hfcache"
	"github.com/DrSmithFr/web-ide/pod/internal/i18n"
	"github.com/DrSmithFr/web-ide/pod/internal/kanban"
	"github.com/DrSmithFr/web-ide/pod/internal/keeper"
	"github.com/DrSmithFr/web-ide/pod/internal/llm"
	"github.com/DrSmithFr/web-ide/pod/internal/preview"
	"github.com/DrSmithFr/web-ide/pod/internal/projects"
	"github.com/DrSmithFr/web-ide/pod/internal/push"
	"github.com/DrSmithFr/web-ide/pod/internal/runtime"
	"github.com/DrSmithFr/web-ide/pod/internal/settings"
	"github.com/DrSmithFr/web-ide/pod/internal/settingsync"
	"github.com/DrSmithFr/web-ide/pod/internal/sshx"
	"github.com/DrSmithFr/web-ide/pod/internal/store"
	"github.com/DrSmithFr/web-ide/pod/internal/tunnel"
)

const cookieName = "webide_token"

type Server struct {
	Cfg      *config.Config
	Store    *store.Store
	Token    string
	Projects *projects.Registry
	Settings *settings.Settings
	Desks    *desks.Store
	// Clipboard is the history of the texts copied in the IDE, shared by every window.
	Clipboard *clipboard.History
	Pool      *sshx.Pool
	// Keeper runs the local terminals so that they survive the updates of the pod (nil: the
	// pod runs them).
	Keeper *keeper.Client
	LLM    *llm.Manager
	Kanban *kanban.Manager
	// Models caches the speech recognition models downloaded for the page.
	Models *hfcache.Cache
	// Sync is the settings sync loop of the pod (nil: the pod has no relay, so nothing is synced).
	Sync *settingsync.Hub
	// Forwards are the port forwards of the SSH projects.
	Forwards *forwards.Manager
	// Previews serve the apps of the projects on temporary URLs (Tailscale by default).
	Previews  *preview.Manager
	PreviewTS preview.Tailscale
	Static    fs.FS
	// Docs is the documentation embedded in the binary (pod/docsdist), served by docs.read.
	Docs fs.FS
	// AllowRemote accepts connections from other machines (the token is then the only protection).
	AllowRemote bool
	// Version of the pod, shown by the page.
	Version string
	// Name of the pod (-name, the host name by default): the badge of its projects on the home page.
	Name string
	// MCPBase is the address the agents of the Agents tool reach the pod at (default: from
	// Cfg.Addr), for its MCP endpoint.
	MCPBase string
	// RelayURL is the relay the pod registers with (empty: no relay, the pod is local only).
	RelayURL string
	// ForceTurn tells the bootstrap page to run the tunnel through TURN (no direct path).
	ForceTurn bool
	// RelayStatus reports the state of the remote access (nil: no relay). The page shows it
	// in Settings > Remote access.
	RelayStatus func() RelayInfo
	// RelaySet changes the relay the pod registers with (empty: remote access off), while the pod
	// runs, and reports the new state (Settings > Remote access).
	RelaySet func(url string) (RelayInfo, error)
	// LAN is the link to the pods of the account on the local network (nil: off).
	LAN LAN
	// Pods is the view of the other pods of the account (nil: the pod is not paired): the home page
	// shows one Projects block per pod (pod/internal/multipod).
	Pods Pods
	// ProjectsChanged is called when the projects of this pod changed: the debounced publish of
	// its list to the other pods.
	ProjectsChanged func()
	// Push holds the push subscriptions of the browsers, PushSend gives an encrypted
	// notification to the relay (both nil: no push, push.go).
	Push     *push.Store
	PushSend func(sub string, body []byte, ttl int, urgency, topic string) error

	// stopped: the pod stops (Shutdown); the runs are left as they are, to be taken back.
	stopped atomic.Bool
	// build is the fingerprint of the front end (Build), computed once.
	buildOnce sync.Once
	build     string

	mu       sync.Mutex
	clients  map[*Client]struct{}
	runtimes map[string]*runtime.Runtime
	opening  map[string]*sync.Mutex
	handlers map[string]handler
	// assistants are the conversations of the assistant running in the pod.
	assistants assistants
	// asks: the questions of the MCP tool ask_user, and the conversations asking (Questions tool).
	asks askRegistry
	// previewMu serializes preview.open (one command per app).
	previewMu sync.Mutex
	// forwardIdle closes the port forwards once no window is left (windowsChanged).
	forwardIdle *time.Timer
}

type handler func(ctx context.Context, c *Client, p json.RawMessage) (any, error)

// Sequential methods keep their order (document changes, keystrokes). Each group has its
// own queue, so a language server still starting does not delay the terminal.
var sequential = map[string]string{
	"lsp.notify": "lsp", "buffer.sync": "buffer", "console.input": "console", "console.resize": "console", "session.update": "session",
}

func (s *Server) Init() {
	s.clients = map[*Client]struct{}{}
	s.runtimes = map[string]*runtime.Runtime{}
	s.opening = map[string]*sync.Mutex{}
	s.handlers = map[string]handler{}
	s.assistants = assistants{runs: map[string]*assistantRun{}, slots: map[string]chan struct{}{}, waiting: map[string]int{}, ui: map[string]chan uiResult{}}
	if s.Kanban == nil {
		s.Kanban = kanban.NewManager(s.Store)
	}
	if s.Clipboard == nil {
		s.Clipboard = clipboard.Load(s.Store)
	}
	s.Forwards = forwards.New(func() { s.broadcast("tunnels.changed", nil, nil) })
	if s.PreviewTS == nil {
		s.PreviewTS = &preview.CLI{}
	}
	s.Previews = preview.New(s.PreviewTS, s.authorized, func() { s.broadcast("preview.changed", s.Previews.List(), nil) })
	s.registerGlobal()
	s.registerProject()
	s.registerDB()
	s.registerGit()
	s.registerIcons()
	s.registerWorktrees()
	s.registerLLM()
	s.registerAssistant()
	s.registerExec()
	s.registerDocker()
	s.registerDockerLogs()
	s.registerDockerDisk()
	s.registerForwards()
	s.registerIdeDir()
	s.registerCommands()
	s.registerAgents()
	s.registerQuestions()
	s.registerKanbanAgent()
	s.registerPreviews()
	s.registerKanban()
	s.registerMilestones()
	s.registerKanbanGit()
	s.registerRelay()
	s.registerLAN()
	s.registerDocs()
	s.registerSync()
	s.registerPods()
	s.registerPush()
}

func (s *Server) handle(name string, h handler) { s.handlers[name] = h }

// ---------- HTTP ----------

func (s *Server) authorized(r *http.Request) bool {
	c, err := r.Cookie(cookieName)
	return err == nil && subtle.ConstantTimeCompare([]byte(c.Value), []byte(s.Token)) == 1
}

func (s *Server) setCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{Name: cookieName, Value: s.Token, Path: "/", HttpOnly: true,
		SameSite: http.SameSiteStrictMode, MaxAge: 400 * 24 * 3600})
}

func loopback(r *http.Request) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return false
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

var tokenPage = template.Must(template.New("pair").Parse(`<!doctype html>
<html lang="{{.Lang}}"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1,maximum-scale=1,viewport-fit=cover">
<title>Web IDE · {{.T "access token"}}</title>
<style>
:root{color-scheme:light dark;--bg:#f6f6f4;--fg:#1d1d1b;--muted:#6b6b66;--line:#d8d8d2;--accent:#2f6fde}
@media (prefers-color-scheme:dark){:root{--bg:#18191b;--fg:#e6e6e3;--muted:#9a9a95;--line:#34363a;--accent:#6c9cff}}
body{margin:0;min-height:100vh;display:grid;place-items:center;background:var(--bg);color:var(--fg);font:15px/1.5 system-ui,sans-serif;touch-action:manipulation}
form{width:min(420px,calc(100vw - 32px));display:grid;gap:12px}
h1{font-size:20px;margin:0}p{margin:0;color:var(--muted)}
input{font:14px ui-monospace,monospace;padding:10px;border:1px solid var(--line);border-radius:6px;background:transparent;color:inherit}
button{padding:10px;border:0;border-radius:6px;background:var(--accent);color:#fff;font-weight:600;cursor:pointer}
.err{color:#d33}
</style></head><body>
<form method="post" action="/auth">
<h1>{{.T "Access token"}}</h1>
<p>{{.T "Paste the access token printed by the pod when it starts (also in"}} <code>~/.web-ide/token</code>).</p>
{{if .Error}}<p class="err">{{.T .Error}}</p>{{end}}
<input name="token" autocomplete="off" autofocus placeholder="{{.T "access token"}}">
<input type="hidden" name="next" value="/">
<button>{{.T "Open"}}</button>
</form></body></html>`))

// tokenView fills the page asking the access token in the language of the browser.
type tokenView struct {
	Lang  string
	Error string
}

func (v tokenView) T(text string) string { return i18n.T(v.Lang, text) }

func showAccessToken(w http.ResponseWriter, r *http.Request, errText string) {
	lang := i18n.Detect(r.Header.Get("Accept-Language"))
	_ = tokenPage.Execute(w, tokenView{Lang: lang, Error: errText})
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !s.AllowRemote && !loopback(r) {
		http.Error(w, "access restricted to the local machine", http.StatusForbidden)
		return
	}
	if tok := r.URL.Query().Get("token"); tok != "" {
		if subtle.ConstantTimeCompare([]byte(tok), []byte(s.Token)) == 1 {
			s.setCookie(w)
			q := r.URL.Query()
			q.Del("token")
			r.URL.RawQuery = q.Encode()
			http.Redirect(w, r, r.URL.RequestURI(), http.StatusFound)
			return
		}
	}
	// Claude Code authenticates with the token as a bearer, not with the cookie.
	if r.URL.Path == mcpPath {
		s.serveMCP(w, r)
		return
	}
	if r.URL.Path == openPath && (s.bearer(r) || s.authorized(r)) {
		s.serveOpen(w, r)
		return
	}
	if r.URL.Path == "/auth" && r.Method == http.MethodPost {
		if subtle.ConstantTimeCompare([]byte(strings.TrimSpace(r.FormValue("token"))), []byte(s.Token)) != 1 {
			w.WriteHeader(http.StatusUnauthorized)
			showAccessToken(w, r, "Invalid access token.")
			return
		}
		s.setCookie(w)
		http.Redirect(w, r, "/", http.StatusFound)
		return
	}
	// The browser fetches the manifest of the installable app without cookies: it and its
	// icons are served before the access token check (nothing private in them).
	if appFiles[r.URL.Path] {
		s.serveStatic(w, r)
		return
	}
	if !s.authorized(r) {
		if r.URL.Path == "/ws" {
			http.Error(w, "not paired", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusUnauthorized)
		showAccessToken(w, r, "")
		return
	}
	if r.URL.Path == "/ws" {
		s.serveWS(w, r)
		return
	}
	if strings.HasPrefix(r.URL.Path, peerPrefix) && s.LAN != nil {
		s.LAN.ServeGateway(w, r, "")
		return
	}
	// The identity module, the wordlist and the pairing link: the app runs on the pod origin
	// and imports the same file the bootstrap page loads from the relay.
	if r.URL.Path == "/_relay/identity.js" {
		s.serveIdentityModule(w, r)
		return
	}
	if r.URL.Path == "/_relay/bip39.txt" {
		s.serveWordlist(w, r)
		return
	}
	if r.URL.Path == "/_relay/identity" {
		s.serveIdentityLink(w, r)
		return
	}
	// A page served by this pod directly: the gateway port of another pod answers here
	// instead, with the way back to its home page.
	if r.URL.Path == "/_lan/gateway" {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte("{}"))
		return
	}
	if strings.HasPrefix(r.URL.Path, hfcache.Prefix) && s.Models != nil {
		s.Models.ServeHTTP(w, r)
		return
	}
	s.serveStatic(w, r)
}

// appFiles are the manifest of the installable app (PWA) and its icons (web/public).
var appFiles = map[string]bool{"/manifest.webmanifest": true, "/icon.svg": true, "/icon-192.png": true, "/icon-512.png": true}

func (s *Server) serveStatic(w http.ResponseWriter, r *http.Request) {
	p := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
	if p == "manifest.webmanifest" {
		w.Header().Set("Content-Type", "application/manifest+json")
	}
	if p != "" {
		if f, err := s.Static.Open(p); err == nil {
			st, _ := f.Stat()
			f.Close()
			if st != nil && !st.IsDir() {
				if strings.HasPrefix(p, "assets/") {
					w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
				}
				http.ServeFileFS(w, r, s.Static, p)
				return
			}
		}
	}
	// Routes of the single page app (/project/:id/...) all serve index.html.
	data, err := fs.ReadFile(s.Static, "index.html")
	if err != nil {
		http.Error(w, "front end not built: run `make build`", http.StatusInternalServerError)
		return
	}
	// The front end is built with relative paths (base ./ of Vite): its assets start from
	// the root, or from the path of a peer served through its gateway pod. The rest (the
	// manifest of the installed app) stays at the root of the origin.
	base := baseOf(r.Context())
	data = bytes.ReplaceAll(data, []byte(`="./assets/`), []byte(`="`+base+`/assets/`))
	data = bytes.ReplaceAll(data, []byte(`="./`), []byte(`="/`))
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	// The Service Worker of the relay keeps this page for the build it names (#73): a reload
	// through the tunnel then carries no static file at all.
	w.Header().Set("X-Webide-Build", s.Build())
	_, _ = w.Write(data)
}

// Build is the fingerprint of the front end: a hash of its index.html, which names every asset by
// the hash of its content. The pod tells it to the relay, and the Service Worker keeps the page of
// a pod under it: a development rebuild keeps its version, never its build.
func (s *Server) Build() string {
	s.buildOnce.Do(func() {
		if s.Static == nil {
			return
		}
		if b, err := fs.ReadFile(s.Static, "index.html"); err == nil {
			sum := sha256.Sum256(b)
			s.build = hex.EncodeToString(sum[:8])
		}
	})
	return s.build
}

// ---------- WebSocket ----------

// Client is a window seen from a request: its connection and the project the request
// runs in. A window has its own project (the one it opened, with its session) and may
// attach the other worktrees of the same repository: a request names one of them in
// `project`, and the events of their runtimes reach the window tagged with their project.
type Client struct {
	*window
	project string
}

// A window reads and writes whole messages: a WebSocket frame, or one message of a tunnel
// channel (a DataChannel of the relay). The JSON protocol is the same on both. Its rpc
// transport carries the requests; the extra channels of a tunnel (term:<id>, xfer:<n>,
// http:<n>) are transports of their own.
type window struct {
	id  string
	srv *Server
	rpc *transport
	ctx context.Context
	// attached are the worktree projects of the window besides its own (under srv.mu).
	attached map[string]bool
	// desk is the desk of its project the window shows (under srv.mu): its session.
	desk    int
	cancels sync.Map
	// streams holds the stop functions of the log streams of the window, by id.
	streams sync.Map
	// lang is the language of the window (messages are translated for it).
	lang atomic.Value
	// watching is the conversation of the assistant the window shows.
	watching atomic.Value
	// channels are the extra channels of the tunnel of this window (term:<id>, xfer:<n>),
	// by label. Empty on a WebSocket, where the rpc transport carries everything.
	mu       sync.Mutex
	channels map[string]*transport
	// queues are the sequential groups of the window (see sequential).
	queues map[string]*queue
	// pushDevice is the browser device of the window and visible whether it is shown: a device
	// with a visible window gets no push notification (push.go).
	pushDevice atomic.Value
	visible    atomic.Bool
	// local is a window on the WebSocket of the pod itself (its token, or 127.0.0.1), not one
	// that came through the relay or a gateway: whoever holds it has the terminals of this machine
	// already, so the pod signs its power requests without the passkey of the browser.
	local bool
}

// transport is one whole-message connection of a window: the rpc channel, or an extra
// channel of the tunnel. A request read on it is dispatched on the window and its answer
// goes back on it; events go on the rpc transport, except the output of a terminal that
// has its own channel.
type transport struct {
	c    *Client
	conn tunnel.MsgConn
	send chan []byte
	// ws is the WebSocket of the rpc transport (the only transport that can be pinged).
	ws *websocket.Conn
}

func newTransport(c *Client, conn tunnel.MsgConn) *transport {
	return &transport{c: c, conn: conn, send: make(chan []byte, 1024)}
}

// channel returns the transport of a label, nil when the window has none.
func (c *Client) channel(label string) *transport {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.channels[label]
}

// openChannel registers an extra channel of the tunnel and starts serving it.
func (c *Client) openChannel(ctx context.Context, label string, conn tunnel.MsgConn) {
	tr := newTransport(c, conn)
	c.mu.Lock()
	c.channels[label] = tr
	c.mu.Unlock()
	go c.writeLoop(ctx, tr)
	// An http:<n> channel carries one HTTP request (the Service Worker of the relay
	// forwards the fetches of the page), not a request of the JSON protocol.
	if strings.HasPrefix(label, "http:") {
		c.serveHTTPChannel(ctx, tr)
		return
	}
	c.handleLoop(ctx, tr)
}

// httpRequest is one fetch forwarded by the Service Worker of the relay on an http:<n>
// channel: the path of the pod (the app, an asset, a preview) and the headers of the page.
// Base is the path the page runs under on the origin the browser used (/pod/<key> on the relay
// origin): the pod serves its index.html with its assets under it.
type httpRequest struct {
	Path    string            `json:"path"`
	Preview string            `json:"preview,omitempty"`
	Base    string            `json:"base,omitempty"`
	Method  string            `json:"method,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
	Body    string            `json:"body,omitempty"`
}

// httpHead is the answer of an http channel: the status and the headers, then the body as
// raw messages (one per chunk), closed by an empty one.
type httpHead struct {
	Status  int               `json:"status"`
	Headers map[string]string `json:"headers"`
}

// serveHTTPChannel answers one fetch of the page through the tunnel. The window is already
// authorized (the token was proved in the handshake), so the request is served as a local
// one: the app, its assets, and the previews under /__preview/<id>/.
func (c *Client) serveHTTPChannel(ctx context.Context, tr *transport) {
	data, err := tr.read(ctx)
	if err != nil {
		return
	}
	var req httpRequest
	if json.Unmarshal(data, &req) != nil {
		return
	}
	h, ok := c.srv.httpHandler(req)
	if !ok {
		c.sendRaw(tr, mustJSON(httpHead{Status: http.StatusNotFound, Headers: map[string]string{}}))
		return
	}
	w := &channelWriter{c: c, tr: tr}
	r, err := http.NewRequest(req.Method, "http://"+c.srv.host()+req.Path, strings.NewReader(req.Body))
	if err != nil {
		c.sendRaw(tr, mustJSON(httpHead{Status: http.StatusBadRequest, Headers: map[string]string{}}))
		return
	}
	for k, v := range req.Headers {
		r.Header.Set(k, v)
	}
	// The tunnel is the pod's own connection: the window is authorized, and it comes from
	// the machine of the pod.
	r.Header.Set("Cookie", cookieName+"="+c.srv.Token)
	r.RemoteAddr = "127.0.0.1:0"
	// The path the page runs under on the origin the browser used, when the bootstrap gave one.
	if basePattern.MatchString(req.Base) {
		r = r.WithContext(context.WithValue(r.Context(), baseKey{}, req.Base))
	}
	h.ServeHTTP(w, r)
	if !w.head {
		c.sendRaw(tr, mustJSON(httpHead{Status: http.StatusInternalServerError, Headers: map[string]string{}}))
		return
	}
	c.sendRaw(tr, nil) // the end of the body
}

// httpHandler picks the handler of a path of the page: a preview, the page of a peer served
// through this pod, or the static app.
func (s *Server) httpHandler(req httpRequest) (http.Handler, bool) {
	if req.Preview != "" {
		return s.Previews.Handler(req.Preview)
	}
	// A window of the tunnel can open a pod of this pod's LAN too: the same gateway as on the
	// origin of this pod.
	if strings.HasPrefix(req.Path, peerPrefix) && s.LAN != nil {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			s.LAN.ServeGateway(w, r, baseOf(r.Context()))
		}), true
	}
	// A link to a file the relay origin opened in the frame of its bootstrap page.
	if path, _, _ := strings.Cut(req.Path, "?"); path == openPath {
		return http.HandlerFunc(s.serveOpen), true
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { s.serveStatic(w, r) }), true
}

// host is the address the page is served at (the public URL of the pod, else its address).
func (s *Server) host() string {
	if s.Cfg != nil && s.Cfg.PublicURL != "" {
		return strings.TrimPrefix(strings.TrimPrefix(s.Cfg.PublicURL, "https://"), "http://")
	}
	return s.Cfg.Addr
}

func mustJSON(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}

// channelWriter writes an HTTP answer on a tunnel channel: the head as JSON (status and
// headers), then the body in chunks of at most 64 KB (one message each), so the Service
// Worker can stream it.
type channelWriter struct {
	c    *Client
	tr   *transport
	h    http.Header
	head bool
}

func (w *channelWriter) Header() http.Header {
	if w.h == nil {
		w.h = http.Header{}
	}
	return w.h
}

func (w *channelWriter) WriteHeader(code int) {
	if w.head {
		return
	}
	w.head = true
	headers := map[string]string{}
	for k, v := range w.h {
		headers[k] = strings.Join(v, ", ")
	}
	w.c.sendRaw(w.tr, mustJSON(httpHead{Status: code, Headers: headers}))
}

func (w *channelWriter) Write(b []byte) (int, error) {
	if !w.head {
		w.WriteHeader(http.StatusOK)
	}
	n := len(b)
	for len(b) > 0 {
		c := len(b)
		if c > 64*1024 {
			c = 64 * 1024
		}
		// The caller reuses its buffer for the next chunk (io.Copy reads into the same
		// slice), so each chunk must be copied out: a message still queued would otherwise
		// be overwritten by the chunk written later.
		chunk := make([]byte, c)
		copy(chunk, b[:c])
		w.c.sendRaw(w.tr, chunk)
		b = b[c:]
	}
	return n, nil
}

// language is the language of the window (English until it tells its own).
func (c *Client) language() string {
	l, _ := c.lang.Load().(string)
	return l
}

type request struct {
	ID     int64           `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
	// Project is the attached worktree the request runs in (the project of the window by default).
	Project string `json:"project,omitempty"`
	// tr is the transport the request came on (the answer goes back on it).
	tr *transport
}

type rpcError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

type response struct {
	ID     int64     `json:"id"`
	Result any       `json:"result,omitempty"`
	Error  *rpcError `json:"error,omitempty"`
}

type event struct {
	Event string `json:"event"`
	Data  any    `json:"data"`
	// Project is the project whose runtime sent the event.
	Project string `json:"project,omitempty"`
}

var clientSeq struct {
	sync.Mutex
	n int
}

func nextClientID() string {
	clientSeq.Lock()
	defer clientSeq.Unlock()
	clientSeq.n++
	return "c" + time.Now().Format("150405") + "-" + strconv.Itoa(clientSeq.n)
}

// wsConn adapts a WebSocket connection to a tunnel.MsgConn: a frame is already a whole
// message, so no framing is needed.
type wsConn struct{ c *websocket.Conn }

func (w *wsConn) Read() ([]byte, error) {
	_, data, err := w.c.Read(context.Background())
	return data, err
}

func (w *wsConn) Write(b []byte) error {
	return w.c.Write(context.Background(), websocket.MessageText, b)
}

func (w *wsConn) Close() error {
	w.c.Close(websocket.StatusNormalClosure, "")
	return nil
}

func (s *Server) serveWS(w http.ResponseWriter, r *http.Request) {
	// Same origin only: another cluster open in the browser must not reach the pod.
	conn, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	conn.SetReadLimit(64 << 20)
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	c := newClient(s, ctx)
	c.local = true
	c.rpc = newTransport(c, &wsConn{c: conn})
	c.rpc.ws = conn
	s.addClient(c)
	go c.writeLoop(ctx, c.rpc)
	c.push("hello", map[string]string{"clientId": c.id})
	c.handleLoop(ctx, c.rpc)
	s.dropClient(c)
	c.rpc.conn.Close()
}

// ServeTunnel runs the WebSocket protocol on a whole-message transport that is not a
// WebSocket (a WebRTC DataChannel over the relay). The JSON protocol is unchanged: the
// same handler set, the same per-client queues and events. The rpc channel carries the
// requests; each extra channel the browser opens (term:<id>, xfer:<n>, http:<n>) is read
// on its own goroutine, so a 2 MB transfer never delays a keystroke. ServeTunnel returns
// when the rpc channel is closed (the window closed) and channels is drained.
func (s *Server) ServeTunnel(ctx context.Context, rpc tunnel.MsgConn, channels <-chan tunnel.LabeledConn) {
	c := newClient(s, ctx)
	c.rpc = newTransport(c, rpc)
	s.addClient(c)
	go c.writeLoop(ctx, c.rpc)
	c.push("hello", map[string]string{"clientId": c.id})
	done := make(chan struct{})
	go func() {
		c.handleLoop(ctx, c.rpc)
		close(done)
	}()
	for {
		select {
		case <-done:
			s.dropClient(c)
			return
		case <-ctx.Done():
			s.dropClient(c)
			return
		case ch := <-channels:
			go c.openChannel(ctx, ch.Label, ch.Conn)
		}
	}
}

func newClient(s *Server, ctx context.Context) *Client {
	return &Client{window: &window{
		id: nextClientID(), srv: s, ctx: ctx, attached: map[string]bool{},
		channels: map[string]*transport{}, queues: map[string]*queue{},
	}}
}

func (s *Server) addClient(c *Client) {
	s.mu.Lock()
	s.clients[c] = struct{}{}
	s.windowsChanged()
	s.mu.Unlock()
}

func (s *Server) dropClient(c *Client) {
	s.mu.Lock()
	delete(s.clients, c)
	s.windowsChanged()
	var rts []*runtime.Runtime
	for id := range c.attached {
		rts = append(rts, s.runtimes[id])
	}
	rts = append(rts, s.runtimes[c.project])
	s.mu.Unlock()
	for _, rt := range rts {
		if rt != nil {
			rt.Detach()
		}
	}
	_ = c.rpc.conn.Close()
	for _, tr := range c.channels {
		_ = tr.conn.Close()
	}
}

func (c *Client) writeLoop(ctx context.Context, tr *transport) {
	ping := time.NewTicker(20 * time.Second)
	defer ping.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case msg := <-tr.send:
			wctx, cancel := context.WithTimeout(ctx, 30*time.Second)
			err := tr.writeMsg(wctx, msg)
			cancel()
			if err != nil {
				tr.conn.Close()
				return
			}
		case <-ping.C:
			// Only a WebSocket can be pinged; a DataChannel has no keepalive of its own.
			if tr.ws == nil {
				continue
			}
			pctx, cancel := context.WithTimeout(ctx, 10*time.Second)
			_ = tr.ws.Ping(pctx)
			cancel()
		}
	}
}

// writeMsg sends one whole message through the transport of the window.
func (t *transport) writeMsg(ctx context.Context, msg []byte) error {
	if t.ws != nil {
		return t.ws.Write(ctx, websocket.MessageText, msg)
	}
	return t.conn.Write(msg)
}

func (c *Client) enqueueOn(tr *transport, v any) {
	if tr == nil {
		return
	}
	data, err := json.Marshal(v)
	if err != nil {
		log.Printf("encoding: %v", err)
		return
	}
	c.sendRaw(tr, data)
}

// sendRaw queues one whole message on a transport (JSON or raw bytes, as the http channels
// send their body chunks).
func (c *Client) sendRaw(tr *transport, data []byte) {
	select {
	case tr.send <- data:
	case <-c.ctx.Done():
	case <-time.After(5 * time.Second):
		// A client that does not read anymore is dropped rather than blocking the pod.
		tr.conn.Close()
	}
}

func (c *Client) enqueue(v any) { c.enqueueOn(c.rpc, v) }

// push sends an event to the window, and pushIn an event of a project (the worktree of a
// runtime). The output of a terminal goes on its own channel when the window opened one
// (term:<id>), so a long scrollback never delays a keystroke.
func (c *Client) push(name string, data any) { c.pushIn(name, data, "") }

func (c *Client) pushIn(name string, data any, project string) {
	if name == "console.output" {
		if e, ok := data.(map[string]any); ok {
			if id, ok := e["id"].(string); ok {
				if tr := c.channel("term:" + id); tr != nil {
					c.enqueueOn(tr, event{Event: name, Data: data, Project: project})
					return
				}
			}
		}
	}
	c.enqueueOn(c.rpc, event{Event: name, Data: data, Project: project})
}

// sees tells whether the window receives the events of a project (under srv.mu).
func (c *Client) sees(projectID string) bool {
	return projectID != "" && (c.project == projectID || c.attached[projectID])
}

// in returns the view of the window for a request naming a project: its own project, or
// one of the worktrees it attached.
func (c *Client) in(projectID string) (*Client, error) {
	if projectID == "" || projectID == c.project {
		return c, nil
	}
	c.srv.mu.Lock()
	ok := c.attached[projectID]
	c.srv.mu.Unlock()
	if !ok {
		return nil, i18n.New("this worktree is not open in the window")
	}
	return &Client{window: c.window, project: projectID}, nil
}

// queue runs the messages of a sequential group in order and counts them, so that a
// request can wait for the notifications received before it (see barrier).
type queue struct {
	ch   chan request
	mu   sync.Mutex
	cond *sync.Cond
	enq  int64
	done int64
}

// barrier: a language server request waits until the document changes sent before it
// have reached the server, else a completion could be computed on an old text.
var barrier = map[string]string{"lsp.request": "lsp"}

// read returns the next whole message, or an error once ctx ends or the transport
// closes. The WebSocket transport already bounds its reads to the connection context;
// a DataChannel blocks, so it is copied out on a goroutine that cancels with ctx.
func (t *transport) read(ctx context.Context) ([]byte, error) {
	type rmsg struct {
		b   []byte
		err error
	}
	ch := make(chan rmsg, 1)
	go func() {
		b, err := t.conn.Read()
		ch <- rmsg{b, err}
	}()
	select {
	case m := <-ch:
		return m.b, m.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (c *Client) handleLoop(ctx context.Context, tr *transport) {
	for {
		data, err := tr.read(ctx)
		if err != nil {
			return
		}
		var req request
		if json.Unmarshal(data, &req) != nil {
			continue
		}
		req.tr = tr
		if req.Method == "$/cancel" {
			var p struct{ ID int64 }
			_ = json.Unmarshal(req.Params, &p)
			if cancel, ok := c.cancels.Load(p.ID); ok {
				cancel.(context.CancelFunc)()
			}
			continue
		}
		if group, ok := sequential[req.Method]; ok {
			q := c.queueFor(group)
			q.mu.Lock()
			q.enq++
			q.mu.Unlock()
			q.ch <- req
			continue
		}
		var wait func()
		c.mu.Lock()
		q := c.queues[barrier[req.Method]]
		c.mu.Unlock()
		if q != nil {
			q.mu.Lock()
			target := q.enq
			q.mu.Unlock()
			wait = func() {
				q.mu.Lock()
				for q.done < target {
					q.cond.Wait()
				}
				q.mu.Unlock()
			}
		}
		go func() {
			if wait != nil {
				wait()
			}
			c.dispatch(ctx, req, tr)
		}()
	}
}

// queueFor returns the sequential queue of a group, starting its reader on first use.
func (c *Client) queueFor(group string) *queue {
	c.mu.Lock()
	q := c.queues[group]
	if q == nil {
		q = &queue{ch: make(chan request, 4096)}
		q.cond = sync.NewCond(&q.mu)
		c.queues[group] = q
		go func() {
			for r := range q.ch {
				c.dispatch(c.ctx, r, r.tr)
				q.mu.Lock()
				q.done++
				q.cond.Broadcast()
				q.mu.Unlock()
			}
		}()
	}
	c.mu.Unlock()
	return q
}

func (c *Client) dispatch(ctx context.Context, req request, tr *transport) {
	h, ok := c.srv.handlers[req.Method]
	var res any
	var err error
	sc, scopeErr := c.in(req.Project)
	if !ok {
		err = i18n.Errorf("unknown method: %s", req.Method)
	} else if scopeErr != nil {
		err = scopeErr
	} else {
		rctx, cancel := context.WithCancel(ctx)
		c.cancels.Store(req.ID, cancel)
		func() {
			defer func() {
				if r := recover(); r != nil {
					log.Printf("panic in %s: %v", req.Method, r)
					err = i18n.New("internal error of the pod")
				}
			}()
			res, err = h(rctx, sc, req.Params)
		}()
		c.cancels.Delete(req.ID)
		cancel()
	}
	if req.ID == 0 {
		return // notification
	}
	resp := response{ID: req.ID, Result: res}
	if err != nil {
		resp.Result = nil
		resp.Error = toRPCError(err, c.language())
	} else if res == nil {
		resp.Result = true
	}
	c.enqueueOn(req.tr, resp)
}

// codeError gives an error a code the page can test (its message is still translated).
type codeError struct {
	code string
	error
}

func (e *codeError) Unwrap() error { return e.error }

func toRPCError(err error, lang string) *rpcError {
	var ce *codeError
	if errors.As(err, &ce) {
		return &rpcError{Code: ce.code, Message: i18n.Translate(lang, ce.error)}
	}
	var ar *sshx.AuthRequired
	var np *db.NeedPassword
	switch {
	case errors.As(err, &ar):
		msg := i18n.Translate(lang, ar.Msg)
		return &rpcError{Code: "auth_required", Message: msg, Data: map[string]string{"kind": ar.Kind, "prompt": msg}}
	case errors.As(err, &np):
		msg := i18n.Translate(lang, np.Msg)
		return &rpcError{Code: "db_password", Message: msg, Data: map[string]string{"kind": "dbpassword", "prompt": msg}}
	case errors.Is(err, context.Canceled):
		return &rpcError{Code: "canceled", Message: i18n.T(lang, "canceled")}
	}
	return &rpcError{Code: "error", Message: i18n.Translate(lang, err)}
}

// emitter returns the event function of a project runtime.
func (s *Server) emitter(projectID string) runtime.Emit {
	return func(name string, data any, except string) {
		// The questions of a session show in the windows of its root, a worktree's too.
		if name == "questions.changed" {
			asked, _ := data.(map[string]any)["asked"].(string)
			s.questionsChanged(projectID, asked)
			return
		}
		s.mu.Lock()
		var targets []*Client
		for c := range s.clients {
			if c.sees(projectID) && c.id != except {
				targets = append(targets, c)
			}
		}
		s.mu.Unlock()
		for _, c := range targets {
			c.pushIn(name, data, projectID)
		}
		// A saved command flagged test that fails is a push notification.
		if ev, ok := data.(map[string]any); ok && name == "console.exit" {
			if code, _ := ev["code"].(int); code != 0 {
				id, _ := ev["id"].(string)
				go s.notifyTests(projectID, id, code)
			}
		}
	}
}

// Broadcast sends an event to every window (used outside a request, e.g. the state of the
// relay link).
func (s *Server) Broadcast(name string, data any) { s.broadcast(name, data, nil) }

// broadcast sends an event to every client (project list, settings).
func (s *Server) broadcast(name string, data any, except *Client) {
	s.mu.Lock()
	var targets []*Client
	for c := range s.clients {
		if c != except {
			targets = append(targets, c)
		}
	}
	s.mu.Unlock()
	for _, c := range targets {
		c.push(name, data)
	}
	// The project list of the pod is published to the other pods: the hook debounces it.
	if s.ProjectsChanged != nil && (name == "projects.changed" || name == "projects.iconChanged") {
		s.ProjectsChanged()
	}
}

// Shutdown stops every project runtime (consoles, language servers, databases); the
// terminals of the keeper keep running.
func (s *Server) Shutdown() {
	s.stopped.Store(true)
	s.saveRunning()
	s.mu.Lock()
	rts := s.runtimes
	s.runtimes = map[string]*runtime.Runtime{}
	s.mu.Unlock()
	s.Forwards.CloseAll()
	s.Previews.Shutdown()
	for _, rt := range rts {
		rt.Release()
	}
	s.Desks.FlushAll()
	s.Pool.CloseAll()
	s.LLM.Close()
	s.Kanban.Close()
}
