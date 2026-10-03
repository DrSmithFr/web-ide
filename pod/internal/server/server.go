// Package server serves the embedded web app and the WebSocket protocol of the pod.
package server

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"html/template"
	"io/fs"
	"log"
	"net"
	"net/http"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"

	"webide/pod/internal/config"
	"webide/pod/internal/db"
	"webide/pod/internal/hfcache"
	"webide/pod/internal/llm"
	"webide/pod/internal/projects"
	"webide/pod/internal/runtime"
	"webide/pod/internal/sessions"
	"webide/pod/internal/settings"
	"webide/pod/internal/sshx"
	"webide/pod/internal/store"
)

const cookieName = "webide_token"

type Server struct {
	Cfg      *config.Config
	Store    *store.Store
	Token    string
	Projects *projects.Registry
	Settings *settings.Settings
	Sessions *sessions.Sessions
	Pool     *sshx.Pool
	LLM      *llm.Manager
	// Models caches the speech recognition models downloaded for the page.
	Models *hfcache.Cache
	Static fs.FS
	// AllowRemote accepts connections from other machines (the token is then the only protection).
	AllowRemote bool

	mu       sync.Mutex
	clients  map[*Client]struct{}
	runtimes map[string]*runtime.Runtime
	opening  map[string]*sync.Mutex
	handlers map[string]handler
	claims   map[string]*Client // conversation of the assistant → window running it
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
	s.claims = map[string]*Client{}
	s.registerGlobal()
	s.registerProject()
	s.registerDB()
	s.registerGit()
	s.registerLLM()
	s.registerExec()
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

var pairPage = template.Must(template.New("pair").Parse(`<!doctype html>
<html lang="fr"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>Web IDE · appairage</title>
<style>
:root{color-scheme:light dark;--bg:#f6f6f4;--fg:#1d1d1b;--muted:#6b6b66;--line:#d8d8d2;--accent:#2f6fde}
@media (prefers-color-scheme:dark){:root{--bg:#18191b;--fg:#e6e6e3;--muted:#9a9a95;--line:#34363a;--accent:#6c9cff}}
body{margin:0;min-height:100vh;display:grid;place-items:center;background:var(--bg);color:var(--fg);font:15px/1.5 system-ui,sans-serif}
form{width:min(420px,calc(100vw - 32px));display:grid;gap:12px}
h1{font-size:20px;margin:0}p{margin:0;color:var(--muted)}
input{font:14px ui-monospace,monospace;padding:10px;border:1px solid var(--line);border-radius:6px;background:transparent;color:inherit}
button{padding:10px;border:0;border-radius:6px;background:var(--accent);color:#fff;font-weight:600;cursor:pointer}
.err{color:#d33}
</style></head><body>
<form method="post" action="/auth">
<h1>Appairer ce navigateur</h1>
<p>Collez le jeton affiché par le pod au démarrage (aussi dans <code>~/.web-ide/token</code>).</p>
{{if .}}<p class="err">{{.}}</p>{{end}}
<input name="token" autocomplete="off" autofocus placeholder="jeton">
<input type="hidden" name="next" value="/">
<button>Appairer</button>
</form></body></html>`))

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !s.AllowRemote && !loopback(r) {
		http.Error(w, "accès réservé à la machine locale", http.StatusForbidden)
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
	if r.URL.Path == "/auth" && r.Method == http.MethodPost {
		if subtle.ConstantTimeCompare([]byte(strings.TrimSpace(r.FormValue("token"))), []byte(s.Token)) != 1 {
			w.WriteHeader(http.StatusUnauthorized)
			_ = pairPage.Execute(w, "Jeton invalide.")
			return
		}
		s.setCookie(w)
		http.Redirect(w, r, "/", http.StatusFound)
		return
	}
	if !s.authorized(r) {
		if r.URL.Path == "/ws" {
			http.Error(w, "non appairé", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusUnauthorized)
		_ = pairPage.Execute(w, "")
		return
	}
	if r.URL.Path == "/ws" {
		s.serveWS(w, r)
		return
	}
	if strings.HasPrefix(r.URL.Path, hfcache.Prefix) && s.Models != nil {
		s.Models.ServeHTTP(w, r)
		return
	}
	s.serveStatic(w, r)
}

func (s *Server) serveStatic(w http.ResponseWriter, r *http.Request) {
	p := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
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
		http.Error(w, "front non compilé : lancer `make build`", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write(data)
}

// ---------- WebSocket ----------

type Client struct {
	id      string
	srv     *Server
	conn    *websocket.Conn
	send    chan []byte
	ctx     context.Context
	project string
	cancels sync.Map
}

type request struct {
	ID     int64           `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
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

func (s *Server) serveWS(w http.ResponseWriter, r *http.Request) {
	// Same origin only: another site open in the browser must not reach the pod.
	conn, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	conn.SetReadLimit(64 << 20)
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	c := &Client{id: nextClientID(), srv: s, conn: conn, send: make(chan []byte, 1024), ctx: ctx}
	s.mu.Lock()
	s.clients[c] = struct{}{}
	s.mu.Unlock()
	go c.writeLoop(ctx)
	c.push("hello", map[string]string{"clientId": c.id})
	c.readLoop(ctx)
	s.mu.Lock()
	delete(s.clients, c)
	var released []string
	for id, owner := range s.claims {
		if owner == c {
			delete(s.claims, id)
			released = append(released, id)
		}
	}
	project := c.project
	rt := s.runtimes[project]
	s.mu.Unlock()
	// A window following a conversation this one was running takes it over.
	for _, id := range released {
		s.emitter(project)("llm.released", map[string]string{"id": id}, c.id)
	}
	if rt != nil {
		rt.Detach()
	}
	conn.Close(websocket.StatusNormalClosure, "")
}

func (c *Client) writeLoop(ctx context.Context) {
	ping := time.NewTicker(20 * time.Second)
	defer ping.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case msg := <-c.send:
			wctx, cancel := context.WithTimeout(ctx, 30*time.Second)
			err := c.conn.Write(wctx, websocket.MessageText, msg)
			cancel()
			if err != nil {
				c.conn.Close(websocket.StatusGoingAway, "write failed")
				return
			}
		case <-ping.C:
			pctx, cancel := context.WithTimeout(ctx, 10*time.Second)
			_ = c.conn.Ping(pctx)
			cancel()
		}
	}
}

func (c *Client) enqueue(v any) {
	data, err := json.Marshal(v)
	if err != nil {
		log.Printf("encodage : %v", err)
		return
	}
	select {
	case c.send <- data:
	case <-c.ctx.Done():
	case <-time.After(5 * time.Second):
		// A client that does not read anymore is dropped rather than blocking the pod.
		c.conn.Close(websocket.StatusPolicyViolation, "client trop lent")
	}
}

func (c *Client) push(name string, data any) { c.enqueue(event{Event: name, Data: data}) }

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

func (c *Client) readLoop(ctx context.Context) {
	queues := map[string]*queue{}
	defer func() {
		for _, q := range queues {
			close(q.ch)
			q.mu.Lock()
			q.done = q.enq // release the waiting requests
			q.cond.Broadcast()
			q.mu.Unlock()
		}
	}()
	for {
		_, data, err := c.conn.Read(ctx)
		if err != nil {
			return
		}
		var req request
		if json.Unmarshal(data, &req) != nil {
			continue
		}
		if req.Method == "$/cancel" {
			var p struct{ ID int64 }
			_ = json.Unmarshal(req.Params, &p)
			if cancel, ok := c.cancels.Load(p.ID); ok {
				cancel.(context.CancelFunc)()
			}
			continue
		}
		if group, ok := sequential[req.Method]; ok {
			q := queues[group]
			if q == nil {
				q = &queue{ch: make(chan request, 4096)}
				q.cond = sync.NewCond(&q.mu)
				queues[group] = q
				go func() {
					for r := range q.ch {
						c.dispatch(ctx, r)
						q.mu.Lock()
						q.done++
						q.cond.Broadcast()
						q.mu.Unlock()
					}
				}()
			}
			q.mu.Lock()
			q.enq++
			q.mu.Unlock()
			q.ch <- req
			continue
		}
		var wait func()
		if q := queues[barrier[req.Method]]; q != nil {
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
			c.dispatch(ctx, req)
		}()
	}
}

func (c *Client) dispatch(ctx context.Context, req request) {
	h, ok := c.srv.handlers[req.Method]
	var res any
	var err error
	if !ok {
		err = errors.New("méthode inconnue : " + req.Method)
	} else {
		rctx, cancel := context.WithCancel(ctx)
		c.cancels.Store(req.ID, cancel)
		func() {
			defer func() {
				if r := recover(); r != nil {
					log.Printf("panique dans %s : %v", req.Method, r)
					err = errors.New("erreur interne du pod")
				}
			}()
			res, err = h(rctx, c, req.Params)
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
		resp.Error = toRPCError(err)
	} else if res == nil {
		resp.Result = true
	}
	c.enqueue(resp)
}

func toRPCError(err error) *rpcError {
	var ar *sshx.AuthRequired
	var np *db.NeedPassword
	switch {
	case errors.As(err, &ar):
		return &rpcError{Code: "auth_required", Message: ar.Prompt, Data: map[string]string{"kind": ar.Kind, "prompt": ar.Prompt}}
	case errors.As(err, &np):
		return &rpcError{Code: "db_password", Message: np.Prompt, Data: map[string]string{"kind": "dbpassword", "prompt": np.Prompt}}
	case errors.Is(err, context.Canceled):
		return &rpcError{Code: "canceled", Message: "annulé"}
	}
	return &rpcError{Code: "error", Message: err.Error()}
}

// emitter returns the event function of a project runtime.
func (s *Server) emitter(projectID string) runtime.Emit {
	return func(name string, data any, except string) {
		s.mu.Lock()
		var targets []*Client
		for c := range s.clients {
			if c.project == projectID && c.id != except {
				targets = append(targets, c)
			}
		}
		s.mu.Unlock()
		for _, c := range targets {
			c.push(name, data)
		}
	}
}

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
}

// Shutdown stops every project runtime (consoles, language servers, databases).
func (s *Server) Shutdown() {
	s.mu.Lock()
	rts := s.runtimes
	s.runtimes = map[string]*runtime.Runtime{}
	s.mu.Unlock()
	for _, rt := range rts {
		rt.Close()
	}
	s.Sessions.FlushAll()
	s.Pool.CloseAll()
	s.LLM.Close()
}
