package acp

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/DrSmithFr/web-ide/pod/internal/i18n"
)

// States of a session.
const (
	Starting = "starting"
	Ready    = "ready"
	Busy     = "busy"
	Asking   = "asking"
	Failed   = "error"
)

// Crash limits: that many exits within the window stop the restarts.
const (
	maxExits   = 3
	exitWindow = time.Minute
)

type Usage struct {
	Used int64 `json:"used"`
	Size int64 `json:"size"`
}

// ConfigOption is a setting of the session the agent offers (mode, model, effort…).
type ConfigOption struct {
	ID           string          `json:"id"`
	Name         string          `json:"name"`
	Category     string          `json:"category,omitempty"`
	Type         string          `json:"type"`
	CurrentValue string          `json:"currentValue"`
	Options      json.RawMessage `json:"options,omitempty"`
}

// Info is a session as the tab shows it. Seq counts its changes: an event older than the
// snapshot the page has is dropped.
type Info struct {
	ID    string `json:"id"`
	Agent string `json:"agent"`
	Name  string `json:"name"`
	Title string `json:"title"`
	Cwd   string `json:"cwd"`
	State string `json:"state"`
	Error string `json:"error,omitempty"`
	// Crashed: the agent kept ending (Error is the end of its error output).
	Crashed bool           `json:"crashed,omitempty"`
	Config  []ConfigOption `json:"config,omitempty"`
	Usage   *Usage         `json:"usage,omitempty"`
	Started int64          `json:"started"`
	// Commands: the slash commands of the agent, as it sent them.
	Commands json.RawMessage `json:"commands,omitempty"`
	Seq      int64           `json:"seq"`
}

// saved is the file of a session: what a restarted pod needs to go on.
type saved struct {
	Info    Info    `json:"info"`
	Entries []Entry `json:"entries"`
	ACP     string  `json:"acp"`
	CanLoad bool    `json:"canLoad"`
	// Proc is the process of the keeper and Offset where its output was read up to.
	Proc   string `json:"proc,omitempty"`
	Offset int64  `json:"offset"`
	NextID int64  `json:"nextId"`
	// Prompt is the id of the session/prompt awaited (0: no turn running).
	Prompt int64 `json:"prompt,omitempty"`
}

type message struct {
	ID     json.RawMessage `json:"id,omitempty"`
	Method string          `json:"method,omitempty"`
	Params json.RawMessage `json:"params,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type response struct {
	result json.RawMessage
	err    error
}

// Session is a conversation with an agent, run by its own process.
type Session struct {
	m *Manager
	q *queue

	mu      sync.Mutex
	info    Info
	th      thread
	acp     string
	canLoad bool
	procID  string
	offset  int64
	nextID  int64
	prompt  int64
	proc    proc
	// gen numbers the processes: the events of an older one are dropped.
	gen     int
	pending map[int64]chan response
	// loading: the replay of session/load is dropped (the thread is already here).
	loading bool
	exits   []time.Time
	closed  bool
	// released: the pod stops, the process is left to the next one.
	released bool
}

func (s *Session) snapshot() saved {
	return saved{Info: s.info, Entries: s.th.Entries, ACP: s.acp, CanLoad: s.canLoad, Proc: s.procID, Offset: s.offset, NextID: s.nextID, Prompt: s.prompt}
}

// ---------- events to the page (under s.mu) ----------

func (s *Session) changed() {
	s.info.Seq++
	s.m.emit("agents.changed", s.info)
	s.m.save(s)
}

func (s *Session) entry(i int) {
	s.info.Seq++
	s.m.emit("agents.entry", map[string]any{"id": s.info.ID, "seq": s.info.Seq, "index": i, "entry": s.th.Entries[i]})
	s.m.save(s)
}

func (s *Session) appended(i int, text string) {
	s.info.Seq++
	s.m.emit("agents.append", map[string]any{"id": s.info.ID, "seq": s.info.Seq, "index": i, "text": text})
	s.m.save(s)
}

func (s *Session) notice(kind, text string) {
	s.entry(s.th.add(Entry{Kind: kind, Text: text}))
}

func (s *Session) setState(state, errText string) {
	if s.info.State != state || s.info.Error != errText {
		s.info.State, s.info.Error = state, errText
		s.changed()
	}
}

// ---------- JSON-RPC ----------

func (s *Session) write(m any) error {
	data, err := json.Marshal(m)
	if err != nil {
		return err
	}
	if s.proc == nil {
		return i18n.New("the agent is not running")
	}
	return s.proc.Write(append(data, '\n'))
}

func (s *Session) request(id int64, method string, params any) error {
	return s.write(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params})
}

// call sends a request and waits for its answer (not on the queue, which delivers it).
func (s *Session) call(method string, params any, timeout time.Duration) (json.RawMessage, error) {
	s.mu.Lock()
	s.nextID++
	id := s.nextID
	ch := make(chan response, 1)
	s.pending[id] = ch
	err := s.request(id, method, params)
	s.mu.Unlock()
	if err != nil {
		return nil, err
	}
	select {
	case r, ok := <-ch:
		if !ok {
			return nil, i18n.New("the agent stopped")
		}
		return r.result, r.err
	case <-time.After(timeout):
		s.mu.Lock()
		delete(s.pending, id)
		s.mu.Unlock()
		return nil, i18n.Errorf("%s: no answer", method)
	}
}

func (s *Session) reply(id json.RawMessage, result any) error {
	return s.write(map[string]any{"jsonrpc": "2.0", "id": id, "result": result})
}

func parseID(raw json.RawMessage) (int64, bool) {
	n, err := strconv.ParseInt(string(raw), 10, 64)
	return n, err == nil
}

// ---------- process ----------

// start runs the agent and opens its session: session/load when it existed (a restart),
// else session/new. Not on the queue.
func (s *Session) start(load bool) {
	argv, err := s.m.command(s.info.Agent)
	if err != nil {
		s.mu.Lock()
		s.setState(Failed, err.Error())
		s.mu.Unlock()
		return
	}
	s.mu.Lock()
	if s.closed || s.released {
		s.mu.Unlock()
		return
	}
	s.setState(Starting, "")
	s.gen++
	gen := s.gen
	p, procID, err := s.m.spawn(s, argv, gen)
	if err != nil {
		s.setState(Failed, err.Error())
		s.mu.Unlock()
		return
	}
	s.proc, s.procID, s.offset = p, procID, 0
	s.mu.Unlock()

	if err := s.open(load); err != nil {
		s.mu.Lock()
		// A process that ended is restarted by exit.
		if s.gen == gen && !s.closed && s.proc != nil {
			s.setState(Failed, err.Error())
		}
		s.mu.Unlock()
	}
}

func (s *Session) open(load bool) error {
	res, err := s.call("initialize", map[string]any{
		"protocolVersion":    1,
		"clientCapabilities": map[string]any{"fs": map[string]bool{"readTextFile": false, "writeTextFile": false}, "terminal": false},
		"clientInfo":         map[string]string{"name": "web-ide", "title": "Web IDE", "version": s.m.Version},
	}, 2*time.Minute)
	if err != nil {
		return err
	}
	var init struct {
		AgentCapabilities struct {
			LoadSession bool `json:"loadSession"`
		} `json:"agentCapabilities"`
	}
	_ = json.Unmarshal(res, &init)
	s.mu.Lock()
	s.canLoad = init.AgentCapabilities.LoadSession
	acpID, cwd := s.acp, s.info.Cwd
	s.mu.Unlock()

	var opened struct {
		SessionID     string         `json:"sessionId"`
		ConfigOptions []ConfigOption `json:"configOptions"`
	}
	lost := false
	if load && acpID != "" && init.AgentCapabilities.LoadSession {
		s.mu.Lock()
		s.loading = true
		s.mu.Unlock()
		res, err = s.call("session/load", map[string]any{"sessionId": acpID, "cwd": cwd, "mcpServers": []any{}}, 2*time.Minute)
		s.mu.Lock()
		s.loading = false
		s.mu.Unlock()
		if err == nil {
			_ = json.Unmarshal(res, &opened)
			opened.SessionID = acpID
		} else {
			log.Printf("acp: %s: session/load failed: %v", s.info.ID, err)
		}
	}
	if opened.SessionID == "" {
		lost = load && acpID != ""
		res, err = s.call("session/new", map[string]any{"cwd": cwd, "mcpServers": []any{}}, 2*time.Minute)
		if err != nil {
			return err
		}
		if err := json.Unmarshal(res, &opened); err != nil || opened.SessionID == "" {
			return i18n.New("the agent did not open a session")
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.acp = opened.SessionID
	if opened.ConfigOptions != nil {
		s.info.Config = opened.ConfigOptions
	}
	if lost {
		s.notice("notice", "The agent could not load the session again: it starts a new one, without the context above.")
	}
	s.info.State, s.info.Error, s.info.Crashed = Ready, "", false
	s.changed()
	return nil
}

// line handles a line of the agent (on the queue).
func (s *Session) line(gen int, data []byte, next int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if gen != s.gen || s.closed {
		return
	}
	s.offset = next
	var m message
	if json.Unmarshal(data, &m) != nil {
		return // not JSON-RPC: noise of the adapter
	}
	switch {
	case m.Method == "" && m.ID != nil:
		id, _ := parseID(m.ID)
		var err error
		if m.Error != nil {
			err = errors.New(m.Error.Message)
		}
		if id != 0 && id == s.prompt {
			s.endTurn(m.Result, err)
		} else if ch, ok := s.pending[id]; ok {
			delete(s.pending, id)
			ch <- response{m.Result, err}
		}
	case m.Method != "" && m.ID != nil:
		s.agentRequest(m)
	case m.Method == "session/update":
		if !s.loading {
			var p struct {
				Update json.RawMessage `json:"update"`
			}
			if json.Unmarshal(m.Params, &p) == nil {
				s.apply(p.Update)
			}
		}
	}
	s.m.save(s)
}

func (s *Session) agentRequest(m message) {
	switch m.Method {
	case "session/request_permission":
		var p struct {
			ToolCall wireToolCall `json:"toolCall"`
			Options  []Option     `json:"options"`
		}
		_ = json.Unmarshal(m.Params, &p)
		if p.ToolCall.ToolCallID != "" {
			s.entry(s.th.applyTool(p.ToolCall))
		}
		title := ""
		if p.ToolCall.Title != nil {
			title = *p.ToolCall.Title
		}
		s.entry(s.th.add(Entry{Kind: "permission", ToolID: p.ToolCall.ToolCallID, Title: title, Request: m.ID, Options: p.Options}))
		s.setState(Asking, "")
	default:
		_ = s.write(map[string]any{"jsonrpc": "2.0", "id": m.ID, "error": rpcError{Code: -32601, Message: "method not found: " + m.Method}})
	}
}

func (s *Session) apply(raw json.RawMessage) {
	var u update
	if json.Unmarshal(raw, &u) != nil {
		return
	}
	switch u.SessionUpdate {
	case "agent_message_chunk", "agent_thought_chunk":
		var b contentBlock
		if json.Unmarshal(u.Content, &b) != nil || b.text() == "" {
			return
		}
		kind := "agent"
		if u.SessionUpdate == "agent_thought_chunk" {
			kind = "thought"
		}
		if i, isNew := s.th.chunk(kind, b.text()); isNew {
			s.entry(i)
		} else {
			s.appended(i, b.text())
		}
	case "tool_call", "tool_call_update":
		var w wireToolCall
		if json.Unmarshal(raw, &w) == nil && w.ToolCallID != "" {
			s.entry(s.th.applyTool(w))
		}
	case "plan":
		s.entry(s.th.setPlan(u.Entries))
	case "available_commands_update":
		s.info.Commands = u.AvailableCommands
		s.changed()
	case "current_mode_update":
		for i := range s.info.Config {
			if s.info.Config[i].Category == "mode" {
				s.info.Config[i].CurrentValue = u.CurrentModeID
			}
		}
		s.changed()
	case "config_option_update":
		var opts []ConfigOption
		if json.Unmarshal(u.ConfigOptions, &opts) == nil {
			s.info.Config = opts
			s.changed()
		}
	case "usage_update":
		if u.Used != nil && u.Size != nil {
			s.info.Usage = &Usage{Used: *u.Used, Size: *u.Size}
			s.changed()
		}
	case "session_info_update":
		if u.Title != nil && *u.Title != "" {
			s.info.Title = *u.Title
			s.changed()
		}
	}
}

// endTurn closes the turn running with the answer of its session/prompt.
func (s *Session) endTurn(result json.RawMessage, err error) {
	s.prompt = 0
	s.cancelPermissions()
	var r struct {
		StopReason string `json:"stopReason"`
	}
	_ = json.Unmarshal(result, &r)
	switch {
	case err != nil:
		s.notice("error", err.Error())
	case r.StopReason == "cancelled":
		s.notice("notice", "Stopped.")
	case r.StopReason == "max_tokens", r.StopReason == "max_turn_requests":
		s.notice("notice", "The agent stopped: limit reached.")
	case r.StopReason == "refusal":
		s.notice("notice", "The agent refused to go on.")
	}
	s.setState(Ready, "")
}

// cancelPermissions answers the questions still open as cancelled.
func (s *Session) cancelPermissions() {
	for i := range s.th.Entries {
		e := &s.th.Entries[i]
		if e.Kind == "permission" && e.Outcome == "" {
			e.Outcome = "cancelled"
			_ = s.reply(e.Request, map[string]any{"outcome": map[string]string{"outcome": "cancelled"}})
			s.entry(i)
		}
	}
}

// exit handles the end of the process (on the queue): restarted with its session, unless it
// keeps dying.
func (s *Session) exit(gen, code int) {
	s.mu.Lock()
	if gen != s.gen || s.closed || s.released {
		s.mu.Unlock()
		return
	}
	s.proc = nil
	for id, ch := range s.pending {
		close(ch)
		delete(s.pending, id)
	}
	if s.prompt != 0 {
		s.prompt = 0
		s.notice("notice", "The agent stopped during the turn.")
	}
	for i := range s.th.Entries {
		if e := &s.th.Entries[i]; e.Kind == "permission" && e.Outcome == "" {
			e.Outcome = "cancelled"
			s.entry(i)
		}
	}
	now := time.Now()
	recent := s.exits[:0]
	for _, t := range s.exits {
		if now.Sub(t) < exitWindow {
			recent = append(recent, t)
		}
	}
	s.exits = append(recent, now)
	if len(s.exits) >= maxExits {
		s.info.Crashed = true
		s.setState(Failed, fmt.Sprintf("exit code %d\n%s", code, s.m.stderrTail(s.info.ID)))
		s.mu.Unlock()
		return
	}
	s.notice("notice", "The agent ended: it is restarted and its session loaded.")
	s.mu.Unlock()
	go s.start(true)
}

// ---------- actions of the page ----------

func (s *Session) Prompt(text string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch s.info.State {
	case Busy, Asking:
		return i18n.New("the agent is working: wait or stop it")
	case Ready:
	default:
		return i18n.New("the agent is not ready")
	}
	s.nextID++
	id := s.nextID
	if err := s.request(id, "session/prompt", map[string]any{"sessionId": s.acp, "prompt": []map[string]string{{"type": "text", "text": text}}}); err != nil {
		return err
	}
	s.prompt = id
	s.th.plan = -1
	s.entry(s.th.add(Entry{Kind: "user", Text: text}))
	s.setState(Busy, "")
	return nil
}

func (s *Session) Cancel() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.prompt == 0 {
		return nil
	}
	s.cancelPermissions()
	return s.write(map[string]any{"jsonrpc": "2.0", "method": "session/cancel", "params": map[string]string{"sessionId": s.acp}})
}

// Answer gives the option chosen for the permission of entry index.
func (s *Session) Answer(index int, option string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if index < 0 || index >= len(s.th.Entries) || s.th.Entries[index].Kind != "permission" || s.th.Entries[index].Outcome != "" {
		return i18n.New("this question is no longer open")
	}
	e := &s.th.Entries[index]
	if err := s.reply(e.Request, map[string]any{"outcome": map[string]string{"outcome": "selected", "optionId": option}}); err != nil {
		return err
	}
	e.Outcome = option
	s.entry(index)
	open := false
	for _, x := range s.th.Entries {
		open = open || (x.Kind == "permission" && x.Outcome == "")
	}
	if !open && s.info.State == Asking {
		s.setState(Busy, "")
	}
	return nil
}

func (s *Session) SetConfig(id, value string) error {
	s.mu.Lock()
	acpID := s.acp
	s.mu.Unlock()
	res, err := s.call("session/set_config_option", map[string]string{"sessionId": acpID, "configId": id, "value": value}, 30*time.Second)
	if err != nil {
		return err
	}
	var r struct {
		ConfigOptions []ConfigOption `json:"configOptions"`
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if json.Unmarshal(res, &r) == nil && r.ConfigOptions != nil {
		s.info.Config = r.ConfigOptions
	} else {
		for i := range s.info.Config {
			if s.info.Config[i].ID == id {
				s.info.Config[i].CurrentValue = value
			}
		}
	}
	s.changed()
	return nil
}

// Restart starts again an agent stopped by its crashes.
func (s *Session) Restart() error {
	s.mu.Lock()
	if s.info.State != Failed {
		s.mu.Unlock()
		return nil
	}
	s.exits = nil
	if s.proc != nil {
		s.proc.Kill()
		s.proc = nil
	}
	s.mu.Unlock()
	go s.start(true)
	return nil
}

// Thread returns the info and the entries of the session.
func (s *Session) Thread() (Info, []Entry) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.info, append([]Entry{}, s.th.Entries...)
}

func (s *Session) Info() Info {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.info
}

func removeQuiet(path string) {
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		log.Printf("acp: %v", err)
	}
}
