package assistant

import (
	"encoding/json"

	"github.com/DrSmithFr/web-ide/pod/llm"
)

// LoadChat reads a conversation of the assistant kept by m.
func LoadChat(m *llm.Manager, loc llm.ChatLocation, id string) (*Chat, error) {
	raw, err := m.GetChat(loc, id)
	if err != nil {
		return nil, err
	}
	var c Chat
	if err := json.Unmarshal(raw, &c); err != nil {
		return nil, err
	}
	return &c, nil
}
