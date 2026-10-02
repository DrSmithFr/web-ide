// Package config holds the pod configuration (~/.web-ide/config.json) and the pairing token.
package config

import (
	"crypto/rand"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"

	"webide/pod/internal/store"
)

const DefaultAddr = "127.0.0.1:4433"

type Config struct {
	Addr      string `json:"addr"`
	Workspace string `json:"workspace"`
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
