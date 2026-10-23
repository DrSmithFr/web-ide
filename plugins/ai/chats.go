package ai

import (
	"encoding/json"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/DrSmithFr/web-ide/pod/assistant"
	"github.com/DrSmithFr/web-ide/pod/llm"
	"github.com/DrSmithFr/web-ide/pod/plugins"
)

// ChatLoc is where the conversations of a project are kept, and the project whose windows
// follow them: a worktree keeps its conversations with those of its parent.
func (s *A) ChatLoc(projectID string) (llm.ChatLocation, string, error) {
	root, err := s.Projects.Root(projectID)
	if err != nil {
		return llm.ChatLocation{}, "", err
	}
	l := llm.ChatLocation{Project: root.ID}
	if root.Type == "local" {
		l.IdeDir = filepath.Join(root.Path, ".ide")
	}
	return l, root.ID, nil
}

// asking are the conversations asking the user (ask_user), so that their questions change
// only when one starts or stops asking.
type asking struct {
	mu    sync.Mutex
	chats map[string]bool
}

// noteAsks: a conversation of the assistant was saved; its questions changed when it asks
// or stops asking, or when a message with questions is among those published.
func (s *A) noteAsks(root string, c *assistant.Chat, from int) {
	pending, touched := false, false
	for i, m := range c.Messages {
		if m.AskState == "pending" {
			pending = true
		}
		if i >= from && from >= 0 && m.AskState != "" {
			touched = true
		}
	}
	s.asking.mu.Lock()
	prev := s.asking.chats[c.ID]
	if pending {
		s.asking.chats[c.ID] = true
	} else {
		delete(s.asking.chats, c.ID)
	}
	s.asking.mu.Unlock()
	if prev != pending || touched {
		s.pod.QuestionsChanged(root, "")
	}
}

// chatQuestions are the ask_user questions of the conversations of root waiting for the
// user (answered through assistant.answer).
func (s *A) chatQuestions(root string) []plugins.Question {
	loc, _, err := s.ChatLoc(root)
	if err != nil {
		return nil
	}
	list, err := s.LLM.ListChats(loc)
	if err != nil {
		return nil
	}
	var out []plugins.Question
	for _, info := range list {
		if !info.Waiting {
			continue
		}
		c, err := s.loadChat(loc, info.ID)
		if err != nil {
			continue
		}
		last := ""
		for i, m := range c.Messages {
			if m.Role == "assistant" {
				if t := textOf(m.Content); strings.TrimSpace(t) != "" {
					last = t
				}
			}
			if m.AskState == "pending" {
				out = append(out, plugins.Question{ID: "chat:" + c.ID + ":" + strconv.Itoa(i), Source: "assistant", Project: root, Session: c.ID, Index: i,
					Name: "Assistant", Title: c.Title, Kind: "ask", Questions: m.Questions, Last: last, Time: m.At})
			}
		}
	}
	return out
}

// textOf is the text of the content of a message (a string; parts are skipped).
func textOf(raw json.RawMessage) string {
	var s string
	_ = json.Unmarshal(raw, &s)
	return s
}
