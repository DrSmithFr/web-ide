package docker

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"antennae.space/ide/lib/i18n"
	dk "antennae.space/ide/plugins/docker/internal/docker"
	"antennae.space/ide/pod/assistant"
	"antennae.space/ide/pod/plugins"
)

// Docker tools of the assistant and the MCP, read-only in every mode: state of the Compose
// services of the project and the last lines of the logs of a service or container.

var profilesProp = map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Compose profiles to include (optional)"}

func str(description string) map[string]any {
	return map[string]any{"type": "string", "description": description}
}

// toolArgs are the arguments of a call, with the profiles active in the conversation (its
// options) when the call names none.
type toolArgs struct {
	Service, Container, Filter string
	Lines                      int
	Profiles                   []string
}

func args(c plugins.Call) (toolArgs, error) {
	var a toolArgs
	if len(c.Params) > 0 {
		if err := json.Unmarshal(c.Params, &a); err != nil {
			return a, plugins.UsageError{Msg: fmt.Sprintf("invalid JSON arguments: %.200s", c.Params)}
		}
	}
	if a.Profiles == nil && len(c.Options) > 0 {
		var o struct{ Profiles []string }
		_ = json.Unmarshal(c.Options, &o)
		a.Profiles = o.Profiles
	}
	return a, nil
}

func registerTools(h plugins.Host) {
	h.AssistantTool(plugins.AssistantTool{
		Name:        "docker_ps",
		Description: "State of the Docker Compose services of the project (Compose file at the project root): container, state, health, image, published ports. Without Compose file, lists every container of the host.",
		Props:       map[string]any{"profiles": profilesProp},
		Run: func(ctx context.Context, c plugins.Call) (plugins.Result, error) {
			a, err := args(c)
			if err != nil {
				return plugins.Result{}, err
			}
			return ps(ctx, c, a)
		},
	})
	h.AssistantTool(plugins.AssistantTool{
		Name:        "docker_logs",
		Description: "Last lines of the logs of a Compose service of the project, or of any container (name or id), stdout and stderr together, with timestamps.",
		Props: map[string]any{"service": str("Compose service of the project"), "container": str("Container name or id (instead of service)"),
			"lines": map[string]any{"type": "integer", "description": "Number of lines (200 by default, 2000 max)"}, "filter": str("Keep only the lines containing this text (case-insensitive)"), "profiles": profilesProp},
		Run: func(ctx context.Context, c plugins.Call) (plugins.Result, error) {
			a, err := args(c)
			if err != nil {
				return plugins.Result{}, err
			}
			return logs(ctx, c, a)
		},
	})
}

func ps(ctx context.Context, c plugins.Call, a toolArgs) (plugins.Result, error) {
	rt := c.Runtime
	st := dockerOf(rt).Status(ctx)
	if !st.Available {
		msg := st.Error
		if st.Err != nil {
			msg = i18n.Translate("en", st.Err)
		}
		return plugins.Result{Content: "Docker is not reachable: " + msg, Summary: assistant.Plain(msg), Error: true}, nil
	}
	if st.ComposeFile == "" || !st.Compose {
		list, err := dockerOf(rt).Containers(ctx)
		if err != nil {
			return plugins.Result{}, err
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
		return plugins.Result{Content: why + ". Containers of the host:\n" + text, Summary: assistant.Tn(len(list), "{n} container", "{n} containers", nil).Raw()}, nil
	}
	var active []string
	for _, p := range a.Profiles {
		for _, d := range st.Profiles {
			if p == d {
				active = append(active, p)
			}
		}
	}
	stack, err := dockerOf(rt).Stack(ctx, active)
	if err != nil {
		return plugins.Result{}, err
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
		act := strings.Join(a.Profiles, ", ")
		if act == "" {
			act = "none"
		}
		lines = append(lines, fmt.Sprintf("Profiles declared: %s; active: %s.", strings.Join(st.Profiles, ", "), act))
	}
	return plugins.Result{Content: strings.Join(lines, "\n"), Summary: assistant.T("{n}/{total} running", map[string]any{"n": running, "total": len(stack.Services)}).Raw()}, nil
}

func logs(ctx context.Context, c plugins.Call, a toolArgs) (plugins.Result, error) {
	service, container := strings.TrimSpace(a.Service), strings.TrimSpace(a.Container)
	if service == "" && container == "" {
		return plugins.Result{}, plugins.UsageError{Msg: "service or container is missing"}
	}
	lines := a.Lines
	if lines <= 0 {
		lines = 200
	}
	lines = min(lines, 2000)
	if container != "" {
		service = ""
	}
	out, err := dockerOf(c.Runtime).LogsTail(ctx, container, service, a.Profiles, lines)
	if err != nil {
		return plugins.Result{}, err
	}
	rows := strings.Split(assistant.TrimEnd(assistant.PlainOutput(out)), "\n")
	if filter := strings.ToLower(a.Filter); filter != "" {
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
	if a.Filter != "" {
		f = fmt.Sprintf(", filtered on \"%s\"", a.Filter)
	}
	return plugins.Result{Content: fmt.Sprintf("Logs of %s (last %d lines%s):\n%s", what, lines, f, text), Summary: assistant.Tn(len(rows), "{n} line", "{n} lines", nil).With(what+" · ", "").Raw()}, nil
}

func portLabel(p dk.Port) string {
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

func containerRow(c dk.Container) string {
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
