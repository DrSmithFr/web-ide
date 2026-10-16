// Package config holds the pod configuration (~/.web-ide/config.json) and the pairing token.
package config

import (
	"crypto/rand"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"

	"github.com/DrSmithFr/web-ide/pod/internal/store"
)

const DefaultAddr = "127.0.0.1:4433"

// DefaultRelay is the hosted relay (docs/relay.md): what Settings > Remote access pre-fills for a
// new pod. Nothing connects to it until the user turns remote access on, and any other relay can
// be typed in its place.
const DefaultRelay = "wss://antennae.space"

type Config struct {
	Addr      string `json:"addr"`
	Workspace string `json:"workspace"`
	// PublicURL is the address the user opens the IDE at from anywhere, for the links given to
	// Claude Code; empty: the page of the pod on its relay (paired), else http://<addr>.
	PublicURL string `json:"publicUrl,omitempty"`
	// RelayURL is the relay the pod registers with (a ws:// or wss:// URL); empty: no relay.
	RelayURL string `json:"relayUrl,omitempty"`
	// LAN is the link to the other pods of the account on the local network.
	LAN LAN `json:"lan,omitzero"`
	// Gateway is the place of this pod among the pods of the account (Settings > Remote access).
	Gateway Gateway `json:"gateway,omitzero"`
}

// Gateway configures how this pod takes part in the multi-pod home page.
type Gateway struct {
	// Preferred marks this pod as the entry point of its site: the browser opens it first, and it
	// reaches the other pods of the site over the LAN.
	Preferred bool `json:"preferred,omitempty"`
}

// LAN configures the LAN link of a paired pod.
type LAN struct {
	// Addr is the listen address of the LAN port (default :4436; "off": no LAN link).
	Addr string `json:"addr,omitempty"`
	// Peers are addresses (host:port) typed by hand, for a network where mDNS fails.
	Peers []string `json:"peers,omitempty"`
}

func Load(st *store.Store) (*Config, error) {
	cfg := &Config{}
	if err := st.ReadJSON("config.json", cfg); err != nil && !store.IsNotExist(err) {
		return nil, err
	}
	if cfg.Addr == "" {
		cfg.Addr = DefaultAddr
	}
	if cfg.Workspace == "" {
		home, _ := os.UserHomeDir()
		cfg.Workspace = filepath.Join(home, "Apps")
	}
	return cfg, nil
}

func (c *Config) Save(st *store.Store) error { return st.WriteJSON("config.json", c) }

// ExpandHome turns a leading ~ into the user home directory.
func ExpandHome(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		home, _ := os.UserHomeDir()
		return filepath.Join(home, p[1:])
	}
	return p
}

// Token returns the pairing token, generating it on first start.
func Token(st *store.Store) (string, error) {
	data, err := os.ReadFile(st.Path("token"))
	if err == nil && len(strings.TrimSpace(string(data))) >= 32 {
		return strings.TrimSpace(string(data)), nil
	}
	buf := make([]byte, 24)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	tok := hex.EncodeToString(buf)
	return tok, st.WriteFile("token", []byte(tok+"\n"))
}
