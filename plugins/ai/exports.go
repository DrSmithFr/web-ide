package ai

import (
	"fmt"
	"github.com/DrSmithFr/web-ide/pod/assistant"
	"github.com/DrSmithFr/web-ide/pod/llm"
	"strings"
)

// What the core asks of the conversations of the assistant: the replies of Claude Code through
// the MCP (kanban_reply, kanban_answer) and whether a conversation runs.

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

// Reply sends a message of Claude Code (kanban_reply of the MCP) into a conversation: queued while it runs,
// else added (the questions waiting for the user are left aside) and the conversation started.
func (s *A) Reply(loc llm.ChatLocation, root, project, lang, id, text string) (string, error) {
	if strings.TrimSpace(text) == "" {
		return "", fmt.Errorf("message is empty")
	}
	if r := s.run(id); r != nil {
		r.mu.Lock()
		if !r.done {
			r.chat.Queue = append(r.chat.Queue, assistant.QueuedMessage{ID: assistant.NewID(), Text: text, Author: assistant.AuthorClaude})
			s.publish(r, -1)
			r.mu.Unlock()
			return "The conversation is running: your message is queued and sent at its next step.", nil
		}
		r.mu.Unlock()
	}
	chat, err := s.openChat(chatCtx{loc, root}, id)
	if err != nil {
		return "", err
	}
	if chat.Server == "" || chat.Model == "" {
		return "", fmt.Errorf("this conversation has no model yet: the user must send its first message")
	}
	from := minFrom(len(chat.Messages), skipQuestions(chat))
	chat.Messages = append(chat.Messages, &assistant.Message{Role: "user", Content: assistant.String(text), Display: text, Author: assistant.AuthorClaude})
	if !s.startRun(loc, root, project, lang, chat, from) {
		return "", fmt.Errorf("the conversation started meanwhile: send your message again")
	}
	return "Message sent: the assistant answers now. Read its answer later with kanban_conversation.", nil
}

// Answer answers the questions of ask_user waiting in a conversation (kanban_answer of the
// MCP), then starts it.
func (s *A) Answer(loc llm.ChatLocation, root, project, lang, id string, answers [][]string, notes []string) (string, error) {
	if s.run(id) != nil {
		return "", fmt.Errorf("the conversation is running: no question waits for an answer")
	}
	chat, err := s.openChat(chatCtx{loc, root}, id)
	if err != nil {
		return "", err
	}
	index := -1
	for i, m := range chat.Messages {
		if m.AskState == "pending" {
			index = i
		}
	}
	if index < 0 {
		return "", fmt.Errorf("no question of this conversation waits for an answer")
	}
	m := chat.Messages[index]
	if len(answers) != len(m.Questions) {
		return "", fmt.Errorf("give one answer per question: %d questions, %d answers", len(m.Questions), len(answers))
	}
	// The questions of a graph are all answered: the path is the questions given an answer.
	var path []int
	if hasGraph(m.Questions) {
		for i, a := range answers {
			if len(a) > 0 {
				path = append(path, i)
			}
		}
	}
	m.Answers, m.Notes, m.Path, m.OffPath, m.AskState, m.Author = answers, notes, path, nil, "answered", assistant.AuthorClaude
	m.Content = assistant.String("Claude Code answered for the user.\n" + assistant.AnswersText(m.Questions, answers, notes, path, nil))
	m.Summary = assistant.T("answers received", nil).Raw()
	if !s.startRun(loc, root, project, lang, chat, index) {
		return "", fmt.Errorf("the conversation started meanwhile: read it again")
	}
	return "Answers sent: the assistant goes on.", nil
}

func hasGraph(qs []assistant.Question) bool {
	for _, q := range qs {
		if q.ID != "" {
			return true
		}
	}
	return false
}
