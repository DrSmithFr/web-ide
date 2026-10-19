package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/DrSmithFr/web-ide/pod/internal/assistant"
	"github.com/DrSmithFr/web-ide/pod/internal/i18n"
)

// Sub-agents (assistant/subagents.go): a conversation starts children that run in the background
// with its rights. A child's notes, questions and report become messages of its parent (a
// question or a report starts a turn of the parent); the parent's answers and messages
// reach the child the same way. Events are delivered outside the locks of the runs.

// deliver adds an event to a conversation: queued while it runs or waits for the user,
// else added, and the conversation started when wake is set.
func (s *A) deliver(r *assistantRun, target string, ev assistant.AgentEvent, wake bool) error {
	text := assistant.EventText(ev)
	if t := s.run(target); t != nil {
		t.mu.Lock()
		if !t.done {
			t.chat.Queue = append(t.chat.Queue, assistant.QueuedMessage{ID: assistant.NewID(), Text: text, Event: &ev})
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
		c.Queue = append(c.Queue, assistant.QueuedMessage{ID: assistant.NewID(), Text: text, Event: &ev})
		return s.publishIdle(r.loc, r.root, c, -1)
	}
	from := len(c.Messages)
	if c.Agent != nil {
		// A message to a child: its open question is left aside, an ended child starts again.
		from = minFrom(from, skipParentWait(c))
		c.Agent.Status, c.Agent.Question, c.Agent.Nudged = assistant.AgentRunning, "", false
	}
	c.Messages = append(c.Messages, &assistant.Message{Role: "user", Kind: "agent_event", Content: assistant.String(text), Event: &ev})
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
func waitsForUser(c *assistant.Chat) bool {
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
func skipParentWait(c *assistant.Chat) int {
	from := -1
	for i, m := range c.Messages {
		if m.Wait == "parent" {
			m.Wait, m.Status = "", "denied"
			m.Content = assistant.String("Your parent did not answer this question; its message follows.")
			m.Summary = assistant.T("not answered", nil).Raw()
			if from < 0 {
				from = i
			}
		}
	}
	return from
}

// event of the run r (a child) for its parent.
func childEvent(r *assistantRun, typ string) assistant.AgentEvent {
	return assistant.AgentEvent{Child: r.id, Title: r.chat.Title, Type: typ}
}

// toParent sends an event of a child to its parent (outside the locks).
func (s *A) toParent(r *assistantRun, parent string, ev assistant.AgentEvent, wake bool) {
	if parent == "" {
		return
	}
	if err := s.deliver(r, parent, ev, wake); err != nil {
		s.emitAssistant(r.root, "assistant.error", map[string]string{"id": r.id, "error": i18n.Translate(r.lang, err)})
	}
}

// childOf loads a child of the conversation of r.
func (s *A) childOf(r *assistantRun, id string) (*assistant.Chat, error) {
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
		var c assistant.Chat
		_ = json.Unmarshal(data, &c)
		return &c, nil
	}
	return s.loadChat(r.loc, id)
}

// liveStatus is the status of a child, with the state of its run.
func (s *A) liveStatus(c *assistant.Chat) string {
	st := c.Agent.Status
	if state, _ := s.stateOf(c.ID); state == "waiting_user" {
		st = "waiting_user"
	}
	return st
}

// ---------- parent side ----------

func (s *A) spawnAgent(r *assistantRun, a assistant.Args) (assistant.ToolResult, error) {
	title, task := strings.TrimSpace(a.Str("title")), strings.TrimSpace(a.Str("task"))
	if title == "" || task == "" {
		return assistant.ToolResult{}, assistant.Usagef("title and task are required")
	}
	r.mu.Lock()
	if !assistant.CanSpawn(r.chat) {
		r.mu.Unlock()
		return assistant.ToolResult{}, failf("a sub-agent at this depth cannot start sub-agents: do the task yourself, or ask your parent")
	}
	children := append([]string(nil), r.chat.Children...)
	parent := r.chat
	depth, orchestrated := 1, parent.Mode == assistant.Orchestrator
	if parent.Agent != nil {
		depth, orchestrated = parent.Agent.Depth+1, parent.Agent.Orchestrated
	}
	mode := a.Str("mode")
	if mode != assistant.Build && mode != assistant.Plan && mode != assistant.Briefing {
		mode = parent.Mode
	}
	if mode == assistant.Orchestrator {
		mode = assistant.Plan // a child explores; it does not steer
	}
	server, model := target(parent)
	var opts *assistant.Options
	if parent.Options != nil {
		o := *parent.Options
		// The child keeps its server in Plan mode.
		o.PlanServer, o.PlanModel = "", ""
		opts = &o
	}
	ticket := parent.Ticket
	r.mu.Unlock()
	server, model, err := s.childTarget(r, a.Str("server"), strings.TrimSpace(a.Str("model")), server, model)
	if err != nil {
		return assistant.ToolResult{}, err
	}
	running := 0
	for _, id := range children {
		if c, err := s.childOf(r, id); err == nil && c.Agent != nil && !assistant.AgentEnded(c.Agent.Status) {
			running++
		}
	}
	if running >= assistant.MaxChildren {
		return assistant.ToolResult{}, failf("%d sub-agents are running already: wait for a report, or stop one (agent_stop)", running)
	}
	files := assistant.ArgStrings(a["files"])
	c := &assistant.Chat{ID: assistant.NewID(), Title: title, Server: server, Model: model, Mode: mode, Options: opts, Ticket: ticket, Parent: r.id,
		Agent: &assistant.SubAgent{Task: task, Files: files, Status: assistant.AgentRunning, Depth: depth, Orchestrated: orchestrated}}
	c.Messages = []*assistant.Message{{Role: "user", Kind: "agent_task", Content: assistant.String(assistant.TaskText(task, files))}}
	r.mu.Lock()
	r.chat.Children = append(r.chat.Children, c.ID)
	s.publish(r, -1)
	r.mu.Unlock()
	s.startRun(r.loc, r.root, r.project, r.lang, c, 0)
	on := ""
	if server != parent.Server || model != parent.Model {
		on = fmt.Sprintf(" on %s (%s)", s.LLM.ServerName(server), model)
	}
	res := assistant.OK(fmt.Sprintf("Sub-agent %s started%s: %s. It runs in the background; its questions and its report will come as messages. Go on with other work, or end your turn to wait.", c.ID, on, title),
		assistant.T("sub-agent started", nil))
	res.Child = c.ID
	return res, nil
}

// childTarget is the server and model of a new child: the ones asked (a server for the
// sub-agents), else the default of the settings, else the parent's.
func (s *A) childTarget(r *assistantRun, server, model, parentServer, parentModel string) (string, string, error) {
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
		return "", "", assistant.Usagef("unknown server for sub-agents: %q (servers: %s)", server, strings.Join(names, ", "))
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
func (s *A) childServers() string {
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
func (s *A) waitIdle(id string) bool {
	for i := 0; i < 50; i++ {
		if s.run(id) == nil {
			return true
		}
		time.Sleep(100 * time.Millisecond)
	}
	return false
}

func (s *A) agentReply(r *assistantRun, a assistant.Args) (assistant.ToolResult, error) {
	id, answer := a.Str("child"), strings.TrimSpace(a.Str("answer"))
	if answer == "" {
		return assistant.ToolResult{}, assistant.Usagef("answer is missing")
	}
	c, err := s.childOf(r, id)
	if err != nil {
		return assistant.ToolResult{}, err
	}
	if c.Agent == nil || c.Agent.Status != assistant.AgentWaitingParent || !s.waitIdle(id) {
		return assistant.ToolResult{}, failf("sub-agent %s has no open question (status: %s); to tell it something, use agent_message", id, s.liveStatus(c))
	}
	if c, err = s.loadChat(r.loc, id); err != nil {
		return assistant.ToolResult{}, err
	}
	from := -1
	for i, m := range c.Messages {
		if m.Wait == "parent" {
			m.Wait, m.Status = "", "ok"
			m.Content = assistant.String("Answer of your parent: " + answer)
			m.Summary = assistant.T("answer received", nil).Raw()
			from = i
		}
	}
	if from < 0 {
		return assistant.ToolResult{}, failf("sub-agent %s has no open question", id)
	}
	c.Agent.Status, c.Agent.Question = assistant.AgentRunning, ""
	s.startRun(r.loc, r.root, r.project, r.lang, c, from)
	res := assistant.OKPlain("Answer sent; the sub-agent goes on.", c.Title)
	res.Child = id
	return res, nil
}

func (s *A) agentMessage(r *assistantRun, a assistant.Args) (assistant.ToolResult, error) {
	id, text := a.Str("child"), strings.TrimSpace(a.Str("text"))
	if text == "" {
		return assistant.ToolResult{}, assistant.Usagef("text is missing")
	}
	c, err := s.childOf(r, id)
	if err != nil {
		return assistant.ToolResult{}, err
	}
	s.waitIdle(id)
	r.mu.Lock()
	title := r.chat.Title
	r.mu.Unlock()
	if err := s.deliver(r, id, assistant.AgentEvent{Child: id, Title: title, Type: "message", Text: text}, true); err != nil {
		return assistant.ToolResult{}, err
	}
	res := assistant.OKPlain("Message sent to the sub-agent.", c.Title)
	res.Child = id
	return res, nil
}

// stopChild stops a child (its parent or the user asked): it ends as stopped.
func (s *A) stopChild(r *assistantRun, id string) error {
	set := func(c *assistant.Chat) int {
		c.Agent.Status, c.Agent.Question = assistant.AgentStopped, ""
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
	if c.Agent == nil || assistant.AgentEnded(c.Agent.Status) {
		return nil
	}
	from := set(c)
	c.Queue = nil
	return s.publishIdle(r.loc, r.root, c, from)
}

func (s *A) agentStop(r *assistantRun, a assistant.Args) (assistant.ToolResult, error) {
	id := a.Str("child")
	c, err := s.childOf(r, id)
	if err != nil {
		return assistant.ToolResult{}, err
	}
	if err := s.stopChild(r, id); err != nil {
		return assistant.ToolResult{}, err
	}
	res := assistant.OKPlain("Sub-agent stopped.", c.Title)
	res.Child = id
	return res, nil
}

func (s *A) agentStatus(r *assistantRun) (assistant.ToolResult, error) {
	r.mu.Lock()
	ids := append([]string(nil), r.chat.Children...)
	r.mu.Unlock()
	if len(ids) == 0 {
		return assistant.OK("No sub-agent.", assistant.Tn(0, "{n} sub-agent", "{n} sub-agents", nil)), nil
	}
	var b strings.Builder
	for _, id := range ids {
		c, err := s.childOf(r, id)
		if err != nil || c.Agent == nil {
			continue
		}
		fmt.Fprintf(&b, "- %s %q: %s", id, c.Title, s.liveStatus(c))
		if n := c.Agent.Note; n != nil {
			fmt.Fprintf(&b, "\n  latest note: %s: %s", n.Title, assistant.Cut(n.Text, 300))
		}
		if c.Agent.Question != "" {
			fmt.Fprintf(&b, "\n  open question: %s", c.Agent.Question)
		}
		if c.Agent.Report != "" {
			fmt.Fprintf(&b, "\n  report: %s", assistant.Cut(c.Agent.Report, 600))
		}
		if c.Agent.Error != "" {
			fmt.Fprintf(&b, "\n  error: %s", c.Agent.Error)
		}
		b.WriteString("\n")
	}
	return assistant.OK(b.String(), assistant.Tn(len(ids), "{n} sub-agent", "{n} sub-agents", nil)), nil
}

// adoptable checks that the Orchestrator of r may adopt a conversation, and makes it its
// adopted sub-agent (the caller saves it and adds it to the children of r).
func adoptable(r *assistantRun, c *assistant.Chat) error {
	switch {
	case c.ID == r.id:
		return failf("a conversation cannot adopt itself")
	case c.Parent == r.id:
		return failf("%s is already one of your sub-agents", c.ID)
	case c.Parent != "":
		return failf("%s is a sub-agent of %s: it reports there", c.ID, c.Parent)
	case c.Mode == assistant.Orchestrator:
		return failf("an Orchestrator conversation cannot be adopted")
	}
	c.Parent = r.id
	c.Agent = &assistant.SubAgent{Task: c.Title, Status: assistant.AgentRunning, Depth: 1, Orchestrated: true, Adopted: true}
	return nil
}

// agentAdopt: an Orchestrator takes a conversation that runs on its own under its wing; it
// is asked to announce itself.
func (s *A) agentAdopt(r *assistantRun, a assistant.Args) (assistant.ToolResult, error) {
	id := strings.TrimSpace(a.Str("chat"))
	if id == r.id {
		return assistant.ToolResult{}, failf("a conversation cannot adopt itself")
	}
	var title string
	adopt := func(c *assistant.Chat) error {
		title = c.Title
		return adoptable(r, c)
	}
	if t := s.run(id); t != nil {
		t.mu.Lock()
		err := adopt(t.chat)
		if err == nil {
			s.publish(t, -1)
		}
		t.mu.Unlock()
		if err != nil {
			return assistant.ToolResult{}, err
		}
	} else {
		c, err := s.loadChat(r.loc, id)
		if err != nil {
			return assistant.ToolResult{}, assistant.Usagef("unknown conversation %q (see list_conversations)", id)
		}
		if err := adopt(c); err != nil {
			return assistant.ToolResult{}, err
		}
		if err := s.publishIdle(r.loc, r.root, c, -1); err != nil {
			return assistant.ToolResult{}, err
		}
	}
	r.mu.Lock()
	r.chat.Children = append(r.chat.Children, id)
	parentTitle := r.chat.Title
	s.publish(r, -1)
	r.mu.Unlock()
	// Its announcement comes back as a note (a running conversation reads it after its step).
	if err := s.deliver(r, id, assistant.AgentEvent{Child: id, Title: parentTitle, Type: "message", Text: assistant.AdoptText}, true); err != nil {
		return assistant.ToolResult{}, err
	}
	res := assistant.OK(fmt.Sprintf("Conversation %s %q adopted: it goes on with the user, announces itself with a note and will send its report when its task is over.", id, title), assistant.T("adopted: {title}", map[string]any{"title": title}))
	res.Child = id
	return res, nil
}

// agentResume: an Orchestrator resumes a conversation whose last answer failed, from its last
// completed step; one that runs on its own is adopted on the way (and told so).
func (s *A) agentResume(r *assistantRun, a assistant.Args) (assistant.ToolResult, error) {
	id := strings.TrimSpace(a.Str("chat"))
	if id == r.id {
		return assistant.ToolResult{}, failf("a conversation cannot resume itself")
	}
	if s.run(id) != nil {
		return assistant.ToolResult{}, failf("%s is running already", id)
	}
	c, err := s.loadChat(r.loc, id)
	if err != nil {
		return assistant.ToolResult{}, assistant.Usagef("unknown conversation %q (see list_conversations)", id)
	}
	if !failed(c) {
		return assistant.ToolResult{}, failf("%s did not fail: nothing to resume (agent_message writes to your sub-agents)", id)
	}
	adopted := false
	if c.Parent != r.id {
		if err := adoptable(r, c); err != nil {
			return assistant.ToolResult{}, err
		}
		adopted = true
	}
	from := resumeFrom(c)
	revive(c)
	if c.Agent != nil {
		c.Agent.Status, c.Agent.Error = assistant.AgentRunning, ""
	}
	r.mu.Lock()
	parentTitle := r.chat.Title
	if adopted {
		r.chat.Children = append(r.chat.Children, id)
		s.publish(r, -1)
	}
	r.mu.Unlock()
	if adopted {
		ev := assistant.AgentEvent{Child: id, Title: parentTitle, Type: "message", Text: assistant.AdoptText + " Your last answer failed: go on from where you stopped."}
		c.Messages = append(c.Messages, &assistant.Message{Role: "user", Kind: "agent_event", Content: assistant.String(assistant.EventText(ev)), Event: &ev})
	}
	if !s.startRun(r.loc, r.root, s.runProject(c, r.root, r.project), r.lang, c, from) {
		return assistant.ToolResult{}, failf("%s is running already", id)
	}
	text := fmt.Sprintf("Conversation %s %q resumed from its last completed step.", id, c.Title)
	if adopted {
		text += " It is now one of your sub-agents: it announces itself with a note and reports when its task is over."
	}
	res := assistant.OK(text, assistant.T("resumed: {title}", map[string]any{"title": c.Title}))
	res.Child = id
	return res, nil
}

// ---------- child side ----------

func (s *A) agentNote(r *assistantRun, a assistant.Args) (assistant.ToolResult, error) {
	title, text := strings.TrimSpace(a.Str("title")), strings.TrimSpace(a.Str("text"))
	if title == "" {
		return assistant.ToolResult{}, assistant.Usagef("title is missing")
	}
	r.mu.Lock()
	if r.chat.Agent == nil {
		r.mu.Unlock()
		return assistant.ToolResult{}, failf("agent_note is for sub-agents")
	}
	r.chat.Agent.Note = &assistant.AgentNote{Title: title, Text: text}
	ev := childEvent(r, "note")
	parent := r.chat.Parent
	r.mu.Unlock()
	ev.Head, ev.Text = title, text
	s.toParent(r, parent, ev, false)
	return assistant.OKPlain("Noted; your parent will read it.", title), nil
}

// childAsk is agent_ask, run by runCalls with r.mu held: the turn of the child stops until
// its parent answers. Returns the event to send once the lock is released.
func childAsk(r *assistantRun, call assistant.ToolCall, question string) (*assistant.Message, *assistant.AgentEvent) {
	m := &assistant.Message{Role: "tool", ToolCallID: call.ID, Name: call.Function.Name}
	sa := r.chat.Agent
	switch {
	case sa == nil:
		m.Content, m.Status, m.Summary = assistant.String("Error: agent_ask is for sub-agents."), "error", assistant.T("not a sub-agent", nil).Raw()
		return m, nil
	case strings.TrimSpace(question) == "":
		m.Content, m.Status, m.Summary = assistant.String("Error: question is missing."), "error", assistant.T("question missing", nil).Raw()
		return m, nil
	case sa.Asked >= assistant.MaxChildQuestions:
		m.Content, m.Status = assistant.String(fmt.Sprintf("Error: you asked %d questions already. Decide by yourself, or end with agent_report (blocked).", sa.Asked)), "error"
		m.Summary = assistant.T("too many questions", nil).Raw()
		return m, nil
	}
	sa.Asked++
	sa.Status, sa.Question = assistant.AgentWaitingParent, question
	m.Content, m.Status, m.Wait = assistant.String("Waiting for the answer of your parent."), "ok", "parent"
	m.Summary = assistant.T("waiting for the parent", nil).Raw()
	ev := childEvent(r, "question")
	ev.Text = question
	return m, &ev
}

// childReport is agent_report (r.mu held): the child ends.
func childReport(r *assistantRun, call assistant.ToolCall, args map[string]json.RawMessage) (*assistant.Message, *assistant.AgentEvent) {
	m := &assistant.Message{Role: "tool", ToolCallID: call.ID, Name: call.Function.Name}
	var summary, status string
	_ = json.Unmarshal(args["summary"], &summary)
	_ = json.Unmarshal(args["status"], &status)
	sa := r.chat.Agent
	if sa == nil || strings.TrimSpace(summary) == "" {
		m.Content, m.Status, m.Summary = assistant.String("Error: agent_report needs a summary (and is for sub-agents)."), "error", assistant.T("invalid report", nil).Raw()
		return m, nil
	}
	if status != assistant.AgentBlocked {
		status = assistant.AgentDone
	}
	files := assistant.ArgStrings(args["files_changed"])
	sa.Status, sa.Report, sa.Changed, sa.Question = status, strings.TrimSpace(summary), files, ""
	m.Content, m.Status, m.Summary = assistant.String("Report sent to your parent; your task is over."), "ok", assistant.T("report sent", nil).Raw()
	ev := childEvent(r, "report")
	ev.Text, ev.Status, ev.Files = sa.Report, status, files
	return m, &ev
}

// childAnswered: a child answered without tools. Told once to report; the second time its
// answer is its report. Returns whether the loop goes on, and the event to send (r.mu held).
func childAnswered(r *assistantRun, msg *assistant.Message) (bool, *assistant.AgentEvent) {
	sa := r.chat.Agent
	if sa == nil || sa.Status != assistant.AgentRunning || sa.Adopted {
		return false, nil // an adopted conversation answers the user
	}
	if !sa.Nudged {
		sa.Nudged = true
		r.chat.Messages = append(r.chat.Messages, &assistant.Message{Role: "user", Kind: "agent_nudge",
			Content: assistant.String("You are a sub-agent: end with agent_report (your parent reads only the report), or ask your parent with agent_ask.")})
		return true, nil
	}
	sa.Status, sa.Report = assistant.AgentDone, strings.TrimSpace(assistant.ContentText(msg.Content))
	ev := childEvent(r, "report")
	ev.Text, ev.Status = sa.Report, assistant.AgentDone
	return false, &ev
}

// childEnded: the run of a child ends (r.mu held). Stopped by the user or failed while it
// was running: its parent is told.
func childEnded(r *assistantRun) *assistant.AgentEvent {
	sa := r.chat.Agent
	if sa == nil || sa.Status != assistant.AgentRunning || len(r.chat.Messages) == 0 {
		return nil
	}
	last := r.chat.Messages[len(r.chat.Messages)-1]
	canceled := r.ctx.Err() != nil
	if !canceled && (last.Role != "assistant" || last.Error == "") {
		return nil
	}
	ev := childEvent(r, "report")
	if canceled {
		sa.Status = assistant.AgentStopped
		ev.Text = "Stopped by the user."
	} else {
		sa.Status, sa.Error = assistant.AgentError, last.Error
		ev.Text = "Error: " + last.Error
	}
	ev.Status = sa.Status
	return &ev
}
