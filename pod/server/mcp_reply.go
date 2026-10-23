package server

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/DrSmithFr/web-ide/plugins/ai"
	"github.com/DrSmithFr/web-ide/pod/llm"
	"github.com/DrSmithFr/web-ide/pod/sshx"

	"github.com/DrSmithFr/web-ide/lib/i18n"
	"github.com/DrSmithFr/web-ide/plugins/kanban/kanban"
	"github.com/DrSmithFr/web-ide/pod/assistant"
)

// Claude Code takes part in a conversation of the assistant linked to a ticket: it
// writes a message (kanban_reply) or answers the questions waiting for the user
// (kanban_answer). Its messages carry the author "claude" and the conversation goes on as
// if the user had sent them.

// linkedChat is a conversation linked to a ticket, with where it runs.
type linkedChat struct {
	loc     llm.ChatLocation
	root    string
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
	return linkedChat{loc: loc, root: root, project: s.ticketChatProject(sc, t, chatID)}, nil
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
	if i18n.Supported(set.Language) {
		return set.Language
	}
	return "en"
}

// pendingQuestions lists the questions waiting in a conversation, for kanban_conversation.
func pendingQuestions(c *assistant.Chat) string {
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

// conversations is the AI plugin when it is on in the project of a linked conversation: it
// holds the conversations of the assistant.
func (s *Server) conversations(project string) (*ai.A, error) {
	rt, err := s.openRuntime(project, sshx.Creds{})
	if err != nil {
		return nil, err
	}
	p, _ := s.Plugins.Get("ai")
	a, _ := p.(*ai.A)
	if a == nil || !s.pluginOn(rt, "ai") {
		return nil, i18n.Errorf("the %s plugin is disabled on this project", "AI")
	}
	return a, nil
}

// mcpReply sends a message of Claude Code into a linked conversation (ai.Reply).
func (s *Server) mcpReply(lc linkedChat, id, text string) (string, error) {
	a, err := s.conversations(lc.project)
	if err != nil {
		return "", err
	}
	return a.Reply(lc.loc, lc.root, lc.project, s.uiLanguage(), id, text)
}

// mcpAnswer answers the questions of ask_user waiting in a linked conversation (ai.Answer).
func (s *Server) mcpAnswer(lc linkedChat, id string, answers [][]string, notes []string) (string, error) {
	a, err := s.conversations(lc.project)
	if err != nil {
		return "", err
	}
	return a.Answer(lc.loc, lc.root, lc.project, s.uiLanguage(), id, answers, notes)
}
