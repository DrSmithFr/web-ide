package push

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
)

// The kinds of notifications, each one a switch per device.
const (
	Finished = "finished" // a turn of the assistant ended
	Asks     = "asks"     // the assistant waits for the user (a change, a command, a question)
	Tests    = "tests"    // a saved command flagged test failed
	Feedback = "feedback" // the assistant or Claude Code added feedback to a ticket
)

// Kinds are all the kinds, in the order of the settings.
var Kinds = []string{Finished, Asks, Tests, Feedback}

// Device is the subscription of one browser (its device key) and its choices: the id the relay
// gave its endpoint, the keys the payload is encrypted for, the language of its texts.
type Device struct {
	Sub    string          `json:"sub"`
	P256dh string          `json:"p256dh"` // base64url
	Auth   string          `json:"auth"`   // base64url
	Kinds  map[string]bool `json:"kinds"`
	Lang   string          `json:"lang,omitempty"`
}

// Store keeps the subscriptions of the browsers in push.json of the data folder (0600).
type Store struct {
	path    string
	mu      sync.Mutex
	devices map[string]*Device
}

// Open reads push.json (none yet: no subscription).
func Open(dir string) (*Store, error) {
	s := &Store{path: filepath.Join(dir, "push.json"), devices: map[string]*Device{}}
	b, err := os.ReadFile(s.path)
	if os.IsNotExist(err) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	var f struct {
		Devices map[string]*Device `json:"devices"`
	}
	if err := json.Unmarshal(b, &f); err != nil {
		return nil, err
	}
	if f.Devices != nil {
		s.devices = f.Devices
	}
	return s, nil
}

func (s *Store) save() error {
	b, _ := json.MarshalIndent(map[string]any{"devices": s.devices}, "", "  ")
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

// Subscribe records the subscription of a device. A new device gets every kind on; one that
// subscribes again keeps its choices.
func (s *Store) Subscribe(device string, d Device) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if old := s.devices[device]; old != nil && d.Kinds == nil {
		d.Kinds = old.Kinds
	}
	if d.Kinds == nil {
		d.Kinds = map[string]bool{}
		for _, k := range Kinds {
			d.Kinds[k] = true
		}
	}
	s.devices[device] = &d
	return s.save()
}

// Unsubscribe forgets a device.
func (s *Store) Unsubscribe(device string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.devices, device)
	return s.save()
}

// SetKind turns one kind on or off for a device.
func (s *Store) SetKind(device, kind string, on bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	d := s.devices[device]
	if d == nil {
		return nil
	}
	d.Kinds[kind] = on
	return s.save()
}

// Get answers the subscription of a device.
func (s *Store) Get(device string) (Device, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	d := s.devices[device]
	if d == nil {
		return Device{}, false
	}
	return *d, true
}

// Gone forgets the devices of a subscription the relay dropped (the push service said 410).
func (s *Store) Gone(sub string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for k, d := range s.devices {
		if d.Sub == sub {
			delete(s.devices, k)
		}
	}
	_ = s.save()
}

// For answers the devices that want a kind, by device key.
func (s *Store) For(kind string) map[string]Device {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := map[string]Device{}
	for k, d := range s.devices {
		if d.Kinds[kind] {
			out[k] = *d
		}
	}
	return out
}
