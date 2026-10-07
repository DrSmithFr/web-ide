package server

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/DrSmithFr/web-ide/pod/internal/agent"
	"github.com/DrSmithFr/web-ide/pod/internal/kanban"
)

// Claude Code takes part in a conversation of the local assistant linked to a ticket: it
// writes a message (kanban_reply) or answers the questions waiting for the user
// (kanban_answer). Its messages carry the author "claude" and the conversation goes on as
// if the user had sent them.

// AuthorClaude marks the messages and answers written by Claude Code.
const AuthorClaude = "claude"

// linkedChat is a conversation linked to a ticket, with where it runs.
type linkedChat struct {
	cc      chatCtx
	project string
}

func (s *Server) linkedChat(sc mcpScope, id int64, chatID string) (linkedChat, error) {
	t, err := s.Kanban.Get(sc.loc, id)
	if err != nil {
		return linkedChat{}, err
	}
	linked := false
	for _, c := range t.ChatList {
		linked = linked || c.ChatID == chatID
	}
	if !linked {
		return linkedChat{}, fmt.Errorf("conversation %s is not linked to ticket #%d", chatID, id)
	}
	loc, root, err := s.chatLoc(sc.root.ID)
	if err != nil {
		return linkedChat{}, err
	}
	return linkedChat{cc: chatCtx{loc, root}, project: s.ticketChatProject(sc, t, chatID)}, nil
}

// ticketChatProject is where the tools of a linked conversation work: the worktree of the
// ticket (of the root of its lineage) for a development or a correction, else the project.
func (s *Server) ticketChatProject(sc mcpScope, t *kanban.Ticket, chatID string) string {
	role := ""
	for _, c := range t.ChatList {
		if c.ChatID == chatID {
			role = c.Role
		}
	}
	if role == "briefing" || role == "plan" {
		return sc.root.ID
	}
	wt := t.Worktree
	if wt == "" && t.Parent != 0 {
		if p, err := s.Kanban.Get(sc.loc, t.Parent); err == nil {
			wt = p.Worktree
		}
	}
	if wt != "" {
		if child, _ := s.Projects.ChildAt(sc.root.ID, wt); child != "" {
			return child
		}
	}
	return sc.root.ID
}

// uiLanguage is the language of the interface, for the errors shown in the conversation.
func (s *Server) uiLanguage() string {
	var set struct{ Language string }
	_ = json.Unmarshal(s.Settings.Current().Settings, &set)
	if set.Language == "fr" {
		return "fr"
	}
	return "en"
}

// mcpReply sends a message of Claude Code into a linked conversation: queued while it runs,
// else added (the questions waiting for the user are left aside) and the conversation started.
func (s *Server) mcpReply(lc linkedChat, id, text string) (string, error) {
	if strings.TrimSpace(text) == "" {
		return "", fmt.Errorf("message is empty")
	}
	if r := s.run(id); r != nil {
		r.mu.Lock()
		if !r.done {
			r.chat.Queue = append(r.chat.Queue, agent.QueuedMessage{ID: newID(), Text: text, Author: AuthorClaude})
			s.publish(r, -1)
			r.mu.Unlock()
			return "The conversation is running: your message is queued and sent at its next step.", nil
		}
		r.mu.Unlock()
	}
	chat, err := s.openChat(lc.cc, id)
	if err != nil {
		return "", err
	}
	if chat.Server == "" || chat.Model == "" {
		return "", fmt.Errorf("this conversation has no model yet: the user must send its first message")
	}
	from := minFrom(len(chat.Messages), skipQuestions(chat))
	chat.Messages = append(chat.Messages, &agent.Message{Role: "user", Content: agent.String(text), Display: text, Author: AuthorClaude})
	if !s.startRun(lc.cc.loc, lc.cc.root, lc.project, s.uiLanguage(), chat, from) {
		return "", fmt.Errorf("the conversation started meanwhile: send your message again")
	}
	return "Message sent: the assistant answers now. Read its answer later with kanban_conversation.", nil
}

// mcpAnswer answers the questions of ask_user waiting in a linked conversation, then starts it.
func (s *Server) mcpAnswer(lc linkedChat, id string, answers [][]string, notes []string) (string, error) {
	if s.run(id) != nil {
		return "", fmt.Errorf("the conversation is running: no question waits for an answer")
	}
	chat, err := s.openChat(lc.cc, id)
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
	m.Answers, m.Notes, m.Path, m.OffPath, m.AskState, m.Author = answers, notes, path, nil, "answered", AuthorClaude
	m.Content = agent.String("Claude Code answered for the user.\n" + agent.AnswersText(m.Questions, answers, notes, path, nil))
	m.Summary = agent.T("answers received", nil).Raw()
	if !s.startRun(lc.cc.loc, lc.cc.root, lc.project, s.uiLanguage(), chat, index) {
		return "", fmt.Errorf("the conversation started meanwhile: read it again")
	}
	return "Answers sent: the assistant goes on.", nil
}

func hasGraph(qs []agent.Question) bool {
	for _, q := range qs {
		if q.ID != "" {
			return true
		}
	}
	return false
}

// pendingQuestions lists the questions waiting in a conversation, for kanban_conversation.
func pendingQuestions(c *agent.Chat) string {
	for i := len(c.Messages) - 1; i >= 0; i-- {
		m := c.Messages[i]
		if m.AskState != "pending" {
			continue
		}
		var b strings.Builder
		b.WriteString("\n## Waiting for the answers of the user\nAnswer with kanban_answer, one list of strings per question, in this order (an option label, several for a multiple choice, or a free text):\n")
		for k, q := range m.Questions {
			fmt.Fprintf(&b, "%d. %s", k+1, q.Question)
			var labels []string
			for _, o := range q.Options {
				labels = append(labels, o.Label)
			}
			if len(labels) > 0 {
				fmt.Fprintf(&b, " [options: %s]", strings.Join(labels, " | "))
			}
			if q.Multiple {
				b.WriteString(" (multiple)")
			}
			b.WriteString("\n")
		}
		return b.String()
	}
	return ""
}
