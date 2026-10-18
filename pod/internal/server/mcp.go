package server

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"io"
	"net/http"
	"strings"
)

// MCP endpoint: Claude Code (or any MCP client) reads and changes the tickets of the
// kanban, with the rules of the model and the author "claude" (docs/design/kanban.md). Streamable
// HTTP transport without streaming: each POST carries JSON-RPC messages and gets its
// answers as JSON. Authenticated by the token of the pod as a bearer.

const mcpPath = "/mcp"

// mcpProtocol is answered when the client asks for a version this server does not know.
const mcpProtocol = "2025-06-18"

var mcpProtocols = map[string]bool{"2024-11-05": true, "2025-03-26": true, "2025-06-18": true}

const mcpInstructions = `Kanban of the Web IDE of the user: tickets go New → To do (plan written) → In progress (branch and worktree) → To test → Done.
Milestones (kanban_milestone) group the tickets of one feature with its spec and user stories; only the user validates them. Objectives (kanban_objective) are named deadlines in order (MVP, a launch), taking tickets of several milestones; a ticket has one at most, apart from its milestone.
Pass cwd (your working directory) to every tool: it selects the project; in the worktree of a ticket, that ticket is the default id.
The saved commands of the project (list_commands, run_saved_command) run in consoles of the IDE that the user sees: use them for a development server or a command the user set up.
The IDE itself: its consoles (list_consoles, read_console, console_input), Docker (docker_ps, docker_logs), app previews to try on any device (share_preview, list_previews), and what shows in its window (open_file, open_diff, focus, show_message, request_doodle for a drawing of the user).
When a choice is the user's, ask_user puts your questions in the Questions tool of the IDE and returns the answers.
Your changes are shown as written by Claude. Keep descriptions under 1500 characters and notes under 1000: notes are for decisions, not reports.
When you mention a file of the project to the user, write it as a Markdown link to the IDE: [path:line](` + "<ide>" + `/open?path=<absolute path>&line=<line>), so that a click opens it in the IDE.`

// mcpLocal is added to the instructions when the public address is not the local one: the user
// may click from anywhere (the relay) or on the machine of the pod (no relay needed).
const mcpLocal = `
The IDE has two addresses: ` + "<ide>" + ` from anywhere, ` + "<local>" + ` on this machine. Give both for a link to it: [path:line](` + "<ide>" + `/open?path=<absolute path>&line=<line>) ([local](` + "<local>" + `/open?path=<absolute path>&line=<line>)), and the same for the pages of the IDE the tools give.`

type mcpMessage struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type mcpError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type mcpReply struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *mcpError       `json:"error,omitempty"`
}

// bearer tells whether the request carries the token of the pod.
func (s *Server) bearer(r *http.Request) bool {
	tok, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	return ok && subtle.ConstantTimeCompare([]byte(strings.TrimSpace(tok)), []byte(s.Token)) == 1
}

func (s *Server) serveMCP(w http.ResponseWriter, r *http.Request) {
	if !s.bearer(r) {
		w.Header().Set("WWW-Authenticate", `Bearer realm="web-ide"`)
		http.Error(w, "the token of the pod is needed as a bearer (~/.web-ide/token)", http.StatusUnauthorized)
		return
	}
	if r.Method != http.MethodPost {
		// No stream of server messages: nothing is ever pushed to the client.
		w.Header().Set("Allow", "POST")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 32<<20))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	trimmed := strings.TrimSpace(string(body))
	var msgs []mcpMessage
	batch := strings.HasPrefix(trimmed, "[")
	if batch {
		err = json.Unmarshal(body, &msgs)
	} else {
		var m mcpMessage
		err = json.Unmarshal(body, &m)
		msgs = []mcpMessage{m}
	}
	if err != nil {
		writeJSON(w, mcpReply{JSONRPC: "2.0", ID: json.RawMessage("null"), Error: &mcpError{-32700, "parse error"}})
		return
	}
	var replies []mcpReply
	for _, m := range msgs {
		if len(m.ID) == 0 || m.Method == "" {
			continue // a notification, or an answer to a request this server never sends
		}
		replies = append(replies, s.mcpCall(r.Context(), m))
	}
	switch {
	case len(replies) == 0:
		w.WriteHeader(http.StatusAccepted)
	case batch:
		writeJSON(w, replies)
	default:
		writeJSON(w, replies[0])
	}
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func (s *Server) mcpCall(ctx context.Context, m mcpMessage) mcpReply {
	reply := mcpReply{JSONRPC: "2.0", ID: m.ID}
	fail := func(code int, msg string) mcpReply {
		reply.Error = &mcpError{code, msg}
		return reply
	}
	switch m.Method {
	case "initialize":
		a, _ := bind[struct {
			ProtocolVersion string `json:"protocolVersion"`
		}](m.Params)
		version := a.ProtocolVersion
		if !mcpProtocols[version] {
			version = mcpProtocol
		}
		reply.Result = map[string]any{
			"protocolVersion": version,
			"capabilities":    map[string]any{"tools": map[string]any{}, "prompts": map[string]any{}},
			"serverInfo":      map[string]any{"name": "web-ide", "title": "Web IDE", "version": s.Version},
			"instructions":    s.mcpInstructions(),
		}
	case "ping":
		reply.Result = map[string]any{}
	case "tools/list":
		tools := make([]map[string]any, 0, len(mcpTools))
		for _, t := range mcpTools {
			tools = append(tools, map[string]any{"name": t.name, "description": t.description, "inputSchema": t.schema()})
		}
		reply.Result = map[string]any{"tools": tools}
	case "tools/call":
		a, err := bind[struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}](m.Params)
		if err != nil {
			return fail(-32602, err.Error())
		}
		t := findMCPTool(a.Name)
		if t == nil {
			return fail(-32602, "unknown tool: "+a.Name)
		}
		var content []any
		if t.rich != nil {
			content, err = t.rich(ctx, s, a.Arguments)
		} else {
			var text string
			text, err = t.run(ctx, s, a.Arguments)
			content = []any{textContent(text)}
		}
		if err != nil {
			// Tool errors go back to the model, which can correct its call.
			reply.Result = map[string]any{"content": []any{textContent("Error: " + err.Error())}, "isError": true}
			return reply
		}
		reply.Result = map[string]any{"content": content}
	case "prompts/list":
		reply.Result = map[string]any{"prompts": mcpPromptList()}
	case "prompts/get":
		a, err := bind[struct {
			Name      string            `json:"name"`
			Arguments map[string]string `json:"arguments"`
		}](m.Params)
		if err != nil {
			return fail(-32602, err.Error())
		}
		res, err := mcpPrompt(a.Name, a.Arguments)
		if err != nil {
			return fail(-32602, err.Error())
		}
		reply.Result = res
	default:
		return fail(-32601, "method not found: "+m.Method)
	}
	return reply
}

// mcpInstructions names the addresses of the IDE: the public one, and the local one beside it.
func (s *Server) mcpInstructions() string {
	text := mcpInstructions
	if pub, local := s.publicURL(), s.localURL(); pub != local {
		text += strings.ReplaceAll(mcpLocal, "<local>", local)
	}
	return strings.ReplaceAll(text, "<ide>", s.publicURL())
}

// publicURL is the address the user opens the IDE at from anywhere: the configured one, else the
// page of this pod on the origin of its relay, else the local one.
func (s *Server) publicURL() string {
	if s.Cfg.PublicURL != "" {
		return strings.TrimSuffix(s.Cfg.PublicURL, "/")
	}
	if u := s.relayPage(); u != "" {
		return u
	}
	return s.localURL()
}

// relayPage is the path of this pod on the origin of its relay (https://<relay>/pod/<device key>),
// for a paired pod: a link there opens the bootstrap page, which shows it in its frame.
func (s *Server) relayPage() string {
	if s.RelayStatus == nil || s.Pods == nil {
		return ""
	}
	key, u := s.Pods.Own(), s.RelayStatus().URL
	if key == "" || u == "" {
		return ""
	}
	if rest, ok := strings.CutPrefix(u, "wss://"); ok {
		u = "https://" + rest
	} else if rest, ok := strings.CutPrefix(u, "ws://"); ok {
		u = "http://" + rest
	}
	return strings.TrimSuffix(u, "/") + "/pod/" + key
}

// localURL is the address of the pod as a client of this machine reaches it.
func (s *Server) localURL() string {
	addr := s.Cfg.Addr
	if strings.HasPrefix(addr, ":") || strings.HasPrefix(addr, "0.0.0.0:") {
		addr = "127.0.0.1:" + addr[strings.LastIndex(addr, ":")+1:]
	}
	return "http://" + addr
}
