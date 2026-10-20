package server

import (
	"encoding/json"

	"github.com/DrSmithFr/web-ide/plugins/ai"
	"github.com/DrSmithFr/web-ide/pod/assistant"
	"github.com/DrSmithFr/web-ide/pod/llm"
)

// aiOf is the AI plugin of the pod of the tests.
func aiOf(s *Server) *ai.A {
	p, _ := s.Plugins.Get("ai")
	return p.(*ai.A)
}

// saveTestChat writes a conversation of the assistant as the tests set it.
func saveTestChat(s *Server, loc llm.ChatLocation, c *assistant.Chat) error {
	data, err := json.Marshal(c)
	if err != nil {
		return err
	}
	return s.LLM.SaveChat(loc, data)
}
