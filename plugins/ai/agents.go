package ai

import (
	"net"
	"strconv"
	"strings"
	"sync"

	"github.com/DrSmithFr/web-ide/lib/i18n"
	"github.com/DrSmithFr/web-ide/plugins/ai/acp"
	"github.com/DrSmithFr/web-ide/plugins/ai/engine"
	kb "github.com/DrSmithFr/web-ide/plugins/kanban/kanban"
	"github.com/DrSmithFr/web-ide/pod/caps"
	"github.com/DrSmithFr/web-ide/pod/plugins"
	"github.com/DrSmithFr/web-ide/pod/projects"
	"github.com/DrSmithFr/web-ide/pod/runtime"
)

// The sessions of the Agents tool of a project are a part of its runtime (pod/runtime parts):
// made when a window opens the project, or on first use, and ended with it. The plugin keeps
// the open ones, for the Questions tool.

// agentsPart is the part of the runtime of a local project: the manager of its sessions.
type agentsPart struct {
	a       *A
	project string
	m       *acp.Manager
}

func (p *agentsPart) Stop(release bool) {
	if release {
		p.m.Release()
	} else {
		p.m.CloseAll()
	}
	p.a.agentParts.drop(p.project, p)
}

// agentParts are the parts of the open projects, by project.
type agentParts struct {
	mu   sync.Mutex
	open map[string]*agentsPart
}

func (r *agentParts) add(p *agentsPart) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.open[p.project] = p
}

func (r *agentParts) drop(project string, p *agentsPart) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.open[project] == p {
		delete(r.open, project)
	}
}

func (r *agentParts) list() []*agentsPart {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]*agentsPart, 0, len(r.open))
	for _, p := range r.open {
		out = append(out, p)
	}
	return out
}

// agentsOf is the manager of the sessions of the project of rt (nil on an SSH project: local
// only for now).
func (s *A) agentsOf(rt *runtime.Runtime) *acp.Manager {
	if !rt.Local || s.Store == nil {
		return nil
	}
	return rt.Part("agents", func() any {
		p := &agentsPart{a: s, project: rt.P.ID}
		p.m = acp.NewManager(acp.Deps{Keeper: s.Keeper, Owner: rt.P.ID, Dir: s.Store.Path("agents", rt.P.ID), Root: rt.Root,
			Catalog: acp.Catalog{Dir: s.Store.Path("acp")}, Version: s.Version, MCP: s.agentsMCP(), Emit: rt.Emit})
		s.agentParts.add(p)
		return p
	}).(*agentsPart).m
}

// agentsMCP is the MCP server of the IDE as the sessions of the Agents tool reach it: the
// listen address of the pod, the token as a bearer. Named like the server users add to Claude
// Code (web-ide), so that the session's one replaces it instead of doubling its tools.
func (s *A) agentsMCP() []acp.MCPServer {
	svc := s.pod.Services() // read again: the address may be set after the registration
	base := svc.MCPBase
	if base == "" && svc.Cfg != nil && svc.Cfg.Addr != "" {
		host, port, err := net.SplitHostPort(svc.Cfg.Addr)
		if err != nil {
			return nil
		}
		if host == "" || host == "0.0.0.0" || host == "::" {
			host = "127.0.0.1"
		}
		base = "http://" + net.JoinHostPort(host, port)
	}
	if base == "" {
		return nil
	}
	return []acp.MCPServer{{Type: "http", Name: "web-ide", URL: strings.TrimSuffix(base, "/") + plugins.MCPPath,
		Headers: []acp.Header{{Name: "Authorization", Value: "Bearer " + svc.Token}}}}
}

// rootOf is the main project of a project (a worktree's parent), whose windows show its
// questions.
func (s *A) rootOf(project string) string {
	if p, err := s.Projects.Root(project); err == nil {
		return p.ID
	}
	return project
}

// agentQuestions are the questions of the sessions of the open projects of root: a permission
// or a form.
func (s *A) agentQuestions(root string) []plugins.Question {
	var out []plugins.Question
	for _, p := range s.agentParts.list() {
		if s.rootOf(p.project) != root {
			continue
		}
		for _, a := range p.m.Questions() {
			q := plugins.Question{ID: "acp:" + p.project + ":" + a.Session + ":" + strconv.Itoa(a.Index), Source: "acp", Project: p.project, Session: a.Session, Index: a.Index,
				Name: a.Name, Title: a.Title, Last: a.Last, Time: a.Entry.Time, AutoOpen: a.AutoOpen}
			if a.Entry.Kind == "permission" {
				q.Kind, q.Text = "permission", a.Entry.Title
				for _, o := range a.Entry.Options {
					q.Options = append(q.Options, plugins.Option{ID: o.ID, Name: o.Name, Kind: o.Kind})
				}
			} else {
				q.Kind, q.Text, q.Schema = "form", a.Entry.Text, a.Entry.Schema
			}
			out = append(out, q)
		}
	}
	return out
}

// answerAgent answers a question of a session (id acp:<project>:<session>:<index>).
func (s *A) answerAgent(root, id string, a plugins.Answer) error {
	parts := strings.Split(id, ":")
	if len(parts) != 4 || s.rootOf(parts[1]) != root {
		return i18n.New("this question is no longer open")
	}
	rt := s.pod.OpenRuntime(parts[1])
	if rt == nil {
		return i18n.New("this question is no longer open")
	}
	m := s.agentsOf(rt)
	if m == nil {
		return i18n.New("this question is no longer open")
	}
	sess, err := m.Get(parts[2])
	if err != nil {
		return err
	}
	index, _ := strconv.Atoi(parts[3])
	if a.Option != "" {
		return sess.Answer(index, a.Option)
	}
	return sess.AnswerForm(index, a.Action, a.Content)
}

// agentsCap is the agents capability, as the Kanban uses it (pod/caps).
type agentsCap struct{ s *A }

func (c agentsCap) Develop(project, agent string, t caps.TicketBrief) (caps.AgentSession, bool, error) {
	s := c.s
	if _, ok := engine.RoleInstructions[t.Role]; !ok {
		return caps.AgentSession{}, false, i18n.Errorf("unknown role: %s", t.Role)
	}
	rt, err := s.pod.Runtime(project)
	if err != nil {
		return caps.AgentSession{}, false, err
	}
	m := s.agentsOf(rt)
	if m == nil {
		return caps.AgentSession{}, false, i18n.New("agents run on local projects only")
	}
	prompt := engine.TicketPrompt(t.ID, t.Role, t.Branch, t.Parent, t.Feedback, t.Markdown)
	if t.Text != "" {
		prompt += "\n\n---\n\n" + t.Text
	}
	for _, info := range m.List() {
		if info.Ticket != t.ID {
			continue
		}
		sess, err := m.Get(info.ID)
		if err != nil {
			break
		}
		if err := sess.Prompt(prompt); err != nil {
			return caps.AgentSession{}, false, err
		}
		return sessionOf(info), false, nil
	}
	info, err := m.CreateWith(agent, "", acp.Options{Title: "#" + strconv.FormatInt(t.ID, 10) + " " + t.Title, Ticket: t.ID, Prompt: prompt})
	if err != nil {
		return caps.AgentSession{}, false, err
	}
	s.pod.Emit(project, "agents.created", info)
	return sessionOf(info), true, nil
}

func (c agentsCap) TicketSession(project string, ticket int64) (caps.AgentSession, bool) {
	info, _, ok := acp.Find(c.s.Store.Path("agents", project), ticket)
	return sessionOf(info), ok
}

func (c agentsCap) Purge(project string) {
	acp.Purge(c.s.Keeper, project, c.s.Store.Path("agents", project))
}

func (c agentsCap) ChatTimes(project string) (map[string]caps.ChatTime, error) {
	loc, _, err := c.s.ChatLoc(project)
	if err != nil {
		return nil, err
	}
	times, err := c.s.LLM.ChatTimes(loc)
	if err != nil {
		return nil, err
	}
	out := make(map[string]caps.ChatTime, len(times))
	for id, t := range times {
		out[id] = caps.ChatTime{Parent: t.Parent, Ms: t.Ms, ThinkMs: t.ThinkMs}
	}
	return out, nil
}

// ticketSession finds the session of a ticket in the files of the Agents tool of its worktree
// (the one of the root of its lineage), open or not.
func (s *A) ticketSession(root string, t *kb.Ticket) (acp.Info, []acp.Entry, bool) {
	owner := t.ID
	if t.Parent != 0 {
		owner = t.Parent
	}
	return acp.Find(s.Store.Path("agents", projects.ChildID(root, owner)), t.ID)
}

func sessionOf(info acp.Info) caps.AgentSession {
	return caps.AgentSession{ID: info.ID, Name: info.Name, Title: info.Title, State: info.State}
}
