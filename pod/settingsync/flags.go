package settingsync

import (
	"encoding/json"

	"github.com/DrSmithFr/web-ide/lib/identity"
	"github.com/DrSmithFr/web-ide/pod/settings"
)

// Flags reads the sync switches from the settings document (the "sync" object): settings sync is on
// by default, the vault is opt-in. The page changes them like any other setting, so they reach the
// other pods through the same blob.
type Flags struct {
	s *settings.Settings
}

func NewFlags(s *settings.Settings) *Flags { return &Flags{s: s} }

// On reports whether a kind is synced.
func (f *Flags) On(kind string) bool {
	var doc map[string]any
	if err := json.Unmarshal(f.s.Current().Settings, &doc); err != nil {
		return false
	}
	sync, _ := doc["sync"].(map[string]any)
	on, ok := sync[kind].(bool)
	if !ok {
		return kind == identity.KindSettings
	}
	return on
}

// Set writes one switch and records a snapshot, so the change is part of the history like any other
// setting.
func (f *Flags) Set(kind string, on bool) error {
	var doc map[string]any
	if err := json.Unmarshal(f.s.Current().Settings, &doc); err != nil {
		doc = map[string]any{}
	}
	sync, _ := doc["sync"].(map[string]any)
	if sync == nil {
		sync = map[string]any{}
	}
	sync[kind] = on
	doc["sync"] = sync
	_, err := f.s.Save(marshal(doc), "sync switch")
	return err
}
