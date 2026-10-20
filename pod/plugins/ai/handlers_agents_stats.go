package ai

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"github.com/DrSmithFr/web-ide/pod/acp"
)

// agents.stats: the work time and the cost of the agents, for the project of the window (its
// worktrees included) or for a ticket (the sessions of its worktree), since a time (ms).
func (s *A) registerAgentStats() {
	acp.SetPriceFile(s.Store.Path("prices.json"))
	s.handle("agents.stats", func(ctx context.Context, c *Client, p json.RawMessage) (any, error) {
		a, err := bind[struct {
			Ticket int64 `json:"ticket"`
			Since  int64 `json:"since"`
		}](p)
		if err != nil {
			return nil, err
		}
		rt, err := c.runtime()
		if err != nil {
			return nil, err
		}
		main := rt.P.ID
		if rt.P.Parent != "" {
			main = rt.P.Parent
		}
		match := func(project string) bool { return project == main || strings.HasPrefix(project, main+"-") }
		if a.Ticket > 0 {
			ticket := main + "-t" + strconv.FormatInt(a.Ticket, 10)
			match = func(project string) bool { return project == ticket }
		}
		return acp.Sum(s.Store.Path("agents"), match, time.UnixMilli(a.Since)), nil
	})
}
