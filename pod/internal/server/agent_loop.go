package server

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/DrSmithFr/web-ide/pod/internal/agent"
	"github.com/DrSmithFr/web-ide/pod/internal/i18n"
	"github.com/DrSmithFr/web-ide/pod/internal/llm"
)

// The turn loop of the agent: send the conversation, stream the answer, run the tool calls
// it asks for and send their results back, until the model answers without tools, the user
// stops it, or it waits for the user (questions, a plan). Long conversations are compacted.

// startRun saves a conversation and runs it in the background, its messages announced from
// the index from; false when it already runs.
func (s *Server) startRun(loc llm.ChatLocation, root, project, lang string, c *agent.Chat, from int) bool {
	s.agents.mu.Lock()
	if s.agents.runs[c.ID] != nil {
		s.agents.mu.Unlock()
		return false
	}
	ctx, cancel := context.WithCancel(context.Background())
	r := &agentRun{id: c.ID, loc: loc, root: root, project: project, lang: lang, ctx: ctx, cancel: cancel, chat: c, state: "running"}
	s.agents.runs[c.ID] = r
	s.agents.mu.Unlock()
	r.mu.Lock()
	c.Running = &agent.Running{}
	s.publish(r, from)
	r.mu.Unlock()
	go s.loop(r)
	return true
}

// modelInfo tells what a model can do and its context size (cached a little: listing the
// models asks the server).
type modelInfo struct {
	caps    llm.Caps
	context int
	found   bool
}

var modelCache sync.Map // server → struct{at time.Time; list *llm.ModelList}

type cachedModels struct {
	at   time.Time
	list *llm.ModelList
}

func (s *Server) modelInfo(ctx context.Context, server, model string) modelInfo {
	var list *llm.ModelList
	if v, ok := modelCache.Load(server); ok && time.Since(v.(cachedModels).at) < 30*time.Second {
		list = v.(cachedModels).list
	} else {
		c, cancel := context.WithTimeout(ctx, 10*time.Second)
		l, err := s.LLM.Models(c, server)
		cancel()
		if err != nil {
			return modelInfo{}
		}
		list = l
		modelCache.Store(server, cachedModels{time.Now(), l})
	}
	for _, m := range list.Models {
		if m.ID == model {
			info := modelInfo{caps: m.Caps, context: m.Context, found: true}
			if list.Kind == "ollama" {
				if n := s.LLM.ServerContext(server); n > 0 {
					info.context = n
				}
			}
			return info
		}
	}
	return modelInfo{}
}

const summaryPrefix = "Summary of the earlier conversation (automatic compaction, the summarized messages are not visible anymore):\n\n"

// apiMessages are the messages as the API expects them (fields of the page and compacted
// messages removed).
//
// A page drawn by the model reaches a model reading images as an image: in a user message
// right after the tool results of its step, since servers often take only text in a tool
// result.
func apiMessages(system string, msgs []*agent.Message, vision bool) []llm.Message {
	out := []llm.Message{{Role: "system", Content: agent.String(system)}}
	var images []*agent.Message
	flush := func() {
		if len(images) == 0 {
			return
		}
		var parts []map[string]any
		for _, m := range images {
			parts = append(parts, map[string]any{"type": "text", "text": "Image of the page \"" + m.Page.Name + "\" drawn by board_draw:"},
				map[string]any{"type": "image_url", "image_url": map[string]string{"url": m.Page.PNG}})
		}
		data, _ := json.Marshal(parts)
		out = append(out, llm.Message{Role: "user", Content: data})
		images = nil
	}
	for _, m := range msgs {
		if m.Role != "tool" {
			flush()
		}
		if vision && m.Role == "tool" && !m.Compacted && m.Page != nil && m.Page.PNG != "" {
			images = append(images, m)
		}
		if m.Compacted || m.Error != "" && m.Role == "assistant" && len(m.Content) == 0 && len(m.ToolCalls) == 0 {
			continue
		}
		msg := llm.Message{Role: m.Role, Reasoning: "", ToolCallID: m.ToolCallID, Name: m.Name}
		switch {
		case m.Kind == "summary":
			msg.Content = agent.String(summaryPrefix + agent.ContentText(m.Content))
		case len(m.Content) > 0:
			msg.Content = m.Content
		case m.Role != "assistant":
			msg.Content = agent.String("")
		}
		for _, c := range m.ToolCalls {
			var tc llm.ToolCall
			tc.ID, tc.Type = c.ID, c.Type
			tc.Function.Name, tc.Function.Arguments = c.Function.Name, c.Function.Arguments
			msg.ToolCalls = append(msg.ToolCalls, tc)
		}
		out = append(out, msg)
	}
	flush()
	return out
}

// estimateTokens is a rough count of a message (images count for a fixed amount).
func estimateTokens(m *agent.Message) int {
	chars, media := 0, 0
	var s string
	if json.Unmarshal(m.Content, &s) == nil {
		chars += len(s)
	} else {
		var parts []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}
		_ = json.Unmarshal(m.Content, &parts)
		for _, p := range parts {
			if p.Type == "text" {
				chars += len(p.Text)
			} else {
				media++
			}
		}
	}
	for _, c := range m.ToolCalls {
		chars += len(c.Function.Name) + len(c.Function.Arguments)
	}
	return (chars*2+6)/7 + media*800 + 8
}

// contextUsed is the size the next request will use: the last reported usage plus what
// came after it.
func contextUsed(c *agent.Chat) int {
	extra := 0
	for i := len(c.Messages) - 1; i >= 0; i-- {
		m := c.Messages[i]
		if m.Compacted {
			break
		}
		if m.Role == "assistant" && len(m.Usage) > 0 && i >= c.ResetAt {
			var u llm.Usage
			if json.Unmarshal(m.Usage, &u) == nil {
				return u.Prompt + u.Completion + extra
			}
		}
		extra += estimateTokens(m)
	}
	return extra + 1500 // system prompt and tool definitions
}

var contextError = regexp.MustCompile(`(?i)context|exceed|too long|too many tokens|n_ctx|num_ctx|longueur`)

// target is the server and the model of the next request: the Plan model in Plan mode when
// one is chosen.
func target(c *agent.Chat) (string, string) {
	if c.Mode == agent.Plan && c.Options != nil && c.Options.PlanServer != "" && c.Options.PlanModel != "" {
		return c.Options.PlanServer, c.Options.PlanModel
	}
	return c.Server, c.Model
}

func opt(c *agent.Chat) agent.Options {
	if c.Options == nil {
		return agent.Options{}
	}
	return *c.Options
}

func boolOr(b *bool, def bool) bool {
	if b == nil {
		return def
	}
	return *b
}

// drainQueue moves the messages written during an answer into the conversation.
func drainQueue(c *agent.Chat) bool {
	if len(c.Queue) == 0 {
		return false
	}
	for _, q := range c.Queue {
		c.Messages = append(c.Messages, &agent.Message{Role: "user", Content: userContent(q.Text, q.Parts), Display: displayOf(q.Text, q.Display, q.Parts), Attachments: q.Attachments})
	}
	c.Queue = nil
	return true
}

// userContent is the content of a message of the user: its text, or the text then the parts
// (attachments).
func userContent(text string, parts json.RawMessage) json.RawMessage {
	var list []json.RawMessage
	if json.Unmarshal(parts, &list) != nil || len(list) == 0 {
		return agent.String(text)
	}
	if text != "" {
		t, _ := json.Marshal(map[string]string{"type": "text", "text": text})
		list = append([]json.RawMessage{t}, list...)
	}
	data, _ := json.Marshal(list)
	return data
}

func displayOf(text, display string, parts json.RawMessage) string {
	if display != "" {
		return display
	}
	var list []json.RawMessage
	if json.Unmarshal(parts, &list) == nil && len(list) > 0 {
		return text
	}
	return ""
}

// closeToolCalls gives a result to the tool calls left without one (interrupted run): the
// API expects it.
func closeToolCalls(c *agent.Chat, reason string) {
	done := map[string]bool{}
	for _, m := range c.Messages {
		if m.Role == "tool" {
			done[m.ToolCallID] = true
			if m.Status == "" {
				m.Status, m.Content, m.Summary = "error", agent.String(reason), agent.T("interrupted", nil).Raw()
			}
		}
	}
	for i := len(c.Messages) - 1; i >= 0; i-- {
		if m := c.Messages[i]; m.Role == "assistant" {
			for _, call := range m.ToolCalls {
				if !done[call.ID] {
					c.Messages = append(c.Messages, &agent.Message{Role: "tool", ToolCallID: call.ID, Name: call.Function.Name, Content: agent.String(reason), Status: "error", Summary: agent.T("interrupted", nil).Raw()})
				}
			}
			break
		}
	}
}

// answer is what a completion wrote, with its timing.
type answer struct {
	mu         sync.Mutex
	content    string
	reasoning  string
	started    time.Time
	thinkStart time.Time
	thinkEnd   time.Time
}

func (a *answer) add(d llm.Delta) {
	a.mu.Lock()
	defer a.mu.Unlock()
	now := time.Now()
	if d.Reasoning != "" {
		if a.thinkStart.IsZero() {
			a.thinkStart = now
		}
		a.reasoning += d.Reasoning
	}
	if d.Content != "" || d.Tool != "" {
		if !a.thinkStart.IsZero() && a.thinkEnd.IsZero() {
			a.thinkEnd = now
		}
		a.content += d.Content
	}
}

// timing sets the time spent thinking and the time of the answer on its message.
func (a *answer) timing(m *agent.Message) {
	a.mu.Lock()
	defer a.mu.Unlock()
	now := time.Now()
	if !a.thinkStart.IsZero() {
		end := a.thinkEnd
		if end.IsZero() {
			end = now
		}
		m.ThinkMs = end.Sub(a.thinkStart).Milliseconds()
	}
	m.ElapsedMs = now.Sub(a.started).Milliseconds()
}

// complete runs one completion as a job of the pod (the pages follow it with llm.attach).
func (s *Server) complete(r *agentRun, req llm.ChatRequest, stream string) (*llm.ChatResult, *answer, error) {
	a := &answer{started: time.Now()}
	if err := s.LLM.StartChat(stream, req); err != nil {
		return nil, a, err
	}
	res, err := s.LLM.WaitChat(r.ctx, stream, nil, a.add)
	if err != nil && r.ctx.Err() != nil {
		s.LLM.CancelChat(stream)
	}
	return res, a, err
}

// systemPrompt builds the system prompt of a run: the template of its mode, the
// instructions and skills of the project, the ticket linked to the conversation.
func (s *Server) systemPrompt(r *agentRun, ref *runtimeRef, c *agent.Chat, tools bool) string {
	ctx := s.LLM.LoadContext(llm.Project{Root: ref.rt.Root, FS: ref.rt.FS})
	vars := agent.PromptVars{Root: ref.rt.Root, ActiveFile: opt(c).ActiveFile}
	if ref.project != nil {
		vars.Project = ref.project.Name()
		if ref.project.SSH != nil {
			vars.Host = ref.project.SSH.Host
		}
	}
	ticket := ""
	if c.Ticket != nil {
		ticket = s.ticketPrompt(r, c.Ticket)
	}
	mode := c.Mode
	if mode == "" {
		mode = agent.Build
	}
	return agent.SystemPrompt(ctx, vars, tools, mode, ticket)
}

// loop runs the turns of a conversation until it stops.
func (s *Server) loop(r *agentRun) {
	defer func() {
		r.mu.Lock()
		r.chat.Running, r.chat.Approval = nil, nil
		r.state = "idle"
		s.publish(r, -1)
		r.mu.Unlock()
		s.agents.mu.Lock()
		delete(s.agents.runs, r.id)
		s.agents.mu.Unlock()
		r.cancel()
	}()
	ref, err := s.agentRuntime(r.project)
	if err != nil {
		r.mu.Lock()
		r.chat.Messages = append(r.chat.Messages, &agent.Message{Role: "assistant", Error: i18n.Translate(r.lang, err)})
		r.mu.Unlock()
		return
	}
	// The run keeps the runtime in use (its language servers stay up without a window).
	ref.rt.Attach()
	defer ref.rt.Detach()
	compactedForError := false
	for {
		if r.ctx.Err() != nil {
			return
		}
		r.mu.Lock()
		from := len(r.chat.Messages)
		drainQueue(r.chat)
		server, model := target(r.chat)
		r.mu.Unlock()
		info := s.modelInfo(r.ctx, server, model)
		r.mu.Lock()
		o := opt(r.chat)
		needs := boolOr(o.AutoCompact, true) && info.context > 0 && contextUsed(r.chat) > info.context*max(o.CompactAt, 75)/100
		r.mu.Unlock()
		if needs {
			if err := s.compact(r, false, ""); err == nil {
				from = 0
			}
		}
		if r.ctx.Err() != nil {
			return
		}
		r.mu.Lock()
		tools := boolOr(o.Tools, true) && (!info.found || info.caps.Tools)
		mode := r.chat.Mode
		if mode == "" {
			mode = agent.Build
		}
		system := s.systemPrompt(r, ref, r.chat, tools)
		req := llm.ChatRequest{Server: server, Model: model, Messages: apiMessages(system, r.chat.Messages, !info.found || info.caps.Vision)}
		if tools {
			req.Tools = agent.ToolsFor(mode, r.chat.Ticket)
		}
		if info.caps.Thinking {
			think := boolOr(o.Think, true)
			req.Think = &think
		}
		stream := newID()
		r.chat.Running = &agent.Running{Stream: ""}
		r.state = "running"
		s.publish(r, from)
		r.mu.Unlock()

		release, err := s.slot(r, server)
		if err != nil {
			return
		}
		r.mu.Lock()
		r.chat.Running = &agent.Running{Stream: stream}
		s.emitAgent(r.root, "agent.update", updateOf(r.chat, r.state, 0, -1))
		r.mu.Unlock()
		res, ans, err := s.complete(r, req, stream)
		release()

		r.mu.Lock()
		r.chat.Running = &agent.Running{}
		if err != nil {
			canceled := r.ctx.Err() != nil
			r.mu.Unlock()
			if !canceled && !compactedForError && ans.content == "" && contextError.MatchString(err.Error()) {
				compactedForError = true
				if s.compact(r, false, "") == nil {
					continue
				}
			}
			r.mu.Lock()
			if ans.content != "" || ans.reasoning != "" || !canceled {
				msg := &agent.Message{Role: "assistant", Content: agent.String(ans.content), Reasoning: ans.reasoning, Model: model, Mode: mode}
				ans.timing(msg)
				if canceled {
					msg.Error = "Stopped."
				} else {
					msg.Error = i18n.Translate(r.lang, err)
				}
				r.chat.Messages = append(r.chat.Messages, msg)
				s.publish(r, len(r.chat.Messages)-1)
			}
			r.mu.Unlock()
			return
		}
		msg := &agent.Message{Role: "assistant", Content: agent.String(agent.ContentText(res.Message.Content)), Reasoning: res.Message.Reasoning, Model: model, Mode: mode}
		for _, c := range res.Message.ToolCalls {
			var tc agent.ToolCall
			tc.ID, tc.Type = c.ID, c.Type
			if tc.Type == "" {
				tc.Type = "function"
			}
			tc.Function.Name, tc.Function.Arguments = c.Function.Name, c.Function.Arguments
			msg.ToolCalls = append(msg.ToolCalls, tc)
		}
		if res.Usage != nil {
			msg.Usage, _ = json.Marshal(res.Usage)
		}
		if res.Finish == "length" {
			msg.Error = "Answer cut: length limit reached."
		}
		ans.timing(msg)
		r.chat.Messages = append(r.chat.Messages, msg)
		s.publish(r, len(r.chat.Messages)-1)
		calls := msg.ToolCalls
		more := len(r.chat.Queue) > 0
		r.mu.Unlock()
		if len(calls) == 0 {
			// Messages queued during the answer: the agent goes on with them.
			if more {
				continue
			}
			return
		}
		ref, err = s.agentRuntime(r.project)
		if err != nil {
			return
		}
		if stop := s.runCalls(r, ref, calls, mode); stop || r.ctx.Err() != nil {
			return
		}
	}
}

// runCalls runs the tool calls of an answer; true when the turn ends (questions, a plan).
func (s *Server) runCalls(r *agentRun, ref *runtimeRef, calls []agent.ToolCall, mode string) bool {
	stop := false
	for _, call := range calls {
		r.mu.Lock()
		if r.ctx.Err() != nil {
			r.chat.Messages = append(r.chat.Messages, &agent.Message{Role: "tool", ToolCallID: call.ID, Name: call.Function.Name, Content: agent.String("Canceled by the user."), Status: "denied", Summary: agent.T("canceled", nil).Raw()})
			s.publish(r, len(r.chat.Messages)-1)
			r.mu.Unlock()
			continue
		}
		name := call.Function.Name
		var args map[string]json.RawMessage
		_ = json.Unmarshal([]byte(orEmpty(call.Function.Arguments)), &args)
		switch name {
		case "exit_plan_mode":
			// The plan goes to the user; the turn ends until they decide.
			var plan string
			_ = json.Unmarshal(args["plan"], &plan)
			plan = strings.TrimSpace(plan)
			m := &agent.Message{Role: "tool", ToolCallID: call.ID, Name: name}
			if plan != "" {
				m.Content, m.Status, m.Summary, m.Plan, m.PlanState = agent.String("Plan presented to the user, who will accept it or ask for changes. Wait for their answer."), "ok", agent.T("plan proposed", nil).Raw(), plan, "pending"
				stop = true
			} else {
				m.Content, m.Status, m.Summary = agent.String("Error: empty plan."), "error", agent.T("empty plan", nil).Raw()
			}
			r.chat.Messages = append(r.chat.Messages, m)
			s.publish(r, len(r.chat.Messages)-1)
			r.mu.Unlock()
			continue
		case "board_draw_image":
			// A capture of the screen of the user needs their click: the turn ends until they
			// share it or refuse (agent.capture), like questions.
			var image string
			if json.Unmarshal(args["image"], &image) != nil || strings.TrimSpace(image) != "screen" {
				break
			}
			m := &agent.Message{Role: "tool", ToolCallID: call.ID, Name: name, Status: "ok", Capture: "pending",
				Content: agent.String("Waiting for the user to share their screen."), Summary: agent.T("waiting for a capture of the screen", nil).Raw()}
			r.chat.Messages = append(r.chat.Messages, m)
			s.publish(r, len(r.chat.Messages)-1)
			r.mu.Unlock()
			stop = true
			continue
		case "ask_user":
			// The questions go to the user; the turn ends until they answer.
			qs, err := agent.NormalizeQuestions(args["questions"])
			m := &agent.Message{Role: "tool", ToolCallID: call.ID, Name: name}
			if err == nil {
				m.Content, m.Status, m.Summary, m.Questions, m.AskState = agent.String("Questions asked to the user: waiting for their answers."), "ok", agent.Tn(len(qs), "{n} question", "{n} questions", nil).Raw(), qs, "pending"
				stop = true
			} else {
				m.Content, m.Status, m.Summary = agent.String("Error: "+err.Error()+". Fix the questions and ask again."), "error", agent.T("invalid questions", nil).Raw()
			}
			r.chat.Messages = append(r.chat.Messages, m)
			s.publish(r, len(r.chat.Messages)-1)
			r.mu.Unlock()
			continue
		}
		running := agent.T("running…", nil)
		if agent.WriteTools[name] {
			running = agent.T("waiting…", nil)
		}
		m := &agent.Message{Role: "tool", ToolCallID: call.ID, Name: name, Content: agent.String(""), Summary: running.Raw()}
		r.chat.Messages = append(r.chat.Messages, m)
		idx := len(r.chat.Messages) - 1
		s.publish(r, idx)
		r.mu.Unlock()
		var res toolResult
		if name == "compact_conversation" {
			// Asked by the model: everything but the last exchange is summarized.
			var instructions string
			_ = json.Unmarshal(args["instructions"], &instructions)
			if err := s.compact(r, true, instructions); err != nil {
				res = toolResult{Content: "Error: " + i18n.Translate("en", err), Summary: agent.Plain(i18n.Translate(r.lang, err)), Status: "error"}
			} else {
				res = toolResult{Content: "Conversation compacted: the older messages are replaced by a summary.", Summary: agent.T("conversation compacted", nil).Raw(), Status: "ok"}
			}
		} else {
			res = s.agentTool(r, ref, call, mode)
		}
		r.mu.Lock()
		// The compaction may have moved the message: it is the last tool result of this call.
		for i := len(r.chat.Messages) - 1; i >= 0; i-- {
			if mm := r.chat.Messages[i]; mm.Role == "tool" && mm.ToolCallID == call.ID {
				mm.Content, mm.Summary, mm.Status, mm.Diff, mm.Page = agent.String(res.Content), res.Summary, res.Status, res.Diff, res.Page
				idx = i
				break
			}
		}
		s.publish(r, idx)
		r.mu.Unlock()
	}
	return stop
}

func orEmpty(s string) string {
	if strings.TrimSpace(s) == "" {
		return "{}"
	}
	return s
}

// ---------- compaction ----------

const summarySystem = `You summarize a conversation between a user and a programming assistant acting on a project with tools, so that another assistant can carry on without having read it.
Write a structured summary in Markdown, in the language of the conversation, with:
- the request of the user and their instructions (including the current request if it is not finished);
- the decisions taken and the important information found (files, functions, commands, errors);
- the files read or changed and what changed;
- the current state and the next planned steps.
Be precise (paths, names, values) and concise. Do not answer the conversation: summarize it.`

func cut(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n/2] + "\n… (" + itoa(int64(len(s)-n)) + " characters cut) …\n" + s[len(s)-n/2:]
}

func transcriptOf(m *agent.Message, budget int) string {
	text := agent.ContentText(m.Content)
	switch m.Role {
	case "user":
		if m.Kind == "summary" {
			return "## Earlier summary\n" + text
		}
		out := "## User\n" + cut(text, budget)
		var atts []struct {
			Name string `json:"name"`
		}
		if json.Unmarshal(m.Attachments, &atts) == nil && len(atts) > 0 {
			names := make([]string, len(atts))
			for i, a := range atts {
				names[i] = a.Name
			}
			out += "\n(attachments: " + strings.Join(names, ", ") + ")"
		}
		return out
	case "assistant":
		var calls []string
		for _, c := range m.ToolCalls {
			calls = append(calls, "→ "+c.Function.Name+"("+cut(c.Function.Arguments, 400)+")")
		}
		out := "## Assistant\n" + cut(text, budget)
		if len(calls) > 0 {
			out += "\n" + strings.Join(calls, "\n")
		}
		return out
	}
	name := m.Name
	if name == "" {
		name = "tool"
	}
	st := ""
	if m.Status != "" && m.Status != "ok" {
		st = " (" + m.Status + ")"
	}
	return "### Result of " + name + st + "\n" + cut(text, min(budget, 1500))
}

var errNothingToCompact = i18n.New("Nothing to compact")

// compact replaces the oldest messages by a summary, keeping the recent ones (about a
// quarter of the context). manual: compact even a short conversation (all but the last
// exchange).
func (s *Server) compact(r *agentRun, manual bool, instructions string) error {
	r.mu.Lock()
	msgs := r.chat.Messages
	start := 0
	for start < len(msgs) && msgs[start].Compacted {
		start++
	}
	server, model := target(r.chat)
	o := opt(r.chat)
	r.mu.Unlock()
	ctxSize := s.modelInfo(r.ctx, server, model).context
	if ctxSize == 0 {
		ctxSize = 16384
	}
	keepBudget := ctxSize / 4
	if manual {
		keepBudget = 0
	}
	// The kept tail starts at a user or assistant message (never at a tool result).
	keepFrom, tokens := len(msgs), 0
	for i := len(msgs) - 1; i > start; i-- {
		tokens += estimateTokens(msgs[i])
		if tokens > keepBudget && keepFrom < len(msgs) {
			break
		}
		if msgs[i].Role != "tool" {
			keepFrom = i
		}
	}
	if manual || keepFrom-start < 2 {
		keepFrom = len(msgs)
		for i := len(msgs) - 1; i > start; i-- {
			if msgs[i].Role == "user" && msgs[i].Kind != "summary" {
				keepFrom = i
				break
			}
		}
		if keepFrom == len(msgs) {
			keepFrom = len(msgs) - 1
		}
	}
	if keepFrom-start < 2 {
		if manual {
			return errNothingToCompact
		}
		return nil
	}
	head := msgs[start:keepFrom]
	maxChars := max(8000, ctxSize*21/10)
	per := 6000
	transcript := ""
	for {
		parts := make([]string, len(head))
		for i, m := range head {
			parts[i] = transcriptOf(m, per)
		}
		transcript = strings.Join(parts, "\n\n")
		if len(transcript) <= maxChars || per <= 300 {
			break
		}
		per /= 2
	}
	if len(transcript) > maxChars {
		transcript = transcript[len(transcript)-maxChars:]
	}
	if o.CompactSrv != "" {
		server, model = o.CompactSrv, o.CompactMdl
	}
	if server == "" || model == "" {
		return i18n.New("No model for the compaction")
	}
	sys := summarySystem
	if instructions != "" {
		sys += "\n\nInstructions of the user for this summary: " + instructions
	}
	r.mu.Lock()
	prev := r.state
	r.state = "compacting"
	s.emitAgent(r.root, "agent.update", updateOf(r.chat, r.state, 0, -1))
	r.mu.Unlock()
	defer func() {
		r.mu.Lock()
		r.state = prev
		r.mu.Unlock()
	}()
	release, err := s.slot(r, server)
	if err != nil {
		return err
	}
	no := false
	res, err := s.LLM.Chat(r.ctx, llm.ChatRequest{Server: server, Model: model, Think: &no, Messages: []llm.Message{
		{Role: "system", Content: agent.String(sys)},
		{Role: "user", Content: agent.String("Conversation to summarize:\n\n" + transcript)},
	}}, nil)
	release()
	if err != nil {
		return err
	}
	summary := strings.TrimSpace(thinkBlock.ReplaceAllString(agent.ContentText(res.Message.Content), ""))
	if summary == "" {
		return errors.New(i18n.Translate(r.lang, i18n.New("the model returned an empty summary")))
	}
	if res.Finish == "length" {
		summary += "\n\n(summary cut)"
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := start; i < keepFrom && i < len(r.chat.Messages); i++ {
		r.chat.Messages[i].Compacted = true
	}
	sm := &agent.Message{Role: "user", Kind: "summary", Content: agent.String(summary), Summarized: keepFrom - start, Model: model}
	r.chat.Messages = append(r.chat.Messages[:keepFrom], append([]*agent.Message{sm}, r.chat.Messages[keepFrom:]...)...)
	r.chat.ResetAt = len(r.chat.Messages)
	s.publish(r, 0)
	return nil
}

var thinkBlock = regexp.MustCompile(`(?s)<think>.*?</think>`)
