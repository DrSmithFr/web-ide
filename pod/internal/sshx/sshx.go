// Package sshx opens SSH connections with the local key set. Private keys never leave the machine.
package sshx

import (
	"bufio"
	"errors"
	"fmt"
	"net"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
	"golang.org/x/crypto/ssh/knownhosts"

	"github.com/DrSmithFr/web-ide/pod/internal/i18n"
)

type Target struct {
	Host    string `json:"host"`
	Port    int    `json:"port"`
	User    string `json:"user"`
	Auth    string `json:"auth"` // agent (agent + default keys) | key | password
	KeyPath string `json:"keyPath,omitempty"`
}

func (t Target) key() string {
	return fmt.Sprintf("%s@%s:%d/%s/%s", t.User, t.Host, t.Port, t.Auth, t.KeyPath)
}

// Creds carries secrets typed by the user. They stay in the pod memory.
type Creds struct {
	Password   string `json:"password,omitempty"`
	Passphrase string `json:"passphrase,omitempty"`
}

// AuthRequired asks the web page to prompt for a secret, then retry.
type AuthRequired struct {
	Kind string // password | passphrase
	Msg  error  // the prompt, translated for the page
}

func (a *AuthRequired) Error() string { return a.Msg.Error() }

func home() string {
	h, _ := os.UserHomeDir()
	return h
}

func expand(p string) string {
	if strings.HasPrefix(p, "~/") {
		return filepath.Join(home(), p[2:])
	}
	return p
}

// Resolve fills the target from ~/.ssh/config (alias, HostName, User, Port, IdentityFile).
func Resolve(t Target) Target {
	if h, ok := ConfigHosts()[t.Host]; ok {
		if h.HostName != "" {
			t.Host = h.HostName
		}
		if t.User == "" {
			t.User = h.User
		}
		if t.Port == 0 || t.Port == 22 {
			if h.Port != 0 {
				t.Port = h.Port
			}
		}
		if t.KeyPath == "" && h.IdentityFile != "" && t.Auth != "password" {
			t.KeyPath = h.IdentityFile
		}
	}
	if t.Port == 0 {
		t.Port = 22
	}
	if t.User == "" {
		if u, err := user.Current(); err == nil {
			t.User = u.Username
		}
	}
	if t.Auth == "" {
		t.Auth = "agent"
	}
	return t
}

func loadKey(path, passphrase string) (ssh.Signer, error) {
	data, err := os.ReadFile(expand(path))
	if err != nil {
		return nil, err
	}
	signer, err := ssh.ParsePrivateKey(data)
	var missing *ssh.PassphraseMissingError
	if errors.As(err, &missing) {
		if passphrase == "" {
			return nil, &AuthRequired{Kind: "passphrase", Msg: i18n.Errorf("Passphrase of the key %s", path)}
		}
		return ssh.ParsePrivateKeyWithPassphrase(data, []byte(passphrase))
	}
	return signer, err
}

var defaultKeys = []string{"~/.ssh/id_ed25519", "~/.ssh/id_ecdsa", "~/.ssh/id_rsa"}

func authMethods(t Target, c Creds) ([]ssh.AuthMethod, error) {
	switch t.Auth {
	case "password":
		if c.Password == "" {
			return nil, &AuthRequired{Kind: "password", Msg: i18n.Errorf("Password of %s@%s", t.User, t.Host)}
		}
		return []ssh.AuthMethod{ssh.Password(c.Password)}, nil
	case "key":
		signer, err := loadKey(t.KeyPath, c.Passphrase)
		if err != nil {
			return nil, err
		}
		return []ssh.AuthMethod{ssh.PublicKeys(signer)}, nil
	}
	// agent: the running agent first, then the unencrypted default keys.
	var signers []ssh.Signer
	if sock := os.Getenv("SSH_AUTH_SOCK"); sock != "" {
		if conn, err := net.Dial("unix", sock); err == nil {
			if s, err := agent.NewClient(conn).Signers(); err == nil {
				signers = append(signers, s...)
			}
		}
	}
	keys := defaultKeys
	if t.KeyPath != "" {
		keys = append([]string{t.KeyPath}, keys...)
	}
	for _, k := range keys {
		if s, err := loadKey(k, c.Passphrase); err == nil {
			signers = append(signers, s)
		}
	}
	if len(signers) == 0 {
		return nil, i18n.New("no SSH key available (empty agent and no readable default key)")
	}
	return []ssh.AuthMethod{ssh.PublicKeys(signers...)}, nil
}

// HostKeys checks ~/.ssh/known_hosts, then the pod's own file where unknown hosts are
// recorded on first use. A changed key is always refused.
type HostKeys struct {
	mu   sync.Mutex
	file string
}

func NewHostKeys(podFile string) *HostKeys { return &HostKeys{file: podFile} }

func (h *HostKeys) callback(hostname string, remote net.Addr, key ssh.PublicKey) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	var files []string
	for _, f := range []string{expand("~/.ssh/known_hosts"), h.file} {
		if _, err := os.Stat(f); err == nil {
			files = append(files, f)
		}
	}
	if len(files) > 0 {
		cb, err := knownhosts.New(files...)
		if err != nil {
			return err
		}
		err = cb(hostname, remote, key)
		var ke *knownhosts.KeyError
		if err == nil {
			return nil
		}
		if !errors.As(err, &ke) || len(ke.Want) > 0 {
			return i18n.Errorf("host key refused for %s: %w", hostname, err)
		}
	}
	f, err := os.OpenFile(h.file, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = fmt.Fprintln(f, knownhosts.Line([]string{knownhosts.Normalize(hostname)}, key))
	return err
}

func Dial(t Target, c Creds, hk *HostKeys) (*ssh.Client, error) {
	t = Resolve(t)
	methods, err := authMethods(t, c)
	if err != nil {
		return nil, err
	}
	cfg := &ssh.ClientConfig{
		User:            t.User,
		Auth:            methods,
		HostKeyCallback: hk.callback,
		Timeout:         10 * time.Second,
	}
	client, err := ssh.Dial("tcp", net.JoinHostPort(t.Host, strconv.Itoa(t.Port)), cfg)
	if err != nil && t.Auth == "password" && strings.Contains(err.Error(), "unable to authenticate") {
		return nil, &AuthRequired{Kind: "password", Msg: i18n.Errorf("Password refused, try again for %s@%s", t.User, t.Host)}
	}
	return client, err
}

// Pool shares one SSH connection per target (project files, consoles, LSP, DB tunnels).
type Pool struct {
	mu      sync.Mutex
	hk      *HostKeys
	clients map[string]*ssh.Client
	creds   map[string]Creds
}

func NewPool(hk *HostKeys) *Pool {
	return &Pool{hk: hk, clients: map[string]*ssh.Client{}, creds: map[string]Creds{}}
}

// Get returns a live connection, dialing it when needed. Creds given once are remembered
// for the life of the pod.
func (p *Pool) Get(t Target, c Creds) (*ssh.Client, error) {
	k := Resolve(t).key()
	p.mu.Lock()
	if c.Password == "" && c.Passphrase == "" {
		c = p.creds[k]
	}
	client := p.clients[k]
	p.mu.Unlock()
	if client != nil {
		if _, _, err := client.SendRequest("keepalive@openssh.com", true, nil); err == nil {
			return client, nil
		}
		client.Close()
	}
	client, err := Dial(t, c, p.hk)
	if err != nil {
		return nil, err
	}
	p.mu.Lock()
	p.clients[k] = client
	p.creds[k] = c
	p.mu.Unlock()
	return client, nil
}

func (p *Pool) CloseAll() {
	p.mu.Lock()
	defer p.mu.Unlock()
	for k, c := range p.clients {
		c.Close()
		delete(p.clients, k)
	}
}

type ConfigHost struct {
	Alias        string `json:"alias"`
	HostName     string `json:"hostName,omitempty"`
	User         string `json:"user,omitempty"`
	Port         int    `json:"port,omitempty"`
	IdentityFile string `json:"identityFile,omitempty"`
}

// ConfigHosts reads the explicit Host entries of ~/.ssh/config (no wildcard, no Include).
func ConfigHosts() map[string]ConfigHost {
	out := map[string]ConfigHost{}
	f, err := os.Open(expand("~/.ssh/config"))
	if err != nil {
		return out
	}
	defer f.Close()
	var cur []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(strings.Replace(line, "=", " ", 1))
		if len(fields) < 2 {
			continue
		}
		key, val := strings.ToLower(fields[0]), fields[1]
		if key == "host" {
			cur = nil
			for _, a := range fields[1:] {
				if !strings.ContainsAny(a, "*?!") {
					cur = append(cur, a)
					if _, ok := out[a]; !ok {
						out[a] = ConfigHost{Alias: a}
					}
				}
			}
			continue
		}
		if key == "match" {
			cur = nil
			continue
		}
		for _, a := range cur {
			h := out[a]
			switch key {
			case "hostname":
				h.HostName = val
			case "user":
				h.User = val
			case "port":
				h.Port, _ = strconv.Atoi(val)
			case "identityfile":
				if h.IdentityFile == "" {
					h.IdentityFile = val
				}
			}
			out[a] = h
		}
	}
	return out
}

// LocalKeys lists the private keys found in ~/.ssh (names only, never the content).
func LocalKeys() []string {
	var out []string
	des, err := os.ReadDir(expand("~/.ssh"))
	if err != nil {
		return out
	}
	for _, de := range des {
		n := de.Name()
		if de.IsDir() || strings.HasSuffix(n, ".pub") || n == "config" || strings.HasPrefix(n, "known_hosts") || n == "authorized_keys" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(expand("~/.ssh"), n))
		if err == nil && strings.Contains(string(data[:min(len(data), 64)]), "PRIVATE KEY") {
			out = append(out, "~/.ssh/"+n)
		}
	}
	return out
}
