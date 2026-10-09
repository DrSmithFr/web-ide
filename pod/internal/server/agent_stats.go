package server

import (
	"context"
	"encoding/json"
	"sort"

	"github.com/DrSmithFr/web-ide/pod/internal/agent"
	"github.com/DrSmithFr/web-ide/pod/internal/stats"
)

type childStats struct {
	ID    string      `json:"id"`
	Title string      `json:"title"`
	Stats stats.Stats `json:"stats"`
}

// agentStats: the statistics of a conversation with its sub-agents (the total and each of
// them), or of all the conversations of the project with a filter and the values to filter on.
func (s *Server) agentStats(ctx context.Context, c *Client, cc chatCtx, p json.RawMessage) (any, error) {
	a, err := bind[struct {
		ID      string `json:"id"`
		Project bool   `json:"project"`
		stats.Filter
	}](p)
	if err != nil {
		return nil, err
	}
	if a.Project {
		list, err := s.LLM.ListChats(cc.loc)
		if err != nil {
			return nil, err
		}
		acc := stats.New(a.Filter)
		models, efforts := map[string]bool{}, map[string]bool{}
		for _, info := range list {
			chat, err := s.readChat(cc, info.ID)
			if err != nil {
				continue
			}
			for _, m := range chat.Messages {
				if m.Role == "assistant" {
					models[m.Model] = models[m.Model] || m.Model != ""
					efforts[m.Effort] = efforts[m.Effort] || m.Effort != ""
				}
			}
			acc.Add(chat)
		}
		return map[string]any{"stats": acc.Result(nil), "models": keys(models), "efforts": keys(efforts)}, nil
	}
	chat, err := s.readChat(cc, a.ID)
	if err != nil {
		return nil, err
	}
	total := stats.New(a.Filter)
	total.Add(chat)
	var children []childStats
	seen := map[string]bool{chat.ID: true}
	var walk func(c *agent.Chat)
	walk = func(c *agent.Chat) {
		for _, id := range c.Children {
			if seen[id] {
				continue
			}
			seen[id] = true
			child, err := s.readChat(cc, id)
			if err != nil {
				continue
			}
			own := stats.New(a.Filter)
			own.Add(child)
			total.Add(child)
			children = append(children, childStats{ID: child.ID, Title: child.Title, Stats: own.Result(child)})
			walk(child)
		}
	}
	walk(chat)
	own := stats.New(a.Filter)
	own.Add(chat)
	return map[string]any{"stats": total.Result(chat), "own": own.Result(chat), "children": children}, nil
}

// readChat: a conversation as it is now (the running one from memory), without changing it.
func (s *Server) readChat(cc chatCtx, id string) (*agent.Chat, error) {
	if r := s.run(id); r != nil {
		r.mu.Lock()
		defer r.mu.Unlock()
		data, _ := json.Marshal(r.chat)
		var copy agent.Chat
		_ = json.Unmarshal(data, &copy)
		return &copy, nil
	}
	return s.loadChat(cc.loc, id)
}

func keys(m map[string]bool) []string {
	out := []string{}
	for k, v := range m {
		if v {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}
