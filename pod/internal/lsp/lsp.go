// Package lsp runs one language server per project and per language, and relays
// JSON-RPC between the web editor and the servers.
package lsp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"webide/pod/internal/execx"
)

type Spec struct {
	Lang       string     `json:"lang"`
	Candidates [][]string `json:"candidates"`
	Markers    []string   `json:"markers"`
	Exts       []string   `json:"exts"`
}

var Defaults = []Spec{
	{Lang: "go", Candidates: [][]string{{"gopls"}}, Markers: []string{"go.mod", "go.work"}, Exts: []string{".go"}},
	{Lang: "php", Candidates: [][]string{{"intelephense", "--stdio"}, {"phpactor", "language-server"}}, Markers: []string{"composer.json"}, Exts: []string{".php", ".phtml"}},
	{Lang: "python", Candidates: [][]string{{"pyright-langserver", "--stdio"}, {"basedpyright-langserver", "--stdio"}, {"pylsp"}}, Markers: []string{"pyproject.toml", "setup.py", "requirements.txt", "Pipfile"}, Exts: []string{".py", ".pyi"}},
	{Lang: "typescript", Candidates: [][]string{{"typescript-language-server", "--stdio"}}, Markers: []string{"package.json", "tsconfig.json", "jsconfig.json"}, Exts: []string{".ts", ".tsx", ".js", ".jsx", ".mjs", ".cjs", ".mts", ".cts"}},
}

// LangOf returns the server language handling a file, from its extension.
func LangOf(p string) string {
	ext := strings.ToLower(path.Ext(p))
	for _, s := range Defaults {
		for _, e := range s.Exts {
			if e == ext {
				return s.Lang
			}
		}
	}
	return ""
}

func URI(p string) string { return (&url.URL{Scheme: "file", Path: p}).String() }

type Status struct {
	Lang     string   `json:"lang"`
	Detected bool     `json:"detected"`
	Command  []string `json:"command,omitempty"`
	Running  bool     `json:"running"`
	Error    string   `json:"error,omitempty"`
}

type Manager struct {
	runner    execx.Runner
	root      string
	local     bool
	overrides map[string][]string
	exists    func(p string) bool
	emit      func(event string, data any)

	mu       sync.Mutex
	servers  map[string]*server
	resolved map[string][]string
}

// NewManager: overrides come from .ide/lsp.json ({"php": ["phpactor", "language-server"]}).
func NewManager(r execx.Runner, root string, local bool, overrides map[string][]string, exists func(string) bool, emit func(string, any)) *Manager {
	return &Manager{runner: r, root: root, local: local, overrides: overrides, exists: exists, emit: emit,
		servers: map[string]*server{}, resolved: map[string][]string{}}
}

func spec(lang string) (Spec, bool) {
	for _, s := range Defaults {
		if s.Lang == lang {
			return s, true
		}
	}
	return Spec{}, false
}

func (m *Manager) command(lang string) []string {
	m.mu.Lock()
	if c, ok := m.resolved[lang]; ok {
		m.mu.Unlock()
		return c
	}
	m.mu.Unlock()
	var cmd []string
	if o, ok := m.overrides[lang]; ok && len(o) > 0 {
		cmd = o
	} else if s, ok := spec(lang); ok {
		for _, c := range s.Candidates {
			if m.runner.Has(c[0]) {
				cmd = c
				break
			}
		}
	}
	m.mu.Lock()
	m.resolved[lang] = cmd
	m.mu.Unlock()
	return cmd
}

// Status lists the languages detected in the project root and their servers.
func (m *Manager) Status() []Status {
	var out []Status
	for _, s := range Defaults {
		st := Status{Lang: s.Lang}
		for _, mk := range s.Markers {
			if m.exists(path.Join(m.root, mk)) {
				st.Detected = true
			}
		}
		m.mu.Lock()
		srv := m.servers[s.Lang]
		m.mu.Unlock()
		if srv != nil {
			st.Running = srv.alive()
			if srv.initErr != nil {
				st.Error = srv.initErr.Error()
			}
		}
		if st.Detected || srv != nil {
			st.Command = m.command(s.Lang)
			if st.Command == nil && st.Error == "" {
				st.Error = "aucun serveur installé (" + candidates(s) + ")"
			}
		}
		out = append(out, st)
	}
	return out
}

func candidates(s Spec) string {
	var n []string
	for _, c := range s.Candidates {
		n = append(n, c[0])
	}
	return strings.Join(n, ", ")
}

func (m *Manager) get(lang string) (*server, error) {
	m.mu.Lock()
	srv := m.servers[lang]
	m.mu.Unlock()
	if srv != nil && srv.alive() {
		<-srv.ready
		return srv, srv.initErr
	}
	cmd := m.command(lang)
	if cmd == nil {
		s, _ := spec(lang)
		return nil, fmt.Errorf("pas de serveur de langage pour %s (installer %s)", lang, candidates(s))
	}
	m.mu.Lock()
	if cur := m.servers[lang]; cur != nil && cur != srv && cur.alive() {
		m.mu.Unlock()
		<-cur.ready
		return cur, cur.initErr
	}
	srv = &server{m: m, lang: lang, pending: map[int64]chan *message{}, docs: map[string]*doc{}, ready: make(chan struct{}), done: make(chan struct{})}
	m.servers[lang] = srv
	m.mu.Unlock()
	srv.start(cmd)
	<-srv.ready
	return srv, srv.initErr
}

func (m *Manager) Request(ctx context.Context, lang, method string, params json.RawMessage) (json.RawMessage, error) {
	srv, err := m.get(lang)
	if err != nil {
		return nil, err
	}
	return srv.request(ctx, method, params)
}

// Capabilities returns the server capabilities from initialize.
func (m *Manager) Capabilities(lang string) (json.RawMessage, error) {
	srv, err := m.get(lang)
	if err != nil {
		return nil, err
	}
	return srv.caps, nil
}

func (m *Manager) Notify(lang, method string, params json.RawMessage) error {
	srv, err := m.get(lang)
	if err != nil {
		return err
	}
	return srv.notifyDoc(method, params)
}

func (m *Manager) Running() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, s := range m.servers {
		if s.alive() {
			return true
		}
	}
	return false
}

func (m *Manager) StopAll() {
	m.mu.Lock()
	servers := m.servers
	m.servers = map[string]*server{}
	m.mu.Unlock()
	for _, s := range servers {
		s.stop()
	}
}

// ---------- server ----------

type message struct {
	JSONRPC string           `json:"jsonrpc"`
	ID      *json.RawMessage `json:"id,omitempty"`
	Method  string           `json:"method,omitempty"`
	Params  json.RawMessage  `json:"params,omitempty"`
	Result  json.RawMessage  `json:"result,omitempty"`
	Error   *rpcError        `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type doc struct {
	refs    int
	version int
}

type server struct {
	m       *Manager
	lang    string
	proc    execx.Process
	wmu     sync.Mutex
	nextID  atomic.Int64
	pmu     sync.Mutex
	pending map[int64]chan *message
	caps    json.RawMessage
	initErr error
	ready   chan struct{}
	done    chan struct{}
	dmu     sync.Mutex
	docs    map[string]*doc
}

func (s *server) alive() bool {
	select {
	case <-s.done:
		return false
	default:
		return true
	}
}

func (s *server) start(cmd []string) {
	proc, err := s.m.runner.Start(cmd, s.m.root)
	if err != nil {
		s.initErr = err
		close(s.done)
		close(s.ready)
		return
	}
	s.proc = proc
	go s.readLoop()
	go func() {
		_ = proc.Wait()
		s.closeDone()
	}()
	go s.initialize()
}

func (s *server) closeDone() {
	s.pmu.Lock()
	defer s.pmu.Unlock()
	select {
	case <-s.done:
	default:
		close(s.done)
		for id, ch := range s.pending {
			close(ch)
			delete(s.pending, id)
		}
	}
}

func (s *server) initialize() {
	defer close(s.ready)
	var pid any
	if s.m.local {
		pid = os.Getpid()
	}
	params := map[string]any{
		"processId": pid,
		"rootUri":   URI(s.m.root),
		"rootPath":  s.m.root,
		"workspaceFolders": []map[string]string{
			{"uri": URI(s.m.root), "name": path.Base(s.m.root)},
		},
		"clientInfo": map[string]string{"name": "web-ide"},
		"capabilities": map[string]any{
			"workspace": map[string]any{
				"workspaceFolders": true,
				"configuration":    true,
				"symbol":           map[string]any{},
			},
			"textDocument": map[string]any{
				"synchronization":    map[string]any{"didSave": true},
				"definition":         map[string]any{"linkSupport": true},
				"declaration":        map[string]any{"linkSupport": true},
				"implementation":     map[string]any{"linkSupport": true},
				"typeDefinition":     map[string]any{"linkSupport": true},
				"references":         map[string]any{},
				"hover":              map[string]any{"contentFormat": []string{"plaintext", "markdown"}},
				"documentSymbol":     map[string]any{"hierarchicalDocumentSymbolSupport": true},
				"publishDiagnostics": map[string]any{"relatedInformation": true},
				"typeHierarchy":      map[string]any{},
			},
		},
	}
	raw, _ := json.Marshal(params)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	res, err := s.request(ctx, "initialize", raw)
	if err != nil {
		s.initErr = fmt.Errorf("initialisation de %s : %w", s.lang, err)
		s.stop()
		return
	}
	var ir struct {
		Capabilities json.RawMessage `json:"capabilities"`
	}
	_ = json.Unmarshal(res, &ir)
	s.caps = ir.Capabilities
	_ = s.send(&message{Method: "initialized", Params: json.RawMessage(`{}`)})
	s.m.emit("lsp.status", map[string]any{"lang": s.lang, "running": true})
}

func (s *server) send(msg *message) error {
	msg.JSONRPC = "2.0"
	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	s.wmu.Lock()
	defer s.wmu.Unlock()
	if s.proc == nil {
		return errors.New("serveur arrêté")
	}
	_, err = fmt.Fprintf(s.proc.Stdin(), "Content-Length: %d\r\n\r\n%s", len(data), data)
	return err
}

func (s *server) request(ctx context.Context, method string, params json.RawMessage) (json.RawMessage, error) {
	id := s.nextID.Add(1)
	ch := make(chan *message, 1)
	s.pmu.Lock()
	select {
	case <-s.done:
		s.pmu.Unlock()
		return nil, errors.New("serveur de langage arrêté")
	default:
	}
	s.pending[id] = ch
	s.pmu.Unlock()
	raw := json.RawMessage(strconv.FormatInt(id, 10))
	if len(params) == 0 {
		params = json.RawMessage(`null`)
	}
	if err := s.send(&message{ID: &raw, Method: method, Params: params}); err != nil {
		return nil, err
	}
	select {
	case msg, ok := <-ch:
		if !ok {
			return nil, errors.New("serveur de langage arrêté")
		}
		if msg.Error != nil {
			return nil, errors.New(msg.Error.Message)
		}
		if msg.Result == nil {
			return json.RawMessage(`null`), nil
		}
		return msg.Result, nil
	case <-ctx.Done():
		s.pmu.Lock()
		delete(s.pending, id)
		s.pmu.Unlock()
		_ = s.send(&message{Method: "$/cancelRequest", Params: json.RawMessage(fmt.Sprintf(`{"id":%d}`, id))})
		return nil, ctx.Err()
	}
}

type docParams struct {
	TextDocument struct {
		URI        string `json:"uri"`
		LanguageID string `json:"languageId,omitempty"`
		Version    int    `json:"version"`
		Text       string `json:"text,omitempty"`
	} `json:"textDocument"`
	ContentChanges json.RawMessage `json:"contentChanges,omitempty"`
	Text           *string         `json:"text,omitempty"`
}

// notifyDoc forwards document notifications. Several windows can open the same document:
// the pod counts the opens, sends one didOpen per document and keeps the version increasing.
func (s *server) notifyDoc(method string, params json.RawMessage) error {
	var p docParams
	if err := json.Unmarshal(params, &p); err != nil || p.TextDocument.URI == "" {
		return s.send(&message{Method: method, Params: params})
	}
	uri := p.TextDocument.URI
	s.dmu.Lock()
	d := s.docs[uri]
	switch method {
	case "textDocument/didOpen":
		if d != nil {
			d.refs++
			d.version++
			change, _ := json.Marshal(map[string]any{
				"textDocument":   map[string]any{"uri": uri, "version": d.version},
				"contentChanges": []map[string]string{{"text": p.TextDocument.Text}},
			})
			s.dmu.Unlock()
			return s.send(&message{Method: "textDocument/didChange", Params: change})
		}
		d = &doc{refs: 1, version: 1}
		s.docs[uri] = d
		p.TextDocument.Version = 1
	case "textDocument/didChange":
		if d == nil {
			s.dmu.Unlock()
			return nil
		}
		d.version++
		p.TextDocument.Version = d.version
	case "textDocument/didClose":
		if d == nil {
			s.dmu.Unlock()
			return nil
		}
		d.refs--
		if d.refs > 0 {
			s.dmu.Unlock()
			return nil
		}
		delete(s.docs, uri)
	}
	s.dmu.Unlock()
	if method == "textDocument/didChange" || method == "textDocument/didOpen" {
		raw, _ := json.Marshal(p)
		params = raw
	}
	return s.send(&message{Method: method, Params: params})
}

func (s *server) readLoop() {
	r := bufio.NewReaderSize(s.proc.Stdout(), 64*1024)
	for {
		length := -1
		for {
			line, err := r.ReadString('\n')
			if err != nil {
				s.closeDone()
				return
			}
			line = strings.TrimSpace(line)
			if line == "" {
				break
			}
			if k, v, ok := strings.Cut(line, ":"); ok && strings.EqualFold(k, "Content-Length") {
				length, _ = strconv.Atoi(strings.TrimSpace(v))
			}
		}
		if length < 0 {
			continue
		}
		body := make([]byte, length)
		if _, err := io.ReadFull(r, body); err != nil {
			s.closeDone()
			return
		}
		var msg message
		if json.Unmarshal(body, &msg) != nil {
			continue
		}
		s.handle(&msg)
	}
}

func (s *server) handle(msg *message) {
	switch {
	case msg.ID != nil && msg.Method != "":
		// Request from the server: answer the common ones with neutral values.
		var result any
		if msg.Method == "workspace/configuration" {
			var p struct {
				Items []any `json:"items"`
			}
			_ = json.Unmarshal(msg.Params, &p)
			result = make([]any, len(p.Items))
		}
		raw, _ := json.Marshal(result)
		_ = s.send(&message{ID: msg.ID, Result: raw})
	case msg.ID != nil:
		id, err := strconv.ParseInt(string(*msg.ID), 10, 64)
		if err != nil {
			return
		}
		s.pmu.Lock()
		ch := s.pending[id]
		delete(s.pending, id)
		s.pmu.Unlock()
		if ch != nil {
			ch <- msg
		}
	case msg.Method == "textDocument/publishDiagnostics":
		s.m.emit("lsp.diagnostics", map[string]any{"lang": s.lang, "params": msg.Params})
	case msg.Method == "window/showMessage" || msg.Method == "window/logMessage":
		s.m.emit("lsp.log", map[string]any{"lang": s.lang, "method": msg.Method, "params": msg.Params})
	}
}

func (s *server) stop() {
	if s.proc == nil {
		return
	}
	if s.alive() && s.initErr == nil {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		_, _ = s.request(ctx, "shutdown", nil)
		cancel()
		_ = s.send(&message{Method: "exit"})
	}
	select {
	case <-s.done:
	case <-time.After(2 * time.Second):
		_ = s.proc.Kill()
	}
	s.m.emit("lsp.status", map[string]any{"lang": s.lang, "running": false})
}
