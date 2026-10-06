// Package llm talks to local model servers (llama.cpp server, Ollama) for the chat tool:
// server registry, model list with capabilities, streamed chat completions with tool
// calls, and the conversations saved per project.
package llm

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/DrSmithFr/web-ide/pod/internal/i18n"
	"github.com/DrSmithFr/web-ide/pod/internal/store"
)

// Server is a model server reachable over HTTP.
type Server struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// Kind: "auto", "llamacpp" or "ollama" ("auto" asks the server).
	Kind string `json:"kind"`
	URL  string `json:"url"`
	// APIKey is sent as a bearer token (llama-server --api-key). Never sent to the page.
	APIKey string `json:"apiKey,omitempty"`
	// Context is the context size asked to Ollama (num_ctx), 0 for its default.
	Context int `json:"context,omitempty"`
	// Parallel is the number of conversations the server runs at once (0: 1, one GPU).
	Parallel int `json:"parallel,omitempty"`
}

// ServerView is what the page receives.
type ServerView struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Kind     string `json:"kind"`
	URL      string `json:"url"`
	HasKey   bool   `json:"hasKey"`
	Context  int    `json:"context,omitempty"`
	Parallel int    `json:"parallel,omitempty"`
}

type Config struct {
	Servers []Server `json:"servers"`
	// Last choice of the user, restored when the tool opens.
	Server string `json:"server,omitempty"`
	Model  string `json:"model,omitempty"`
}

type Manager struct {
	st     *store.Store
	client *http.Client
	mu     sync.Mutex
	cfg    Config
	kinds  map[string]string  // detected kind per server URL
	dbs    map[string]*sql.DB // conversation bases by path
	jobs   map[string]*job    // completions running or recently ended, by stream
}

const configFile = "llm.json"

func New(st *store.Store) *Manager {
	m := &Manager{st: st, client: &http.Client{}, kinds: map[string]string{}, dbs: map[string]*sql.DB{}, jobs: map[string]*job{}}
	if err := st.ReadJSON(configFile, &m.cfg); err != nil && !store.IsNotExist(err) {
		m.cfg = Config{}
	}
	return m
}

// View returns the configuration without the API keys.
func (m *Manager) View() map[string]any {
	m.mu.Lock()
	defer m.mu.Unlock()
	views := []ServerView{}
	for _, s := range m.cfg.Servers {
		views = append(views, ServerView{ID: s.ID, Name: s.Name, Kind: s.Kind, URL: s.URL, HasKey: s.APIKey != "", Context: s.Context, Parallel: s.Parallel})
	}
	return map[string]any{"servers": views, "server": m.cfg.Server, "model": m.cfg.Model}
}

// SaveServer adds or replaces a server. keepKey keeps the stored API key when the page
// sends none (it never receives it).
func (m *Manager) SaveServer(s Server, keepKey bool) error {
	s.URL = NormalizeURL(s.URL)
	if s.URL == "" {
		return i18n.New("server address is missing")
	}
	if s.Kind == "" {
		s.Kind = "auto"
	}
	if s.Name == "" {
		s.Name = strings.TrimPrefix(strings.TrimPrefix(s.URL, "http://"), "https://")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	found := false
	for i, old := range m.cfg.Servers {
		if old.ID == s.ID {
			if keepKey && s.APIKey == "" {
				s.APIKey = old.APIKey
			}
			m.cfg.Servers[i] = s
			found = true
		}
	}
	if !found {
		if s.ID == "" {
			s.ID = fmt.Sprintf("s%d", time.Now().UnixNano())
		}
		m.cfg.Servers = append(m.cfg.Servers, s)
	}
	delete(m.kinds, s.URL)
	return m.st.WriteJSON(configFile, m.cfg)
}

func (m *Manager) DeleteServer(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := m.cfg.Servers[:0]
	for _, s := range m.cfg.Servers {
		if s.ID != id {
			out = append(out, s)
		}
	}
	m.cfg.Servers = out
	if m.cfg.Server == id {
		m.cfg.Server, m.cfg.Model = "", ""
	}
	return m.st.WriteJSON(configFile, m.cfg)
}

// Select remembers the server and model last used.
func (m *Manager) Select(server, model string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cfg.Server == server && m.cfg.Model == model {
		return nil
	}
	m.cfg.Server, m.cfg.Model = server, model
	return m.st.WriteJSON(configFile, m.cfg)
}

func (m *Manager) server(id string) (Server, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, s := range m.cfg.Servers {
		if s.ID == id {
			return s, nil
		}
	}
	return Server{}, i18n.New("unknown model server")
}

// NormalizeURL accepts "host:port", "http://host:port/" or "host:port/v1".
func NormalizeURL(u string) string {
	u = strings.TrimSpace(u)
	if u == "" {
		return ""
	}
	if !strings.Contains(u, "://") {
		u = "http://" + u
	}
	u = strings.TrimRight(u, "/")
	return strings.TrimSuffix(u, "/v1")
}

// ---------- HTTP helpers ----------

func (m *Manager) do(ctx context.Context, s Server, method, path string, body any) (*http.Response, error) {
	var rd io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		rd = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, s.URL+path, rd)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if s.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+s.APIKey)
	}
	resp, err := m.client.Do(req)
	if err != nil {
		var ue interface{ Timeout() bool }
		if errors.As(err, &ue) && ue.Timeout() {
			return nil, i18n.Errorf("%s: no answer", s.URL)
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, i18n.Errorf("%s cannot be reached: %v", s.URL, unwrapNet(err))
	}
	if resp.StatusCode >= 300 {
		defer resp.Body.Close()
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, &HTTPError{Status: resp.StatusCode, Message: errorMessage(data)}
	}
	return resp, nil
}

type HTTPError struct {
	Status  int
	Message string
}

func (e *HTTPError) Error() string {
	if e.Message == "" {
		return fmt.Sprintf("erreur HTTP %d", e.Status)
	}
	return fmt.Sprintf("erreur %d : %s", e.Status, e.Message)
}

// errorMessage extracts the message of an OpenAI ({"error":{"message"}}) or Ollama
// ({"error":"..."}) error body.
func errorMessage(data []byte) string {
	var v struct {
		Error json.RawMessage `json:"error"`
	}
	if json.Unmarshal(data, &v) == nil && len(v.Error) > 0 {
		var s string
		if json.Unmarshal(v.Error, &s) == nil {
			return s
		}
		var o struct{ Message string }
		if json.Unmarshal(v.Error, &o) == nil && o.Message != "" {
			return o.Message
		}
	}
	return strings.TrimSpace(string(data))
}

func unwrapNet(err error) error {
	for {
		u := errors.Unwrap(err)
		if u == nil {
			return err
		}
		err = u
	}
}

func (m *Manager) getJSON(ctx context.Context, s Server, path string, v any) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	resp, err := m.do(ctx, s, http.MethodGet, path, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return json.NewDecoder(resp.Body).Decode(v)
}

func (m *Manager) postJSON(ctx context.Context, s Server, path string, body, v any) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	resp, err := m.do(ctx, s, http.MethodPost, path, body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return json.NewDecoder(resp.Body).Decode(v)
}

// kind returns "ollama" or "llamacpp" (any OpenAI compatible server).
func (m *Manager) kind(ctx context.Context, s Server) string {
	if s.Kind == "ollama" || s.Kind == "llamacpp" {
		return s.Kind
	}
	m.mu.Lock()
	k, ok := m.kinds[s.URL]
	m.mu.Unlock()
	if ok {
		return k
	}
	k = "llamacpp"
	var v struct{ Version string }
	if m.getJSON(ctx, s, "/api/version", &v) == nil && v.Version != "" {
		k = "ollama"
	}
	m.mu.Lock()
	m.kinds[s.URL] = k
	m.mu.Unlock()
	return k
}

// Parallel is the number of conversations a server runs at once (1 when unknown).
func (m *Manager) Parallel(id string) int {
	s, err := m.server(id)
	if err != nil || s.Parallel < 1 {
		return 1
	}
	return s.Parallel
}

// ServerContext is the context size set for a server (Ollama), 0 when unset.
func (m *Manager) ServerContext(id string) int {
	s, err := m.server(id)
	if err != nil {
		return 0
	}
	return s.Context
}
