package server

import (
	"encoding/json"
	"log"
	"os"
	"time"

	"github.com/DrSmithFr/web-ide/pod/internal/agent"
	"github.com/DrSmithFr/web-ide/pod/internal/llm"
)

// With a keeper, the conversations running when the pod stops are taken back when it starts:
// the completion awaited goes on from the relay of the keeper, the command of a tool call
// running in the keeper is followed again, the other tool calls without result run again (an
// approval is asked again). running.json tells which conversations ran: they are spread over
// the bases of the projects.

type runningEntry struct {
	ID      string           `json:"id"`
	Loc     llm.ChatLocation `json:"loc"`
	Root    string           `json:"root"`
	Project string           `json:"project"`
	Lang    string           `json:"lang,omitempty"`
}

func (s *Server) runningPath() string { return s.Store.Path("running.json") }

// saveRunning writes the conversations of s.agents (called when one starts or ends).
func (s *Server) saveRunning() {
	s.agents.mu.Lock()
	list := make([]runningEntry, 0, len(s.agents.runs))
	for _, r := range s.agents.runs {
		list = append(list, runningEntry{ID: r.id, Loc: r.loc, Root: r.root, Project: r.project, Lang: r.lang})
	}
	s.agents.mu.Unlock()
	data, _ := json.Marshal(list)
	tmp := s.runningPath() + ".tmp"
	if os.WriteFile(tmp, data, 0o600) == nil {
		_ = os.Rename(tmp, s.runningPath())
	}
}

// stopping: the pod stops; the completions it follows are left to the relay.
func (s *Server) stopping() bool { return s.stopped.Load() }

// keptFor runs the command of a tool call of r in the keeper (when its project has one), and
// records it on the conversation for a resume.
func (s *Server) keptFor(r *agentRun) *kept {
	return &kept{owner: r.project, started: func(id string, until time.Time) {
		s.toolRunning(r, func(run *agent.Running) { run.Proc, run.Until = id, until.UnixMilli() })
	}, ended: s.forgetProc}
}

// forgetProc drops a command of the keeper whose result is known (not when the pod stops:
// the next pod follows it).
func (s *Server) forgetProc(id string) {
	if s.Keeper != nil && !s.stopping() {
		_ = s.Keeper.Forget(id)
	}
}

// toolRunning records what the tool call running needs to be followed after a restart.
func (s *Server) toolRunning(r *agentRun, set func(*agent.Running)) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.chat.Running == nil {
		r.chat.Running = &agent.Running{}
	}
	set(r.chat.Running)
	s.publish(r, -1)
}

// ResumeRuns takes back the conversations that ran when the pod stopped. Called before the
// pod listens: no window can start them meanwhile.
func (s *Server) ResumeRuns() {
	data, err := os.ReadFile(s.runningPath())
	if err != nil {
		return
	}
	_ = os.Remove(s.runningPath())
	var list []runningEntry
	if json.Unmarshal(data, &list) != nil || len(list) == 0 {
		return
	}
	if s.Keeper == nil {
		log.Printf("agent: %d conversation(s) ran when the pod stopped; without keeper they are interrupted", len(list))
		return
	}
	relays := map[string]bool{}
	if l, err := s.Keeper.ListHTTP(""); err == nil {
		for _, h := range l {
			relays[h.ID] = true
		}
	}
	for _, e := range list {
		c, err := s.loadChat(e.Loc, e.ID)
		if err != nil {
			continue
		}
		if r := s.resumeRun(e, c, relays); r != "" {
			log.Printf("agent: %s taken back (%s)", e.ID, r)
		}
	}
	s.saveRunning()
}

// resumeRun starts the run of c again where it was; "" when it cannot (it is then closed as
// interrupted when opened, as without keeper).
func (s *Server) resumeRun(e runningEntry, c *agent.Chat, relays map[string]bool) string {
	run := c.Running
	if run == nil {
		run = &agent.Running{}
	}
	r := &agentRun{id: c.ID, loc: e.Loc, root: e.Root, project: e.Project, lang: e.Lang, chat: c, state: "running"}
	how := "next step"
	switch {
	case run.Stream != "":
		if !relays[run.Stream] {
			return ""
		}
		r.resumeStream = run
		how = "completion " + run.Stream
	default:
		calls, pending := unfinishedCalls(c)
		if len(calls) > 0 {
			// The results not written yet: the command still running is followed, the
			// other calls run again.
			kept := c.Messages[:0]
			for _, m := range c.Messages {
				if !(m.Role == "tool" && pending[m.ToolCallID]) {
					kept = append(kept, m)
				}
			}
			c.Messages = kept
			r.resumeCalls = calls
			if run.Proc != "" || run.Console != "" {
				r.follow = &agent.Running{Proc: run.Proc, Console: run.Console, Until: run.Until}
			}
			how = "tool calls"
		}
	}
	c.Approval = nil
	s.agents.mu.Lock()
	if s.agents.runs[c.ID] != nil {
		s.agents.mu.Unlock()
		return ""
	}
	r.ctx, r.cancel = contextWithCancel()
	s.agents.runs[c.ID] = r
	s.agents.mu.Unlock()
	go s.loop(r)
	return how
}

// unfinishedCalls are the tool calls of the last answer without a result (or with the
// result of a call still running), in order; pending names those with such a result.
func unfinishedCalls(c *agent.Chat) ([]agent.ToolCall, map[string]bool) {
	last := -1
	for i := len(c.Messages) - 1; i >= 0; i-- {
		if m := c.Messages[i]; m.Role == "assistant" {
			last = i
			break
		} else if m.Role == "user" {
			return nil, nil
		}
	}
	if last < 0 || len(c.Messages[last].ToolCalls) == 0 {
		return nil, nil
	}
	done, pending := map[string]bool{}, map[string]bool{}
	for _, m := range c.Messages[last+1:] {
		if m.Role == "tool" {
			if m.Status == "" {
				pending[m.ToolCallID] = true
			} else {
				done[m.ToolCallID] = true
			}
		}
	}
	var calls []agent.ToolCall
	for _, call := range c.Messages[last].ToolCalls {
		if !done[call.ID] {
			calls = append(calls, call)
		}
	}
	return calls, pending
}

// followed is the result of a command a run was waiting for when the pod stopped, followed
// again in the keeper; ok false when it is not one.
func (s *Server) followed(r *agentRun, ref *runtimeRef, call agent.ToolCall, a toolArgs) (toolResult, bool) {
	r.mu.Lock()
	f := r.follow
	r.follow = nil
	r.mu.Unlock()
	if f == nil {
		return toolResult{}, false
	}
	until := time.UnixMilli(f.Until)
	switch {
	case call.Function.Name == "bash" && f.Proc != "":
		s.toolRunning(r, func(run *agent.Running) { run.Proc, run.Until = f.Proc, f.Until })
		res, err := followShell(r.ctx, ref.rt, f.Proc, time.Now(), until, s.toolProgress(r, call.ID))
		s.forgetProc(f.Proc)
		if err != nil {
			return fail(r, err), true
		}
		return shellToolResult(res), true
	case call.Function.Name == "run_command" && f.Console != "":
		if _, _, err := consoleText(ref.rt, f.Console); err != nil {
			return toolResult{}, false // the console is gone: the command runs again
		}
		s.toolRunning(r, func(run *agent.Running) { run.Console, run.Until = f.Console, f.Until })
		res, err := s.waitCommand(r, ref.rt, a.str("command"), f.Console, until, s.toolProgress(r, call.ID))
		if err != nil {
			return fail(r, err), true
		}
		return res, true
	}
	return toolResult{}, false
}
