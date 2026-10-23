package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	kbp "github.com/DrSmithFr/web-ide/plugins/kanban"
	"github.com/DrSmithFr/web-ide/plugins/kanban/kanban"
	"github.com/DrSmithFr/web-ide/pod/acp"
	"github.com/DrSmithFr/web-ide/pod/assistant"
	"github.com/DrSmithFr/web-ide/pod/llm"
	"github.com/DrSmithFr/web-ide/pod/plugins"
)

// Tools of the MCP endpoint on the conversations linked to a ticket (Host.MCPTool): Claude
// Code reads one (kanban_conversation), or takes part in it: it writes a message
// (kanban_reply) or answers the questions waiting for the user (kanban_answer). Its messages
// carry the author "claude" and the conversation goes on as if the user had sent them. They
// need the tickets of the Kanban, whose scope and ticket binding they share.

func (s *A) registerMCP() {
	for _, t := range s.mcpTools() {
		s.host.MCPTool(t)
	}
}

func (s *A) mcpTools() []plugins.MCPTool {
	return []plugins.MCPTool{
		{
			Name:        "kanban_conversation",
			Needs:       "tickets",
			Description: "Reads a conversation of the assistant linked to the ticket (see Linked conversations in kanban_get), or the session of the agent developing it (Agent session in kanban_get): the messages of the user and of the assistant, with the questions asked and their answers. Use it to take over a briefing; with last and tools, to review a development (its commands, errors and notes).",
			Props: map[string]any{"id": kbp.TicketID, "chat": plugins.Str("Conversation id (chat … in kanban_get)"),
				"last":  map[string]any{"type": "integer", "description": "Only the last messages, this many (default: all)"},
				"tools": map[string]any{"type": "boolean", "description": "Also the tool calls (name and arguments, cut), their errors, the answers that failed and the notes and reports of sub-agents"}},
			Required: []string{"chat"},
			Run: kbp.TicketTool(s.kb(), func(ctx context.Context, sc kbp.Scope, id int64, a struct {
				Chat  string
				Last  int
				Tools bool
			}) (string, error) {
				t, err := s.kb().Kanban.Get(sc.Loc, id)
				if err != nil {
					return "", err
				}
				linked := false
				for _, c := range t.ChatList {
					linked = linked || c.ChatID == a.Chat
				}
				if _, info, entries, ok := s.kb().TicketSession(sc.Root, t); !linked && ok && info.ID == a.Chat {
					return acpThreadText(info, entries, a.Last, a.Tools), nil
				}
				if !linked {
					return "", fmt.Errorf("conversation %s is not linked to ticket #%d", a.Chat, id)
				}
				raw, err := s.LLM.GetChat(llm.ChatLocation{Project: sc.Loc.Project, IdeDir: sc.Loc.IdeDir}, a.Chat)
				if err != nil {
					return "", err
				}
				text := conversationText(raw, a.Last, a.Tools)
				if s.Running(a.Chat) {
					return text + "\n\n(The assistant is answering now: read the conversation again later.)", nil
				}
				var chat assistant.Chat
				_ = json.Unmarshal(raw, &chat)
				return text + pendingQuestions(&chat), nil
			}),
		},
		{
			Name:        "kanban_reply",
			Needs:       "tickets",
			Description: "Writes a message in a conversation of the assistant linked to the ticket, as Claude Code: the assistant reads it and answers (its questions still waiting are left aside; use kanban_answer to answer them). Queued if the conversation is running. Use it to answer a question the assistant asked in its text, or to guide it.",
			Props:       map[string]any{"id": kbp.TicketID, "chat": plugins.Str("Conversation id (chat … in kanban_get)"), "message": plugins.Str("Your message, in Markdown")},
			Required:    []string{"chat", "message"},
			Run: kbp.TicketTool(s.kb(), func(ctx context.Context, sc kbp.Scope, id int64, a struct{ Chat, Message string }) (string, error) {
				lc, err := s.linkedChat(sc, id, a.Chat)
				if err != nil {
					return "", err
				}
				return s.reply(lc.loc, lc.root, lc.project, sc.Lang, a.Chat, a.Message)
			}),
		},
		{
			Name:        "kanban_answer",
			Needs:       "tickets",
			Description: "Answers the questions of ask_user waiting in a conversation of the assistant linked to the ticket, for the user (kanban_conversation lists them with their options); the assistant then goes on. Answer only what the plan, the ticket or the user already decided; otherwise leave the questions to the user.",
			Props: map[string]any{
				"id":   kbp.TicketID,
				"chat": plugins.Str("Conversation id (chat … in kanban_get)"),
				"answers": map[string]any{"type": "array", "description": "One list per question, in order: an option label, several for a multiple choice, the order for a rank, or a free text",
					"items": map[string]any{"type": "array", "items": map[string]any{"type": "string"}}},
				"notes": plugins.StrList("A note per question (optional, same order)"),
			},
			Required: []string{"chat", "answers"},
			Run: kbp.TicketTool(s.kb(), func(ctx context.Context, sc kbp.Scope, id int64, a struct {
				Chat    string
				Answers [][]string
				Notes   []string
			}) (string, error) {
				lc, err := s.linkedChat(sc, id, a.Chat)
				if err != nil {
					return "", err
				}
				return s.answer(lc.loc, lc.root, lc.project, sc.Lang, a.Chat, a.Answers, a.Notes)
			}),
		},
	}
}

// linkedChat is a conversation linked to a ticket, with where it runs.
type linkedChat struct {
	loc     llm.ChatLocation
	root    string
	project string
}

func (s *A) linkedChat(sc kbp.Scope, id int64, chatID string) (linkedChat, error) {
	t, err := s.kb().Kanban.Get(sc.Loc, id)
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
	loc, root, err := s.ChatLoc(sc.Root.ID)
	if err != nil {
		return linkedChat{}, err
	}
	return linkedChat{loc: loc, root: root, project: s.ticketChatProject(sc, t, chatID)}, nil
}

// ticketChatProject is where the tools of a linked conversation work: the worktree of the
// ticket (of the root of its lineage) for a development or a correction, else the project.
func (s *A) ticketChatProject(sc kbp.Scope, t *kanban.Ticket, chatID string) string {
	role := ""
	for _, c := range t.ChatList {
		if c.ChatID == chatID {
			role = c.Role
		}
	}
	if role == "briefing" || role == "plan" {
		return sc.Root.ID
	}
	wt := t.Worktree
	if wt == "" && t.Parent != 0 {
		if p, err := s.kb().Kanban.Get(sc.Loc, t.Parent); err == nil {
			wt = p.Worktree
		}
	}
	if wt != "" {
		if child, _ := s.Projects.ChildAt(sc.Root.ID, wt); child != "" {
			return child
		}
	}
	return sc.Root.ID
}

// reply sends a message of Claude Code (kanban_reply of the MCP) into a conversation: queued while it runs,
// else added (the questions waiting for the user are left aside) and the conversation started.
func (s *A) reply(loc llm.ChatLocation, root, project, lang, id, text string) (string, error) {
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

// answer answers the questions of ask_user waiting in a conversation (kanban_answer of the
// MCP), then starts it.
func (s *A) answer(loc llm.ChatLocation, root, project, lang, id string, answers [][]string, notes []string) (string, error) {
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

// conversationText is a conversation of the assistant as Claude reads it: what the user
// and the assistant wrote, and the questions of ask_user with their answers.
// conversationText is what Claude Code reads of a conversation: the last messages only when
// last > 0, and the tool calls, their errors and the failed answers with tools.
func conversationText(raw json.RawMessage, last int, tools bool) string {
	var chat struct {
		Title    string `json:"title"`
		Messages []struct {
			Role      string          `json:"role"`
			Content   json.RawMessage `json:"content"`
			Display   string          `json:"display"`
			Kind      string          `json:"kind"`
			Author    string          `json:"author"`
			ToolCalls []struct {
				ID       string `json:"id"`
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
			ToolCallID string `json:"tool_call_id"`
			AskState   string `json:"askState"`
			Name       string `json:"name"`
			Status     string `json:"status"`
			Error      string `json:"error"`
		} `json:"messages"`
	}
	_ = json.Unmarshal(raw, &chat)
	text := func(c json.RawMessage) string {
		var s string
		if json.Unmarshal(c, &s) == nil {
			return s
		}
		var parts []struct{ Type, Text string }
		_ = json.Unmarshal(c, &parts)
		var out []string
		for _, p := range parts {
			if p.Type == "text" {
				out = append(out, p.Text)
			}
		}
		return strings.Join(out, "\n")
	}
	var b strings.Builder
	fmt.Fprintf(&b, "# Conversation: %s\n", chat.Title)
	asks := map[string]bool{}
	msgs := chat.Messages
	if last > 0 && last < len(msgs) {
		fmt.Fprintf(&b, "\n(%d earlier messages left out)\n", len(msgs)-last)
		msgs = msgs[len(msgs)-last:]
	}
	for _, m := range msgs {
		switch m.Role {
		case "user":
			t := m.Display
			if t == "" {
				t = text(m.Content)
			}
			label := "User"
			if m.Author == assistant.AuthorClaude {
				label = "Claude Code (you)"
			}
			if m.Kind == "summary" {
				label = "Summary of the earlier messages"
			}
			fmt.Fprintf(&b, "\n## %s\n%s\n", label, strings.TrimSpace(t))
		case "assistant":
			if t := strings.TrimSpace(text(m.Content)); t != "" {
				fmt.Fprintf(&b, "\n## Assistant\n%s\n", t)
			}
			for _, c := range m.ToolCalls {
				if c.Function.Name == "ask_user" {
					asks[c.ID] = true
					fmt.Fprintf(&b, "\n## Assistant asks (ask_user)\n%s\n", c.Function.Arguments)
				} else if tools {
					fmt.Fprintf(&b, "- call %s %s\n", c.Function.Name, assistant.Cut(c.Function.Arguments, 300))
				}
			}
			if tools && m.Error != "" {
				fmt.Fprintf(&b, "\n## The answer failed\n%s\n", m.Error)
			}
		case "tool":
			switch {
			case asks[m.ToolCallID] && m.AskState != "pending":
				fmt.Fprintf(&b, "\n## Answers of the user\n%s\n", strings.TrimSpace(text(m.Content)))
			case tools && (m.Name == "agent_note" || m.Name == "agent_report"):
				// The note itself is in the call; its result says nothing more.
			case tools && (m.Status == "error" || m.Status == "denied" || strings.HasPrefix(text(m.Content), "Interrupted")):
				fmt.Fprintf(&b, "  → %s: %s\n", m.Status, assistant.Cut(strings.TrimSpace(text(m.Content)), 300))
			}
		}
	}
	return strings.TrimSpace(b.String())
}

// acpThreadText is the thread of a session of the Agents tool as kanban_conversation gives
// it: the prompts and answers, the questions; with tools, the tool calls and the notices.
func acpThreadText(info acp.Info, entries []acp.Entry, last int, tools bool) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Agent session: %s (%s, %s)\n", info.Title, info.Name, info.State)
	if last > 0 && last < len(entries) {
		fmt.Fprintf(&b, "\n(%d earlier entries left out)\n", len(entries)-last)
		entries = entries[len(entries)-last:]
	}
	for _, e := range entries {
		switch e.Kind {
		case "user":
			fmt.Fprintf(&b, "\n## User\n%s\n", strings.TrimSpace(e.Text))
		case "agent":
			fmt.Fprintf(&b, "\n## %s\n%s\n", info.Name, strings.TrimSpace(e.Text))
		case "permission":
			outcome := e.Outcome
			if outcome == "" {
				outcome = "waiting for the user"
			}
			fmt.Fprintf(&b, "\n## %s asks permission: %s (%s)\n", info.Name, e.Title, outcome)
		case "plan":
			if tools {
				b.WriteString("\n## Plan\n")
				for _, p := range e.Plan {
					fmt.Fprintf(&b, "- [%s] %s\n", p.Status, p.Content)
				}
			}
		case "tool":
			if tools {
				fmt.Fprintf(&b, "- %s %s (%s)\n", e.ToolKind, assistant.Cut(e.Title, 300), e.Status)
			}
		case "notice", "error":
			if tools {
				fmt.Fprintf(&b, "\n(%s)\n", e.Text)
			}
		}
	}
	return b.String()
}
