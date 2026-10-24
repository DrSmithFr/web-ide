package kanban

import (
	kb "antennae.space/ide/plugins/kanban/kanban"
	"antennae.space/ide/pod/caps"
	"antennae.space/ide/pod/runtime"
)

// Time of work on a ticket: the time of the answers of its linked conversations and of
// their sub-agents (the assistant of the IDE only: Claude Code runs in a terminal).

type chatTimes struct {
	times    map[string]caps.ChatTime
	children map[string][]string
}

// chatTimes of the conversations of a project (nil when they cannot be read, or the AI plugin is
// off on the project of rt).
func (s *K) chatTimes(rt *runtime.Runtime, projectID string) *chatTimes {
	ag := s.agents(rt)
	if ag == nil {
		return nil
	}
	times, err := ag.ChatTimes(projectID)
	if err != nil {
		return nil
	}
	ct := &chatTimes{times: times, children: map[string][]string{}}
	for id, t := range times {
		if t.Parent != "" {
			ct.children[t.Parent] = append(ct.children[t.Parent], id)
		}
	}
	return ct
}

// of: the time of conversations with their sub-agents, each counted once.
func (ct *chatTimes) of(ids ...string) int64 {
	seen := map[string]bool{}
	var total int64
	var walk func(id string)
	walk = func(id string) {
		if seen[id] {
			return
		}
		seen[id] = true
		total += ct.times[id].Ms
		for _, c := range ct.children[id] {
			walk(c)
		}
	}
	for _, id := range ids {
		walk(id)
	}
	return total
}

// withTimes fills the times of a ticket answered by a handler.
func (s *K) withTimes(rt *runtime.Runtime, projectID string) func(any, error) (any, error) {
	return func(res any, err error) (any, error) {
		if t, ok := res.(*kb.Ticket); ok && err == nil && t != nil {
			if ct := s.chatTimes(rt, projectID); ct != nil {
				ids := make([]string, len(t.ChatList))
				for i := range t.ChatList {
					c := &t.ChatList[i]
					ids[i] = c.ChatID
					c.GenerationMs, c.OwnMs, c.ThinkMs = ct.of(c.ChatID), ct.times[c.ChatID].Ms, ct.times[c.ChatID].ThinkMs
				}
				t.GenerationMs = ct.of(ids...)
			}
		}
		return res, err
	}
}
