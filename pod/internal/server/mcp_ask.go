package server

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/DrSmithFr/web-ide/pod/internal/assistant"
	"github.com/DrSmithFr/web-ide/pod/internal/i18n"
)

// ask_user of the MCP endpoint: a CLI agent asks the user questions of the assistant's types;
// they wait in the Questions tool of the windows of the project, and the call returns the
// answers. The call does not wait forever (an MCP client gives up on a tool after a while):
// past mcpAskWait it answers that the question is still open, and the agent calls again
// with wait to go on waiting.

// mcpAskWait is how long one call waits for the user.
var mcpAskWait = 4 * time.Minute

type mcpAnswer struct {
	answers [][]string
	notes   []string
	path    []int
	offPath *int
	skip    bool
}

type mcpAsk struct {
	q    Question
	root string
	// done holds the answers until a call takes them (answered: no longer listed).
	done     chan mcpAnswer
	answered bool
}

var mcpAskTool = mcpTool{
	name:        "ask_user",
	description: fmt.Sprintf("Asks the user one or more questions (1 to %d) in the IDE, where they answer in its Questions tool, when information is missing or a choice is theirs. Each question has a type that picks its look: choice (the default), idea, compare, rank or scenario. The user can also answer freely, say \"I don't know\" or \"Up to you\", and add a note. The call returns the answers; when the user takes long, it returns that the questions are still open with their id: call again with wait set to that id (and no questions) to go on waiting. Write the questions in the user's language.", assistant.MaxQuestions),
	props: map[string]any{
		"questions": assistant.AskUserQuestions(),
		"context":   str("What the user should know before answering (your finding, what you are about to do), shown above the questions"),
		"wait":      str("Id of questions asked before and still open: wait for their answers instead of asking"),
	},
	run: func(ctx context.Context, s *Server, raw json.RawMessage) (string, error) {
		var a struct {
			Cwd       string          `json:"cwd"`
			Questions json.RawMessage `json:"questions"`
			Context   string          `json:"context"`
			Wait      string          `json:"wait"`
		}
		if err := json.Unmarshal(raw, &a); err != nil {
			return "", err
		}
		sc, err := s.mcpScope(a.Cwd)
		if err != nil {
			return "", err
		}
		var ask *mcpAsk
		if a.Wait != "" {
			s.asks.mu.Lock()
			ask = s.asks.items[a.Wait]
			s.asks.mu.Unlock()
			if ask == nil || ask.root != sc.root.ID {
				return "", fmt.Errorf("no open questions %q: they were answered or the IDE restarted; ask again", a.Wait)
			}
		} else {
			qs, err := assistant.NormalizeQuestions(a.Questions)
			if err != nil {
				return "", err
			}
			ask = s.newMCPAsk(sc, qs, a.Context)
		}
		select {
		case ans := <-ask.done:
			s.asks.mu.Lock()
			delete(s.asks.items, ask.q.ID)
			s.asks.mu.Unlock()
			if ans.skip {
				return "The user did not answer these questions: go on with your best judgment, or ask in another way.", nil
			}
			return assistant.AnswersText(ask.q.Questions, ans.answers, ans.notes, ans.path, ans.offPath), nil
		case <-time.After(mcpAskWait):
			return fmt.Sprintf("The user has not answered yet; the questions stay open in the IDE. Call ask_user again with wait %q to go on waiting.", ask.q.ID), nil
		case <-ctx.Done():
			return "", ctx.Err()
		}
	},
}

func init() { mcpTools = append(mcpTools, mcpAskTool) }

// newMCPAsk puts questions of an MCP client in the Questions tool of its project.
func (s *Server) newMCPAsk(sc mcpScope, qs []assistant.Question, context string) *mcpAsk {
	s.asks.mu.Lock()
	s.asks.next++
	id := "mcp:" + strconv.Itoa(s.asks.next)
	title := sc.here.Name()
	if sc.ticket != 0 {
		title = fmt.Sprintf("#%d · %s", sc.ticket, title)
	}
	a := &mcpAsk{root: sc.root.ID, done: make(chan mcpAnswer, 1),
		q: Question{ID: id, Source: "mcp", Project: sc.here.ID, Name: "Claude Code", Title: title, Kind: "ask", Questions: qs, Last: context, Time: time.Now().UnixMilli()}}
	s.asks.items[id] = a
	s.asks.mu.Unlock()
	s.questionsChanged(sc.root.ID, title)
	return a
}

// answerMCPAsk gives the answers of the user to the questions id of root.
func (s *Server) answerMCPAsk(id, root string, ans mcpAnswer) error {
	s.asks.mu.Lock()
	a := s.asks.items[id]
	if a == nil || a.root != root || a.answered {
		s.asks.mu.Unlock()
		return i18n.New("this question is no longer open")
	}
	for _, i := range append(append([]int{}, ans.path...), derefOr(ans.offPath, 0)) {
		if i < 0 || i >= len(a.q.Questions) {
			s.asks.mu.Unlock()
			return i18n.New("these questions are not waiting for an answer")
		}
	}
	a.answered = true
	s.asks.mu.Unlock()
	a.done <- ans
	s.questionsChanged(root, "")
	return nil
}

func derefOr(p *int, v int) int {
	if p == nil {
		return v
	}
	return *p
}
