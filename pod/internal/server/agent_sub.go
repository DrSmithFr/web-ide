package server

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/DrSmithFr/web-ide/pod/internal/agent"
	"github.com/DrSmithFr/web-ide/pod/internal/i18n"
)

// Sub-agents (agent/subagents.go): a conversation starts children that run in the background
// with its rights. A child's notes, questions and report become messages of its parent (a
// question or a report starts a turn of the parent); the parent's answers and messages
// reach the child the same way. Events are delivered outside the locks of the runs.

// deliver adds an event to a conversation: queued while it runs or waits for the user,
// else added, and the conversation started when wake is set.
func (s *Server) deliver(r *agentRun, target string, ev agent.AgentEvent, wake bool) error {
	text := agent.EventText(ev)
	if t := s.run(target); t != nil {
		t.mu.Lock()
		if !t.done {
			t.chat.Queue = append(t.chat.Queue, agent.QueuedMessage{ID: newID(), Text: text, Event: &ev})
			s.publish(t, -1)
			t.mu.Unlock()
			return nil
		}
		t.mu.Unlock()
	}
	c, err := s.loadChat(r.loc, target)
	if err != nil {
		return err
	}
	if waitsForUser(c) {
		c.Queue = append(c.Queue, agent.QueuedMessage{ID: newID(), Text: text, Event: &ev})
		return s.publishIdle(r.loc, r.root, c, -1)
	}
	from := len(c.Messages)
	if c.Agent != nil {
		// A message to a child: its open question is left aside, an ended child starts again.
		from = minFrom(from, skipParentWait(c))
		c.Agent.Status, c.Agent.Question, c.Agent.Nudged = agent.AgentRunning, "", false
	}
	c.Messages = append(c.Messages, &agent.Message{Role: "user", Kind: "agent_event", Content: agent.String(text), Event: &ev})
	if !wake {
		return s.publishIdle(r.loc, r.root, c, from)
	}
	if !s.startRun(r.loc, r.root, s.runProject(c, r.root, r.project), r.lang, c, from) {
		// Started meanwhile: queued instead.
		return s.deliver(r, target, ev, false)
	}
	return nil
}

// waitsForUser: questions, a plan or a capture of the screen wait for the user; the events
// wait with them.
func waitsForUser(c *agent.Chat) bool {
	for i := len(c.Messages) - 1; i >= 0; i-- {
		m := c.Messages[i]
		if m.Role != "tool" {
			return false
		}
		if m.AskState == "pending" || m.PlanState == "pending" || m.Capture == "pending" {
			return true
		}
	}
	return false
}

// skipParentWait: the question of a child left without answer (a message of the parent
// follows instead).
func skipParentWait(c *agent.Chat) int {
	from := -1
	for i, m := range c.Messages {
		if m.Wait == "parent" {
			m.Wait, m.Status = "", "denied"
			m.Content = agent.String("Your parent did not answer this question; its message follows.")
			m.Summary = agent.T("not answered", nil).Raw()
			if from < 0 {
				from = i
			}
		}
	}
	return from
}

// event of the run r (a child) for its parent.
func childEvent(r *agentRun, typ string) agent.AgentEvent {
	return agent.AgentEvent{Child: r.id, Title: r.chat.Title, Type: typ}
}

// toParent sends an event of a child to its parent (outside the locks).
func (s *Server) toParent(r *agentRun, parent string, ev agent.AgentEvent, wake bool) {
	if parent == "" {
		return
	}
	if err := s.deliver(r, parent, ev, wake); err != nil {
		s.emitAgent(r.root, "agent.error", map[string]string{"id": r.id, "error": i18n.Translate(r.lang, err)})
	}
}

// childOf loads a child of the conversation of r.
func (s *Server) childOf(r *agentRun, id string) (*agent.Chat, error) {
	r.mu.Lock()
	mine := false
	for _, c := range r.chat.Children {
		mine = mine || c == id
	}
	r.mu.Unlock()
	if !mine {
		return nil, failf("%s is not one of your sub-agents (see agent_status)", id)
	}
	if t := s.run(id); t != nil {
		t.mu.Lock()
		defer t.mu.Unlock()
		data, _ := json.Marshal(t.chat)
		var c agent.Chat
		_ = json.Unmarshal(data, &c)
		return &c, nil
	}
	return s.loadChat(r.loc, id)
}

// liveStatus is the status of a child, with the state of its run.
func (s *Server) liveStatus(c *agent.Chat) string {
	st := c.Agent.Status
	if state, _ := s.stateOf(c.ID); state == "waiting_user" {
		st = "waiting_user"
	}
	return st
}

// ---------- parent side ----------

func (s *Server) spawnAgent(r *agentRun, a toolArgs) (toolResult, error) {
	title, task := strings.TrimSpace(a.str("title")), strings.TrimSpace(a.str("task"))
	if title == "" || task == "" {
		return toolResult{}, failf("title and task are required")
	}
	r.mu.Lock()
	if !agent.CanSpawn(r.chat) {
		r.mu.Unlock()
		return toolResult{}, failf("a sub-agent at this depth cannot start sub-agents: do the task yourself, or ask your parent")
	}
	children := append([]string(nil), r.chat.Children...)
	parent := r.chat
	depth, orchestrated := 1, parent.Mode == agent.Orchestrator
	if parent.Agent != nil {
		depth, orchestrated = parent.Agent.Depth+1, parent.Agent.Orchestrated
	}
	mode := a.str("mode")
	if mode != agent.Build && mode != agent.Plan && mode != agent.Briefing {
		mode = parent.Mode
	}
	if mode == agent.Orchestrator {
		mode = agent.Plan // a child explores; it does not steer
	}
	server, model := target(parent)
	var opts *agent.Options
	if parent.Options != nil {
		o := *parent.Options
		// The child keeps its server in Plan mode.
		o.PlanServer, o.PlanModel = "", ""
		opts = &o
	}
	ticket := parent.Ticket
	r.mu.Unlock()
	server, model, err := s.childTarget(r, a.str("server"), strings.TrimSpace(a.str("model")), server, model)
	if err != nil {
		return toolResult{}, err
	}
	running := 0
	for _, id := range children {
		if c, err := s.childOf(r, id); err == nil && c.Agent != nil && !agent.AgentEnded(c.Agent.Status) {
			running++
		}
	}
	if running >= agent.MaxChildren {
		return toolResult{}, failf("%d sub-agents are running already: wait for a report, or stop one (agent_stop)", running)
	}
	files := agent.ArgStrings(a["files"])
	c := &agent.Chat{ID: newID(), Title: title, Server: server, Model: model, Mode: mode, Options: opts, Ticket: ticket, Parent: r.id,
		Agent: &agent.SubAgent{Task: task, Files: files, Status: agent.AgentRunning, Depth: depth, Orchestrated: orchestrated}}
	c.Messages = []*agent.Message{{Role: "user", Kind: "agent_task", Content: agent.String(agent.TaskText(task, files))}}
	r.mu.Lock()
	r.chat.Children = append(r.chat.Children, c.ID)
	s.publish(r, -1)
	r.mu.Unlock()
	s.startRun(r.loc, r.root, r.project, r.lang, c, 0)
	on := ""
	if server != parent.Server || model != parent.Model {
		on = fmt.Sprintf(" on %s (%s)", s.LLM.ServerName(server), model)
	}
	res := ok(fmt.Sprintf("Sub-agent %s started%s: %s. It runs in the background; its questions and its report will come as messages. Go on with other work, or end your turn to wait.", c.ID, on, title),
		agent.T("sub-agent started", nil))
	res.Child = c.ID
	return res, nil
}

// childTarget is the server and model of a new child: the ones asked (a server for the
// sub-agents), else the default of the settings, else the parent's.
func (s *Server) childTarget(r *agentRun, server, model, parentServer, parentModel string) (string, string, error) {
	if server == "" {
		if ds, dm := s.LLM.ChildDefault(); ds != "" && (model == "" || model == dm) {
			return ds, dm, nil
		}
		if model == "" || model == parentModel {
			return parentServer, parentModel, nil
		}
		server = parentServer
	}
	var names []string
	found := ""
	for _, v := range s.LLM.ForChildren() {
		names = append(names, v.Name)
		if v.ID == server || strings.EqualFold(v.Name, server) {
			found = v.ID
		}
	}
	if found == "" && server == parentServer {
		found = server
	}
	if found == "" {
		return "", "", failf("unknown server for sub-agents: %q (servers: %s)", server, strings.Join(names, ", "))
	}
	if model == "" {
		if ds, dm := s.LLM.ChildDefault(); ds == found {
			return found, dm, nil
		}
		return "", "", failf("give the model of the child on %s", s.LLM.ServerName(found))
	}
	// The model must be one of the server (when it can list them).
	ctx, cancel := context.WithTimeout(r.ctx, 15*time.Second)
	defer cancel()
	if list, err := s.LLM.Models(ctx, found); err == nil {
		var ids []string
		for _, m := range list.Models {
			if m.ID == model {
				return found, model, nil
			}
			ids = append(ids, m.ID)
		}
		if len(ids) > 30 {
			ids = append(ids[:30], "…")
		}
		return "", "", failf("%s has no model %q (models: %s)", s.LLM.ServerName(found), model, strings.Join(ids, ", "))
	}
	return found, model, nil
}

// childServers tells the parent the servers it may give its sub-agents.
func (s *Server) childServers() string {
	list := s.LLM.ForChildren()
	if len(list) == 0 {
		return ""
	}
	ds, dm := s.LLM.ChildDefault()
	var b strings.Builder
	b.WriteString("Servers for sub-agents (spawn_agent server and model; without them, the child runs on")
	if ds != "" {
		fmt.Fprintf(&b, " %s (%s)):", s.LLM.ServerName(ds), dm)
	} else {
		b.WriteString(" your server and model):")
	}
	for _, v := range list {
		fmt.Fprintf(&b, "\n- %s", v.Name)
		var models []string
		for _, m := range v.Models {
			models = append(models, m.ID)
		}
		if len(models) > 0 {
			fmt.Fprintf(&b, ", models: %s", strings.Join(models, ", "))
		}
		if v.Note != "" {
			fmt.Fprintf(&b, " — %s", v.Note)
		}
		if v.Parallel > 1 {
			fmt.Fprintf(&b, " (%d at once)", v.Parallel)
		}
	}
	return b.String()
}

// waitIdle waits a little for a child whose run is ending (it just asked or reported).
func (s *Server) waitIdle(id string) bool {
	for i := 0; i < 50; i++ {
		if s.run(id) == nil {
			return true
		}
		time.Sleep(100 * time.Millisecond)
	}
	return false
}

func (s *Server) agentReply(r *agentRun, a toolArgs) (toolResult, error) {
	id, answer := a.str("child"), strings.TrimSpace(a.str("answer"))
	if answer == "" {
		return toolResult{}, failf("answer is missing")
	}
	c, err := s.childOf(r, id)
	if err != nil {
		return toolResult{}, err
	}
	if c.Agent == nil || c.Agent.Status != agent.AgentWaitingParent || !s.waitIdle(id) {
		return toolResult{}, failf("sub-agent %s has no open question (status: %s); to tell it something, use agent_message", id, s.liveStatus(c))
	}
	if c, err = s.loadChat(r.loc, id); err != nil {
		return toolResult{}, err
	}
	from := -1
	for i, m := range c.Messages {
		if m.Wait == "parent" {
			m.Wait, m.Status = "", "ok"
			m.Content = agent.String("Answer of your parent: " + answer)
			m.Summary = agent.T("answer received", nil).Raw()
			from = i
		}
	}
	if from < 0 {
		return toolResult{}, failf("sub-agent %s has no open question", id)
	}
	c.Agent.Status, c.Agent.Question = agent.AgentRunning, ""
	s.startRun(r.loc, r.root, r.project, r.lang, c, from)
	res := okPlain("Answer sent; the sub-agent goes on.", c.Title)
	res.Child = id
	return res, nil
}

func (s *Server) agentMessage(r *agentRun, a toolArgs) (toolResult, error) {
	id, text := a.str("child"), strings.TrimSpace(a.str("text"))
	if text == "" {
		return toolResult{}, failf("text is missing")
	}
	c, err := s.childOf(r, id)
	if err != nil {
		return toolResult{}, err
	}
	s.waitIdle(id)
	r.mu.Lock()
	title := r.chat.Title
	r.mu.Unlock()
	if err := s.deliver(r, id, agent.AgentEvent{Child: id, Title: title, Type: "message", Text: text}, true); err != nil {
		return toolResult{}, err
	}
	res := okPlain("Message sent to the sub-agent.", c.Title)
	res.Child = id
	return res, nil
}

// stopChild stops a child (its parent or the user asked): it ends as stopped.
func (s *Server) stopChild(r *agentRun, id string) error {
	set := func(c *agent.Chat) int {
		c.Agent.Status, c.Agent.Question = agent.AgentStopped, ""
		return skipParentWait(c)
	}
	if t := s.run(id); t != nil {
		t.mu.Lock()
		if t.chat.Agent != nil {
			set(t.chat)
		}
		t.mu.Unlock()
		t.cancel()
		return nil
	}
	c, err := s.loadChat(r.loc, id)
	if err != nil {
		return err
	}
	if c.Agent == nil || agent.AgentEnded(c.Agent.Status) {
		return nil
	}
	from := set(c)
	c.Queue = nil
	return s.publishIdle(r.loc, r.root, c, from)
}

func (s *Server) agentStop(r *agentRun, a toolArgs) (toolResult, error) {
	id := a.str("child")
	c, err := s.childOf(r, id)
	if err != nil {
		return toolResult{}, err
	}
	if err := s.stopChild(r, id); err != nil {
		return toolResult{}, err
	}
	res := okPlain("Sub-agent stopped.", c.Title)
	res.Child = id
	return res, nil
}

func (s *Server) agentStatus(r *agentRun) (toolResult, error) {
	r.mu.Lock()
	ids := append([]string(nil), r.chat.Children...)
	r.mu.Unlock()
	if len(ids) == 0 {
		return ok("No sub-agent.", agent.Tn(0, "{n} sub-agent", "{n} sub-agents", nil)), nil
	}
	var b strings.Builder
	for _, id := range ids {
		c, err := s.childOf(r, id)
		if err != nil || c.Agent == nil {
			continue
		}
		fmt.Fprintf(&b, "- %s %q: %s", id, c.Title, s.liveStatus(c))
		if n := c.Agent.Note; n != nil {
			fmt.Fprintf(&b, "\n  latest note: %s: %s", n.Title, cut(n.Text, 300))
		}
		if c.Agent.Question != "" {
			fmt.Fprintf(&b, "\n  open question: %s", c.Agent.Question)
		}
		if c.Agent.Report != "" {
			fmt.Fprintf(&b, "\n  report: %s", cut(c.Agent.Report, 600))
		}
		if c.Agent.Error != "" {
			fmt.Fprintf(&b, "\n  error: %s", c.Agent.Error)
		}
		b.WriteString("\n")
	}
	return ok(b.String(), agent.Tn(len(ids), "{n} sub-agent", "{n} sub-agents", nil)), nil
}

// agentAdopt: an Orchestrator takes a conversation that runs on its own under its wing; it
// is asked to announce itself.
func (s *Server) agentAdopt(r *agentRun, a toolArgs) (toolResult, error) {
	id := strings.TrimSpace(a.str("chat"))
	if id == r.id {
		return toolResult{}, failf("a conversation cannot adopt itself")
	}
	var title string
	adopt := func(c *agent.Chat) error {
		switch {
		case c.Parent == r.id:
			return failf("%s is already one of your sub-agents", id)
		case c.Parent != "":
			return failf("%s is a sub-agent of %s: it reports there", id, c.Parent)
		case c.Mode == agent.Orchestrator:
			return failf("an Orchestrator conversation cannot be adopted")
		}
		c.Parent, title = r.id, c.Title
		c.Agent = &agent.SubAgent{Task: c.Title, Status: agent.AgentRunning, Depth: 1, Orchestrated: true, Adopted: true}
		return nil
	}
	if t := s.run(id); t != nil {
		t.mu.Lock()
		err := adopt(t.chat)
		if err == nil {
			s.publish(t, -1)
		}
		t.mu.Unlock()
		if err != nil {
			return toolResult{}, err
		}
	} else {
		c, err := s.loadChat(r.loc, id)
		if err != nil {
			return toolResult{}, failf("unknown conversation %q (see list_conversations)", id)
		}
		if err := adopt(c); err != nil {
			return toolResult{}, err
		}
		if err := s.publishIdle(r.loc, r.root, c, -1); err != nil {
			return toolResult{}, err
		}
	}
	r.mu.Lock()
	r.chat.Children = append(r.chat.Children, id)
	parentTitle := r.chat.Title
	s.publish(r, -1)
	r.mu.Unlock()
	// Its announcement comes back as a note (a running conversation reads it after its step).
	if err := s.deliver(r, id, agent.AgentEvent{Child: id, Title: parentTitle, Type: "message", Text: agent.AdoptText}, true); err != nil {
		return toolResult{}, err
	}
	res := ok(fmt.Sprintf("Conversation %s %q adopted: it goes on with the user, announces itself with a note and will send its report when its task is over.", id, title), agent.T("adopted: {title}", map[string]any{"title": title}))
	res.Child = id
	return res, nil
}

// ---------- child side ----------

func (s *Server) agentNote(r *agentRun, a toolArgs) (toolResult, error) {
	title, text := strings.TrimSpace(a.str("title")), strings.TrimSpace(a.str("text"))
	if title == "" {
		return toolResult{}, failf("title is missing")
	}
	r.mu.Lock()
	if r.chat.Agent == nil {
		r.mu.Unlock()
		return toolResult{}, failf("agent_note is for sub-agents")
	}
	r.chat.Agent.Note = &agent.AgentNote{Title: title, Text: text}
	ev := childEvent(r, "note")
	parent := r.chat.Parent
	r.mu.Unlock()
	ev.Head, ev.Text = title, text
	s.toParent(r, parent, ev, false)
	return okPlain("Noted; your parent will read it.", title), nil
}

// childAsk is agent_ask, run by runCalls with r.mu held: the turn of the child stops until
// its parent answers. Returns the event to send once the lock is released.
func childAsk(r *agentRun, call agent.ToolCall, question string) (*agent.Message, *agent.AgentEvent) {
	m := &agent.Message{Role: "tool", ToolCallID: call.ID, Name: call.Function.Name}
	sa := r.chat.Agent
	switch {
	case sa == nil:
		m.Content, m.Status, m.Summary = agent.String("Error: agent_ask is for sub-agents."), "error", agent.T("not a sub-agent", nil).Raw()
		return m, nil
	case strings.TrimSpace(question) == "":
		m.Content, m.Status, m.Summary = agent.String("Error: question is missing."), "error", agent.T("question missing", nil).Raw()
		return m, nil
	case sa.Asked >= agent.MaxChildQuestions:
		m.Content, m.Status = agent.String(fmt.Sprintf("Error: you asked %d questions already. Decide by yourself, or end with agent_report (blocked).", sa.Asked)), "error"
		m.Summary = agent.T("too many questions", nil).Raw()
		return m, nil
	}
	sa.Asked++
	sa.Status, sa.Question = agent.AgentWaitingParent, question
	m.Content, m.Status, m.Wait = agent.String("Waiting for the answer of your parent."), "ok", "parent"
	m.Summary = agent.T("waiting for the parent", nil).Raw()
	ev := childEvent(r, "question")
	ev.Text = question
	return m, &ev
}

// childReport is agent_report (r.mu held): the child ends.
func childReport(r *agentRun, call agent.ToolCall, args map[string]json.RawMessage) (*agent.Message, *agent.AgentEvent) {
	m := &agent.Message{Role: "tool", ToolCallID: call.ID, Name: call.Function.Name}
	var summary, status string
	_ = json.Unmarshal(args["summary"], &summary)
	_ = json.Unmarshal(args["status"], &status)
	sa := r.chat.Agent
	if sa == nil || strings.TrimSpace(summary) == "" {
		m.Content, m.Status, m.Summary = agent.String("Error: agent_report needs a summary (and is for sub-agents)."), "error", agent.T("invalid report", nil).Raw()
		return m, nil
	}
	if status != agent.AgentBlocked {
		status = agent.AgentDone
	}
	files := agent.ArgStrings(args["files_changed"])
	sa.Status, sa.Report, sa.Changed, sa.Question = status, strings.TrimSpace(summary), files, ""
	m.Content, m.Status, m.Summary = agent.String("Report sent to your parent; your task is over."), "ok", agent.T("report sent", nil).Raw()
	ev := childEvent(r, "report")
	ev.Text, ev.Status, ev.Files = sa.Report, status, files
	return m, &ev
}

// childAnswered: a child answered without tools. Told once to report; the second time its
// answer is its report. Returns whether the loop goes on, and the event to send (r.mu held).
func childAnswered(r *agentRun, msg *agent.Message) (bool, *agent.AgentEvent) {
	sa := r.chat.Agent
	if sa == nil || sa.Status != agent.AgentRunning || sa.Adopted {
		return false, nil // an adopted conversation answers the user
	}
	if !sa.Nudged {
		sa.Nudged = true
		r.chat.Messages = append(r.chat.Messages, &agent.Message{Role: "user", Kind: "agent_nudge",
			Content: agent.String("You are a sub-agent: end with agent_report (your parent reads only the report), or ask your parent with agent_ask.")})
		return true, nil
	}
	sa.Status, sa.Report = agent.AgentDone, strings.TrimSpace(agent.ContentText(msg.Content))
	ev := childEvent(r, "report")
	ev.Text, ev.Status = sa.Report, agent.AgentDone
	return false, &ev
}

// childEnded: the run of a child ends (r.mu held). Stopped by the user or failed while it
// was running: its parent is told.
func childEnded(r *agentRun) *agent.AgentEvent {
	sa := r.chat.Agent
	if sa == nil || sa.Status != agent.AgentRunning || len(r.chat.Messages) == 0 {
		return nil
	}
	last := r.chat.Messages[len(r.chat.Messages)-1]
	canceled := r.ctx.Err() != nil
	if !canceled && (last.Role != "assistant" || last.Error == "") {
		return nil
	}
	ev := childEvent(r, "report")
	if canceled {
		sa.Status = agent.AgentStopped
		ev.Text = "Stopped by the user."
	} else {
		sa.Status, sa.Error = agent.AgentError, last.Error
		ev.Text = "Error: " + last.Error
	}
	ev.Status = sa.Status
	return &ev
}
