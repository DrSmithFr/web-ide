package ai

import (
	"github.com/DrSmithFr/web-ide/pod/acp"
	"github.com/DrSmithFr/web-ide/pod/assistant"
	"github.com/DrSmithFr/web-ide/pod/runtime"
)

// What the tests of the pod ask of the conversations of the assistant: whether one runs, where
// it works, the approval it waits for.

// Running tells whether the conversation id runs now.
func (s *A) Running(id string) bool { return s.run(id) != nil }

// RunProject is the project the tools of a conversation work in (runProject).
func (s *A) RunProject(chat *assistant.Chat, root, project string) string {
	return s.runProject(chat, root, project)
}

// Approval is the id of the approval a running conversation waits for ("" for none).
func (s *A) Approval(id string) string {
	r := s.run(id)
	if r == nil {
		return ""
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.chat.Approval == nil {
		return ""
	}
	return r.chat.Approval.ID
}

func hasGraph(qs []assistant.Question) bool {
	for _, q := range qs {
		if q.ID != "" {
			return true
		}
	}
	return false
}

// AgentsOf is the manager of the sessions of the Agents tool of the project of rt.
func (s *A) AgentsOf(rt *runtime.Runtime) *acp.Manager { return s.agentsOf(rt) }

// NoteAsks records the questions of a conversation saved from message from (noteAsks).
func (s *A) NoteAsks(root string, c *assistant.Chat, from int) { s.noteAsks(root, c, from) }
