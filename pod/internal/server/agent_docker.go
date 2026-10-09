package server

import (
	"fmt"
	"strings"

	"github.com/DrSmithFr/web-ide/pod/internal/agent"
	"github.com/DrSmithFr/web-ide/pod/internal/docker"
	"github.com/DrSmithFr/web-ide/pod/internal/i18n"
)

// Docker tools of the assistant, read-only in every mode: state of the Compose services of
// the project and the last lines of the logs of a service or container.

func portLabel(p docker.Port) string {
	if p.Published == 0 {
		return fmt.Sprintf("%d/%s", p.Target, p.Protocol)
	}
	ip := ""
	if p.IP != "" {
		ip = p.IP + ":"
	}
	proto := ""
	if p.Protocol != "tcp" {
		proto = "/" + p.Protocol
	}
	return fmt.Sprintf("%s%d→%d%s", ip, p.Published, p.Target, proto)
}

func containerRow(c docker.Container) string {
	var ports []string
	for _, p := range c.Ports {
		if p.Published != 0 {
			ports = append(ports, portLabel(p))
		}
	}
	name := c.Name
	if c.Service != "" {
		name = c.Service + " (" + c.Name + ")"
	}
	state := c.Status
	if state == "" {
		state = c.State
	}
	if c.Health != "" {
		state += ", " + c.Health
	}
	row := "- " + name + ": " + state + " · " + c.Image
	if len(ports) > 0 {
		row += " · ports " + strings.Join(ports, ", ")
	}
	return row
}

func (s *Server) dockerTool(r *agentRun, ref *runtimeRef, name string, a toolArgs) toolResult {
	rt := ref.rt
	r.mu.Lock()
	profiles := append([]string{}, opt(r.chat).DockerProfiles...)
	r.mu.Unlock()
	if name == "docker_ps" {
		st := rt.Docker.Status(r.ctx)
		if !st.Available {
			msg := st.Error
			if st.Err != nil {
				msg = i18n.Translate("en", st.Err)
			}
			return toolResult{Content: "Error: Docker is not reachable: " + msg, Summary: agent.Plain(msg), Status: "error"}
		}
		if st.ComposeFile == "" || !st.Compose {
			list, err := rt.Docker.Containers(r.ctx)
			if err != nil {
				return fail(r, err)
			}
			why := "No Compose file at the project root"
			if st.ComposeFile != "" {
				why = "Docker Compose is not installed"
			}
			var rows []string
			for _, c := range list {
				rows = append(rows, containerRow(c))
			}
			text := strings.Join(rows, "\n")
			if text == "" {
				text = "(none)"
			}
			return ok(why+". Containers of the host:\n"+text, agent.Tn(len(list), "{n} container", "{n} containers", nil))
		}
		var active []string
		for _, p := range profiles {
			for _, d := range st.Profiles {
				if p == d {
					active = append(active, p)
				}
			}
		}
		stack, err := rt.Docker.Stack(r.ctx, active)
		if err != nil {
			return fail(r, err)
		}
		running := 0
		lines := []string{}
		for _, c := range stack.Containers {
			if c.State == "running" {
				running++
			}
			lines = append(lines, containerRow(c))
		}
		for _, svc := range stack.Services {
			found := false
			for _, c := range stack.Containers {
				found = found || c.Service == svc
			}
			if !found {
				lines = append(lines, "- "+svc+": not created")
			}
		}
		head := fmt.Sprintf("Compose project \"%s\" (%s), %d/%d services running.", stack.Name, st.ComposeFile, running, len(stack.Services))
		lines = append([]string{head}, lines...)
		if len(st.Profiles) > 0 {
			act := strings.Join(profiles, ", ")
			if act == "" {
				act = "none"
			}
			lines = append(lines, fmt.Sprintf("Profiles declared: %s; active: %s.", strings.Join(st.Profiles, ", "), act))
		}
		return ok(strings.Join(lines, "\n"), agent.T("{n}/{total} running", map[string]any{"n": running, "total": len(stack.Services)}))
	}
	service, container := strings.TrimSpace(a.str("service")), strings.TrimSpace(a.str("container"))
	if service == "" && container == "" {
		return fail(r, usagef("service or container is missing"))
	}
	lines := a.num("lines")
	if lines <= 0 {
		lines = 200
	}
	lines = min(lines, 2000)
	if container != "" {
		service = ""
	}
	out, err := rt.Docker.LogsTail(r.ctx, container, service, profiles, lines)
	if err != nil {
		return fail(r, err)
	}
	rows := strings.Split(agent.TrimEnd(agent.PlainOutput(out)), "\n")
	if filter := strings.ToLower(a.str("filter")); filter != "" {
		var kept []string
		for _, l := range rows {
			if strings.Contains(strings.ToLower(l), filter) {
				kept = append(kept, l)
			}
		}
		rows = kept
	}
	what := container
	if what == "" {
		what = service
	}
	text := strings.Join(rows, "\n")
	if text == "" {
		text = "(no output)"
	}
	f := ""
	if a.str("filter") != "" {
		f = fmt.Sprintf(", filtered on \"%s\"", a.str("filter"))
	}
	return ok(fmt.Sprintf("Logs of %s (last %d lines%s):\n%s", what, lines, f, text), agent.Tn(len(rows), "{n} line", "{n} lines", nil).With(what+" · ", ""))
}
