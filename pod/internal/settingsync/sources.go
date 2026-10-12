package settingsync

import (
	"encoding/json"

	"github.com/DrSmithFr/web-ide/pod/internal/identity"
	"github.com/DrSmithFr/web-ide/pod/internal/llm"
	"github.com/DrSmithFr/web-ide/pod/internal/settings"
	"github.com/DrSmithFr/web-ide/pod/internal/store"
)

// Source is one kind of data a pod syncs: how to read its content and how to apply a merged one.
// The settings and the vault are two sources; the loop is the same for both.
type Source interface {
	Kind() string
	MapFields() []string
	// Enabled reports whether the source is synced. Off: nothing is put, and the blob is deleted.
	Enabled() bool
	Read() json.RawMessage
	Write(content json.RawMessage, label string) error
	// Clear empties the source (the blob was deleted on the other side).
	Clear(label string) error
}

// SettingsSource syncs the settings of the pod. Every applied change goes through Settings.Save
// with a label, so the history and the rollback keep working with the changes of the other pods.
// The notify hook tells the page that a change came from another pod: without it the settings of the
// window would stay on the values it read at open.
type SettingsSource struct {
	s      *settings.Settings
	on     func() bool
	notify func(settings.Entry)
}

// Map fields of the settings: their entries merge per key. The per-device layout and the sync
// switches are maps too, so the entry of a device does not overwrite the entry of another.
var settingsMaps = []string{"tokenColors", "syntax", "keyboard/overrides", "devices", "sync"}

func NewSettingsSource(s *settings.Settings, on func() bool, notify func(settings.Entry)) *SettingsSource {
	return &SettingsSource{s: s, on: on, notify: notify}
}

func (s *SettingsSource) Kind() string          { return identity.KindSettings }
func (s *SettingsSource) MapFields() []string   { return settingsMaps }
func (s *SettingsSource) Enabled() bool         { return s.on() }
func (s *SettingsSource) Read() json.RawMessage { return s.s.Current().Settings }

func (s *SettingsSource) Write(content json.RawMessage, label string) error {
	e, err := s.s.Save(content, label)
	if err == nil && s.notify != nil {
		s.notify(e)
	}
	return err
}

func (s *SettingsSource) Clear(label string) error {
	e, err := s.s.Save(json.RawMessage(`{}`), label)
	if err == nil && s.notify != nil {
		s.notify(e)
	}
	return err
}

// VaultSource syncs the vault: the API keys of the model servers (by server id) and the remembered
// database secrets. It is opt-in; off, nothing is put and the blob is deleted. Its content is not a
// whole document but a set of entries, so it merges per entry.
type VaultSource struct {
	st     *store.Store
	llm    *llm.Manager
	on     func() bool
	notify func()
}

var vaultMaps = []string{"llm", "secrets"}

func NewVaultSource(st *store.Store, m *llm.Manager, on func() bool, notify func()) *VaultSource {
	return &VaultSource{st: st, llm: m, on: on, notify: notify}
}

func (v *VaultSource) Kind() string        { return identity.KindVault }
func (v *VaultSource) MapFields() []string { return vaultMaps }
func (v *VaultSource) Enabled() bool       { return v.on() }

// Read is the vault as the merge engine sees it: one entry per server id and per stored secret.
func (v *VaultSource) Read() json.RawMessage {
	b, err := json.Marshal(map[string]any{"llm": v.llm.Keys(), "secrets": v.secrets()})
	if err != nil {
		return json.RawMessage(`{}`)
	}
	return b
}

func (v *VaultSource) secrets() map[string]json.RawMessage {
	all := map[string]json.RawMessage{}
	if err := v.st.ReadJSON("secrets.json", &all); err != nil && !store.IsNotExist(err) {
		return nil
	}
	return all
}

// Write applies the merged entries: a key for a server this pod does not know is kept in the vault
// but cannot be applied, and a secret of another project is left alone.
func (v *VaultSource) Write(content json.RawMessage, label string) error {
	var vault map[string]map[string]json.RawMessage
	if err := json.Unmarshal(content, &vault); err != nil {
		return err
	}
	keys := map[string]string{}
	for id, raw := range vault["llm"] {
		var key string
		if err := json.Unmarshal(raw, &key); err == nil && key != "" {
			keys[id] = key
		}
	}
	if len(keys) > 0 {
		if err := v.llm.ApplyKeys(keys); err != nil {
			return err
		}
	}
	if len(vault["secrets"]) > 0 {
		all := v.secrets()
		for k, raw := range vault["secrets"] {
			all[k] = raw
		}
		if err := v.st.WriteJSON("secrets.json", all); err != nil {
			return err
		}
	}
	if v.notify != nil {
		v.notify()
	}
	_ = label
	return nil
}

// Clear applies an empty vault: there is no entry to apply. A pod never drops a secret it holds and
// the other side has not seen — a vault that was off has nothing to delete.
func (v *VaultSource) Clear(label string) error {
	_ = label
	return nil
}
