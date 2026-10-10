package server

import (
	"encoding/json"

	"github.com/DrSmithFr/web-ide/pod/internal/agent"
)

// Reasoning effort of an answer. Fixed by the user, or dynamic: the answer to a message of the
// user thinks the most (xhigh); one after tool results thinks less (medium), unless a call
// failed or was refused (xhigh again), or every call was a mechanical one that succeeded (low:
// the next step was decided before them). With set_effort turned on, the level the model
// chose since the last message of the user wins.

var efforts = map[string]bool{"xhigh": true, "medium": true, "low": true}

// mechanicalTools: steps decided before the call, whose success leaves little to think about.
var mechanicalTools = map[string]bool{
	"edit_file": true, "write_file": true, "open_file": true, "focus": true,
	"kanban_add_note": true, "kanban_update": true, "kanban_move": true, "agent_note": true,
}

// dynamicEffort: the user lets the agent choose the effort at each step.
func dynamicEffort(o agent.Options) bool {
	return !efforts[o.Effort]
}

// effortFor is the reasoning effort of the next answer of c.
func effortFor(c *agent.Chat, o agent.Options) string {
	// A session of a ticket keeps the effort of its complexity.
	if c.Ticket != nil && c.Ticket.Effort != "" {
		o.Effort = c.Ticket.Effort
	}
	if !dynamicEffort(o) {
		return o.Effort
	}
	if o.EffortTool {
		for i := len(c.Messages) - 1; i >= 0; i-- {
			m := c.Messages[i]
			if isUserTurn(m) {
				break
			}
			if m.Role == "tool" && m.Name == "set_effort" && m.Status == "ok" && efforts[m.Effort] {
				return m.Effort
			}
		}
	}
	steps, mechanical := 0, true
	for i := len(c.Messages) - 1; i >= 0; i-- {
		m := c.Messages[i]
		if m.Role == "assistant" || isUserTurn(m) {
			if isUserTurn(m) || steps == 0 {
				return "xhigh"
			}
			break
		}
		if m.Role != "tool" || m.Name == "set_effort" {
			continue
		}
		steps++
		if m.Status == "error" || m.Status == "denied" {
			return "xhigh"
		}
		mechanical = mechanical && mechanicalTools[m.Name]
	}
	if steps > 0 && mechanical {
		return "low"
	}
	return "medium"
}

// isUserTurn: a message the model has not seen answered yet comes from outside (the user, a
// parent or a child agent); the summary of a compaction is not one.
func isUserTurn(m *agent.Message) bool {
	return m.Role == "user" && m.Kind != "summary"
}

// setEffort runs set_effort: the level is kept on the tool message, read by effortFor.
func setEffort(args map[string]json.RawMessage) (toolResult, string) {
	var level string
	_ = json.Unmarshal(args["level"], &level)
	if !efforts[level] {
		return toolResult{Content: "Error: level must be xhigh, medium or low.", Summary: agent.T("invalid effort", nil).Raw(), Status: "error", Failure: "usage"}, ""
	}
	return toolResult{Content: "Effort set to " + level + " until the user writes again.", Summary: agent.Plain(level), Status: "ok"}, level
}
