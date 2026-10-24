package server

import (
	"encoding/json"

	"antennae.space/ide/plugins/ai"
	"antennae.space/ide/plugins/ai/engine"
	"antennae.space/ide/plugins/ai/llm"
)

// aiOf is the AI plugin of the pod of the tests.
func aiOf(s *Server) *ai.A {
	p, _ := s.Plugins.Get("ai")
	return p.(*ai.A)
}

// llmOf is the model servers and the conversations of the pod of the tests (the AI's).
func llmOf(s *Server) *llm.Manager { return aiOf(s).LLM }

// saveTestChat writes a conversation of the assistant as the tests set it.
func saveTestChat(s *Server, loc llm.ChatLocation, c *engine.Chat) error {
	data, err := json.Marshal(c)
	if err != nil {
		return err
	}
	return llmOf(s).SaveChat(loc, data)
}
