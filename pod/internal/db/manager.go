// Package db is the backend of the Database explorer tool: SQLite, Postgres and Redis
// connections, optionally through an SSH tunnel, all opened by the pod.
package db

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"

	"webide/pod/internal/execx"
	"webide/pod/internal/fsx"
	"webide/pod/internal/sshx"
	"webide/pod/internal/store"
)

type SSHTunnel struct {
	Enabled bool   `json:"enabled"`
	Host    string `json:"host"`
	Port    int    `json:"port"`
	User    string `json:"user"`
	Auth    string `json:"auth"` // agent | key | password
	KeyPath string `json:"keyPath,omitempty"`
}

// ConnConfig is stored in .ide/connections.json of the project. No secret in it.
type ConnConfig struct {
	ID               string     `json:"id"`
	Name             string     `json:"name"`
	Kind             string     `json:"kind"` // sqlite | postgres | redis
	Path             string     `json:"path,omitempty"`
	Host             string     `json:"host,omitempty"`
	Port             int        `json:"port,omitempty"`
	Database         string     `json:"database,omitempty"`
	User             string     `json:"user,omitempty"`
	SSLMode          string     `json:"sslMode,omitempty"`
	RedisDB          int        `json:"redisDb,omitempty"`
	RememberPassword bool       `json:"rememberPassword"`
	SSH              *SSHTunnel `json:"ssh,omitempty"`
}

// Secret is kept in ~/.web-ide/secrets.json (0600) when remembered, in memory otherwise.
type Secret struct {
	Password      string `json:"password,omitempty"`
	SSHPassword   string `json:"sshPassword,omitempty"`
	SSHPassphrase string `json:"sshPassphrase,omitempty"`
}

func (s Secret) empty() bool { return s == Secret{} }

// NeedPassword asks the page for the database password.
type NeedPassword struct{ Prompt string }

func (e *NeedPassword) Error() string { return e.Prompt }

type Status struct {
	State string `json:"state"` // untested | connected | error | closed
	Error string `json:"error,omitempty"`
}

type ConnView struct {
	ConnConfig
	Status    Status `json:"status"`
	HasSecret bool   `json:"hasSecret"`
}

type Deps struct {
	FS        fsx.FS
	Root      string
	ProjectID string
	Local     bool
	Runner    execx.Runner
	Pool      *sshx.Pool
	Store     *store.Store
}

type console struct {
	mu     sync.Mutex
	id     string
	connID string
	db     string
	sess   Session
	auto   bool
	cancel context.CancelFunc
}

type Manager struct {
	d        Deps
	mu       sync.Mutex
	configs  []ConnConfig
	loaded   bool
	live     map[string]Driver
	status   map[string]Status
	secrets  map[string]Secret
	consoles map[string]*console
	cache    map[string]*Result
}

func NewManager(d Deps) *Manager {
	return &Manager{d: d, live: map[string]Driver{}, status: map[string]Status{}, secrets: map[string]Secret{},
		consoles: map[string]*console{}, cache: map[string]*Result{}}
}

func (m *Manager) file() string { return path.Join(m.d.Root, ".ide", "connections.json") }

func (m *Manager) load() {
	if m.loaded {
		return
	}
	m.loaded = true
	if data, err := m.d.FS.Read(m.file()); err == nil {
		_ = json.Unmarshal(data, &m.configs)
	}
}

func (m *Manager) saveConfigs() error {
	data, _ := json.MarshalIndent(m.configs, "", "  ")
	return m.d.FS.Write(m.file(), data)
}

// ---------- secrets ----------

func (m *Manager) secretKey(id string) string { return m.d.ProjectID + "/" + id }

func (m *Manager) storedSecrets() map[string]Secret {
	all := map[string]Secret{}
	_ = m.d.Store.ReadJSON("secrets.json", &all)
	return all
}

func (m *Manager) secret(id string) Secret {
	if s, ok := m.secrets[id]; ok {
		return s
	}
	return m.storedSecrets()[m.secretKey(id)]
}

func (m *Manager) putSecret(cfg ConnConfig, s Secret) {
	all := m.storedSecrets()
	if cfg.RememberPassword && !s.empty() {
		all[m.secretKey(cfg.ID)] = s
	} else if !cfg.RememberPassword {
		delete(all, m.secretKey(cfg.ID))
	}
	_ = m.d.Store.WriteJSON("secrets.json", all)
	if !s.empty() {
		m.secrets[cfg.ID] = s
	}
}

// ---------- configs ----------

func (m *Manager) find(id string) (ConnConfig, bool) {
	m.load()
	for _, c := range m.configs {
		if c.ID == id {
			return c, true
		}
	}
	return ConnConfig{}, false
}

func (m *Manager) List() []ConnView {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.load()
	out := make([]ConnView, 0, len(m.configs))
	for _, c := range m.configs {
		st, ok := m.status[c.ID]
		if !ok {
			st = Status{State: "untested"}
		}
		out = append(out, ConnView{ConnConfig: c, Status: st, HasSecret: !m.secret(c.ID).empty()})
	}
	return out
}

func validate(c *ConnConfig) error {
	switch c.Kind {
	case "sqlite":
		if c.Path == "" {
			return errors.New("chemin du fichier SQLite manquant")
		}
		c.SSH = nil
	case "postgres", "redis":
		if c.Host == "" {
			return errors.New("hôte manquant")
		}
	default:
		return errors.New("type de connexion inconnu")
	}
	if c.Name == "" {
		c.Name = c.Host
		if c.Kind == "sqlite" {
			c.Name = path.Base(c.Path)
		} else if c.Database != "" {
			c.Name = c.Database + "@" + c.Host
		}
	}
	if c.SSH != nil && !c.SSH.Enabled {
		c.SSH = nil
	}
	return nil
}

func newID() string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// Save creates or replaces a connection. An empty secret keeps the known one.
func (m *Manager) Save(c ConnConfig, s Secret) (ConnView, error) {
	if err := validate(&c); err != nil {
		return ConnView{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.load()
	if c.ID == "" {
		c.ID = newID()
		m.configs = append(m.configs, c)
	} else {
		found := false
		for i := range m.configs {
			if m.configs[i].ID == c.ID {
				m.configs[i] = c
				found = true
			}
		}
		if !found {
			m.configs = append(m.configs, c)
		}
		m.closeLocked(c.ID)
		delete(m.status, c.ID)
	}
	if s.empty() {
		s = m.secret(c.ID)
	}
	m.putSecret(c, s)
	if err := m.saveConfigs(); err != nil {
		return ConnView{}, err
	}
	return ConnView{ConnConfig: c, Status: Status{State: "untested"}, HasSecret: !s.empty()}, nil
}

func (m *Manager) Delete(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.load()
	m.closeLocked(id)
	for i := range m.configs {
		if m.configs[i].ID == id {
			m.configs = append(m.configs[:i], m.configs[i+1:]...)
			break
		}
	}
	delete(m.status, id)
	delete(m.secrets, id)
	all := m.storedSecrets()
	delete(all, m.secretKey(id))
	_ = m.d.Store.WriteJSON("secrets.json", all)
	return m.saveConfigs()
}

// ---------- connections ----------

func (m *Manager) dialer(c ConnConfig, s Secret) (dialFunc, error) {
	if c.SSH == nil {
		return nil, nil
	}
	client, err := m.d.Pool.Get(sshx.Target{Host: c.SSH.Host, Port: c.SSH.Port, User: c.SSH.User, Auth: c.SSH.Auth, KeyPath: c.SSH.KeyPath},
		sshx.Creds{Password: s.SSHPassword, Passphrase: s.SSHPassphrase})
	if err != nil {
		return nil, err
	}
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		return client.DialContext(ctx, "tcp", addr)
	}, nil
}

func authFailure(err error) bool {
	msg := strings.ToLower(err.Error())
	for _, s := range []string{"password authentication failed", "no password supplied", "28p01", "noauth", "wrongpass", "invalid password", "invalid username-password"} {
		if strings.Contains(msg, s) {
			return true
		}
	}
	return false
}

func (m *Manager) open(ctx context.Context, c ConnConfig, s Secret) (Driver, error) {
	switch c.Kind {
	case "sqlite":
		p := c.Path
		if !path.IsAbs(p) {
			p = path.Join(m.d.Root, p)
		}
		return openSQLite(ctx, p, m.d.Local, m.d.Runner)
	}
	dial, err := m.dialer(c, s)
	if err != nil {
		return nil, err
	}
	var drv Driver
	if c.Kind == "postgres" {
		drv, err = openPostgres(ctx, c, s.Password, dial)
	} else {
		drv, err = openRedis(ctx, c, s.Password, dial)
	}
	if err != nil && authFailure(err) {
		prompt := "Mot de passe de " + c.Name
		if s.Password != "" {
			prompt = "Mot de passe refusé pour " + c.Name
		}
		return nil, &NeedPassword{Prompt: prompt}
	}
	return drv, err
}

// Test opens then closes a connection described by the form, without saving it.
func (m *Manager) Test(ctx context.Context, c ConnConfig, s Secret) (string, error) {
	if err := validate(&c); err != nil {
		return "", err
	}
	m.mu.Lock()
	if s.empty() && c.ID != "" {
		s = m.secret(c.ID)
	}
	m.mu.Unlock()
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	start := time.Now()
	drv, err := m.open(ctx, c, s)
	if err != nil {
		return "", err
	}
	drv.Close()
	return fmt.Sprintf("Connexion réussie (%d ms)", time.Since(start).Milliseconds()), nil
}

// Connect opens a saved connection. A secret given here is remembered for the session
// (and on disk when the connection asks for it).
func (m *Manager) Connect(ctx context.Context, id string, s Secret) error {
	m.mu.Lock()
	c, ok := m.find(id)
	if !ok {
		m.mu.Unlock()
		return errors.New("connexion introuvable")
	}
	if _, ok := m.live[id]; ok {
		m.mu.Unlock()
		return nil
	}
	if s.empty() {
		s = m.secret(id)
	}
	m.mu.Unlock()
	drv, err := m.open(ctx, c, s)
	m.mu.Lock()
	defer m.mu.Unlock()
	if err != nil {
		var np *NeedPassword
		var ar *sshx.AuthRequired
		if !errors.As(err, &np) && !errors.As(err, &ar) {
			m.status[id] = Status{State: "error", Error: err.Error()}
		}
		return err
	}
	if !s.empty() {
		m.putSecret(c, s)
	}
	m.live[id] = drv
	m.status[id] = Status{State: "connected"}
	return nil
}

func (m *Manager) driver(ctx context.Context, id string) (Driver, error) {
	m.mu.Lock()
	drv, ok := m.live[id]
	m.mu.Unlock()
	if ok {
		return drv, nil
	}
	return m.reconnect(ctx, id)
}

// openDriver is driver for the consoles and table views: a connection closed by the user
// stays closed until reopened from the tree.
func (m *Manager) openDriver(ctx context.Context, id string) (Driver, error) {
	m.mu.Lock()
	drv, ok := m.live[id]
	closed := m.status[id].State == "closed"
	m.mu.Unlock()
	if ok {
		return drv, nil
	}
	if closed {
		return nil, errors.New("connexion fermée : la rouvrir depuis le Database explorer")
	}
	return m.reconnect(ctx, id)
}

func (m *Manager) reconnect(ctx context.Context, id string) (Driver, error) {
	if err := m.Connect(ctx, id, Secret{}); err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.live[id], nil
}

func (m *Manager) closeLocked(id string) {
	if drv, ok := m.live[id]; ok {
		drv.Close()
		delete(m.live, id)
	}
	for _, c := range m.consoles {
		if c.connID == id && c.sess != nil {
			c.sess.Close()
			c.sess = nil
		}
	}
	for k := range m.cache {
		if strings.HasPrefix(k, id+"|") {
			delete(m.cache, k)
		}
	}
}

// Disconnect closes a connection. Its consoles and table views stay open, disconnected.
func (m *Manager) Disconnect(id string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closeLocked(id)
	if _, ok := m.status[id]; ok {
		m.status[id] = Status{State: "closed"}
	}
}

func (m *Manager) CloseAll() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id := range m.live {
		m.closeLocked(id)
	}
}

// ---------- tree & table view ----------

func (m *Manager) Children(ctx context.Context, id string, n Node) ([]Node, error) {
	drv, err := m.driver(ctx, id)
	if err != nil {
		return nil, err
	}
	nodes, err := drv.Children(ctx, n)
	if nodes == nil {
		nodes = []Node{}
	}
	return nodes, err
}

func (m *Manager) DDL(ctx context.Context, id, db, table string) (string, error) {
	drv, err := m.driver(ctx, id)
	if err != nil {
		return "", err
	}
	return drv.DDL(ctx, db, table)
}

func (m *Manager) IndexDef(ctx context.Context, id, db, table, index string) (string, error) {
	drv, err := m.driver(ctx, id)
	if err != nil {
		return "", err
	}
	return drv.IndexDef(ctx, db, table, index)
}

// Page reads a page of a table. Pages are cached until a console modifies the data.
func (m *Manager) Page(ctx context.Context, id, db, table string, offset, limit int, refresh bool) (*Result, error) {
	key := id + "|" + db + "|" + table + "|" + strconv.Itoa(offset) + "|" + strconv.Itoa(limit)
	m.mu.Lock()
	if r, ok := m.cache[key]; ok && !refresh {
		m.mu.Unlock()
		return r, nil
	}
	m.mu.Unlock()
	drv, err := m.openDriver(ctx, id)
	if err != nil {
		return nil, err
	}
	r, err := drv.Page(ctx, db, table, offset, limit)
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	m.cache[key] = r
	m.mu.Unlock()
	return r, nil
}

// ---------- SQL consoles ----------

type ConsoleState struct {
	ID         string `json:"id"`
	ConnID     string `json:"connId"`
	DB         string `json:"db"`
	AutoCommit bool   `json:"autoCommit"`
	InTx       bool   `json:"inTx"`
	Connected  bool   `json:"connected"`
	Running    bool   `json:"running"`
}

func (c *console) state(connected bool) ConsoleState {
	st := ConsoleState{ID: c.id, ConnID: c.connID, DB: c.db, AutoCommit: c.auto, Connected: connected && c.sess != nil, Running: c.cancel != nil}
	if c.sess != nil {
		st.InTx = c.sess.InTx()
	}
	return st
}

// ConsoleOpen registers a console. id may be given to reattach a console after a pod restart.
func (m *Manager) ConsoleOpen(id, connID, db string) ConsoleState {
	m.mu.Lock()
	defer m.mu.Unlock()
	if c, ok := m.consoles[id]; ok {
		_, live := m.live[c.connID]
		return c.state(live)
	}
	if id == "" {
		id = newID()
	}
	c := &console{id: id, connID: connID, db: db, auto: true}
	m.consoles[id] = c
	_, live := m.live[connID]
	return c.state(live)
}

func (m *Manager) console(id string) (*console, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.consoles[id]
	if !ok {
		return nil, errors.New("console SQL inconnue")
	}
	return c, nil
}

func (m *Manager) session(ctx context.Context, c *console) (Session, error) {
	if c.sess != nil {
		return c.sess, nil
	}
	m.mu.Lock()
	drv, ok := m.live[c.connID]
	m.mu.Unlock()
	if !ok {
		return nil, errors.New("connexion fermée : la rouvrir depuis le Database explorer")
	}
	s, err := drv.Session(ctx, c.db)
	if err != nil {
		return nil, err
	}
	if !c.auto {
		if err := s.SetAutoCommit(false); err != nil {
			s.Close()
			return nil, err
		}
	}
	c.sess = s
	return s, nil
}

type HistoryEntry struct {
	TS         time.Time `json:"ts"`
	Query      string    `json:"query"`
	DurationMs float64   `json:"durationMs"`
	Rows       int64     `json:"rows"`
	Error      string    `json:"error,omitempty"`
}

func (m *Manager) historyFile(connID string) string {
	return "sql-history/" + m.d.ProjectID + "-" + connID + ".json"
}

func (m *Manager) addHistory(connID string, e HistoryEntry) {
	var h []HistoryEntry
	_ = m.d.Store.ReadJSON(m.historyFile(connID), &h)
	h = append(h, e)
	if len(h) > 500 {
		h = h[len(h)-500:]
	}
	_ = m.d.Store.WriteJSON(m.historyFile(connID), h)
}

func (m *Manager) History(connID string) []HistoryEntry {
	var h []HistoryEntry
	_ = m.d.Store.ReadJSON(m.historyFile(connID), &h)
	for i, j := 0, len(h)-1; i < j; i, j = i+1, j-1 {
		h[i], h[j] = h[j], h[i]
	}
	return h
}

func (m *Manager) Exec(ctx context.Context, id, query string) (*Result, ConsoleState, error) {
	c, err := m.console(id)
	if err != nil {
		return nil, ConsoleState{}, err
	}
	if _, err := m.openDriver(ctx, c.connID); err != nil {
		return nil, ConsoleState{}, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	sess, err := m.session(ctx, c)
	if err != nil {
		return nil, c.state(false), err
	}
	ctx, cancel := context.WithCancel(ctx)
	m.mu.Lock()
	c.cancel = cancel
	m.mu.Unlock()
	start := time.Now()
	res, err := sess.Exec(ctx, query)
	m.mu.Lock()
	c.cancel = nil
	if !readOnly(query) {
		for k := range m.cache {
			if strings.HasPrefix(k, c.connID+"|") {
				delete(m.cache, k)
			}
		}
	}
	m.mu.Unlock()
	cancel()
	e := HistoryEntry{TS: start, Query: strings.TrimSpace(query), DurationMs: ms(start)}
	if err != nil {
		if errors.Is(ctx.Err(), context.Canceled) {
			err = errors.New("instruction annulée")
		}
		e.Error = err.Error()
	} else {
		e.Rows = res.Affected
	}
	m.addHistory(c.connID, e)
	return res, c.state(true), err
}

// Cancel stops the statement running in a console.
func (m *Manager) Cancel(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.consoles[id]
	if !ok {
		return errors.New("console SQL inconnue")
	}
	if c.cancel != nil {
		c.cancel()
	}
	return nil
}

func (m *Manager) SetAutoCommit(ctx context.Context, id string, on bool) (ConsoleState, error) {
	c, err := m.console(id)
	if err != nil {
		return ConsoleState{}, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.sess != nil {
		if err := c.sess.SetAutoCommit(on); err != nil {
			return c.state(true), err
		}
	} else if !on {
		// Check the driver accepts manual transactions.
		if drv, err := m.driver(ctx, c.connID); err == nil {
			if s, err := drv.Session(ctx, c.db); err == nil {
				err = s.SetAutoCommit(false)
				s.Close()
				if err != nil {
					return c.state(true), err
				}
			}
		}
	}
	c.auto = on
	return c.state(true), nil
}

func (m *Manager) EndTx(ctx context.Context, id string, commit bool) (ConsoleState, error) {
	c, err := m.console(id)
	if err != nil {
		return ConsoleState{}, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.sess == nil || !c.sess.InTx() {
		return c.state(true), errors.New("aucune transaction ouverte")
	}
	if commit {
		err = c.sess.Commit(ctx)
	} else {
		err = c.sess.Rollback(ctx)
	}
	if commit && err == nil {
		m.mu.Lock()
		for k := range m.cache {
			if strings.HasPrefix(k, c.connID+"|") {
				delete(m.cache, k)
			}
		}
		m.mu.Unlock()
	}
	return c.state(true), err
}

func (m *Manager) ConsoleClose(id string) {
	m.mu.Lock()
	c, ok := m.consoles[id]
	delete(m.consoles, id)
	m.mu.Unlock()
	if ok {
		if c.cancel != nil {
			c.cancel()
		}
		c.mu.Lock()
		if c.sess != nil {
			c.sess.Close()
		}
		c.mu.Unlock()
	}
}
