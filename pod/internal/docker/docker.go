// Package docker drives Docker and Docker Compose through the docker command of the project
// target (the local machine or the SSH host). Outputs are read in their JSON formats only.
package docker

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"path"
	"sort"
	"strconv"
	"strings"

	"github.com/DrSmithFr/web-ide/pod/internal/execx"
	"github.com/DrSmithFr/web-ide/pod/internal/i18n"
)

// ComposeFiles are the names Compose looks for, in its order.
var ComposeFiles = []string{"compose.yaml", "compose.yml", "docker-compose.yaml", "docker-compose.yml"}

type Docker struct {
	run  execx.Runner
	root string
	// exists tells whether a file of the project target exists.
	exists func(p string) bool
}

func New(r execx.Runner, root string, exists func(string) bool) *Docker {
	return &Docker{run: r, root: root, exists: exists}
}

// Status tells what the tool can do: docker reachable, Compose plugin, Compose file.
type Status struct {
	Available   bool     `json:"available"`
	Error       string   `json:"error,omitempty"`
	Err         error    `json:"-"` // translated into Error for the window
	Compose     bool     `json:"compose"`
	ComposeFile string   `json:"composeFile,omitempty"`
	Profiles    []string `json:"profiles,omitempty"`
}

func (d *Docker) Status(ctx context.Context) Status {
	var st Status
	for _, f := range ComposeFiles {
		if d.exists(path.Join(d.root, f)) {
			st.ComposeFile = f
			break
		}
	}
	if !d.run.Has("docker") {
		st.Err = i18n.New("the docker command is not installed")
		return st
	}
	if _, err := d.run.Output(ctx, []string{"docker", "version", "--format", "{{.Server.Version}}"}, ""); err != nil {
		st.Err = err
		return st
	}
	st.Available = true
	_, err := d.run.Output(ctx, []string{"docker", "compose", "version", "--short"}, "")
	st.Compose = err == nil
	if st.Compose && st.ComposeFile != "" {
		out, err := d.run.Output(ctx, []string{"docker", "compose", "config", "--profiles"}, d.root)
		if err == nil {
			st.Profiles = lines(out)
		}
	}
	return st
}

func lines(out []byte) []string {
	var l []string
	for _, s := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if s = strings.TrimSpace(s); s != "" {
			l = append(l, s)
		}
	}
	return l
}

// jsonLines decodes the output of a --format json command: one object per line, or a JSON
// array (older Compose versions).
func jsonLines[T any](out []byte) ([]T, error) {
	out = bytes.TrimSpace(out)
	var list []T
	if len(out) == 0 {
		return list, nil
	}
	if out[0] == '[' {
		err := json.Unmarshal(out, &list)
		return list, err
	}
	sc := bufio.NewScanner(bytes.NewReader(out))
	sc.Buffer(make([]byte, 1<<20), 16<<20)
	for sc.Scan() {
		if len(bytes.TrimSpace(sc.Bytes())) == 0 {
			continue
		}
		var v T
		if err := json.Unmarshal(sc.Bytes(), &v); err != nil {
			return nil, err
		}
		list = append(list, v)
	}
	return list, sc.Err()
}

// ---------- containers ----------

type Port struct {
	IP        string `json:"ip,omitempty"`
	Published int    `json:"published,omitempty"`
	Target    int    `json:"target"`
	Protocol  string `json:"protocol"`
}

type Container struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Image   string `json:"image"`
	State   string `json:"state"`  // created, running, paused, restarting, exited, dead
	Status  string `json:"status"` // "Up 2 minutes (healthy)"
	Health  string `json:"health,omitempty"`
	Project string `json:"project,omitempty"`
	Service string `json:"service,omitempty"`
	Ports   []Port `json:"ports"`
}

// Stack is the Compose project of the project root: declared services and their containers.
type Stack struct {
	Name       string      `json:"name"`
	Services   []string    `json:"services"`
	Containers []Container `json:"containers"`
}

func profileArgs(profiles []string) []string {
	var a []string
	for _, p := range profiles {
		a = append(a, "--profile", p)
	}
	return a
}

// ComposeArgv is `docker compose [--profile p]… args…`.
func ComposeArgv(profiles []string, args ...string) []string {
	return append(append([]string{"docker", "compose"}, profileArgs(profiles)...), args...)
}

func (d *Docker) Stack(ctx context.Context, profiles []string) (*Stack, error) {
	out, err := d.run.Output(ctx, ComposeArgv(profiles, "config", "--services"), d.root)
	if err != nil {
		return nil, err
	}
	st := &Stack{Services: lines(out), Containers: []Container{}}
	sort.Strings(st.Services)
	if out, err := d.run.Output(ctx, ComposeArgv(profiles, "config", "--format", "json"), d.root); err == nil {
		var cfg struct{ Name string }
		if json.Unmarshal(out, &cfg) == nil {
			st.Name = cfg.Name
		}
	}
	out, err = d.run.Output(ctx, ComposeArgv(profiles, "ps", "-a", "--format", "json"), d.root)
	if err != nil {
		return nil, err
	}
	raw, err := jsonLines[composePS](out)
	if err != nil {
		return nil, err
	}
	for _, c := range raw {
		st.Containers = append(st.Containers, c.container())
	}
	sort.Slice(st.Containers, func(i, j int) bool { return st.Containers[i].Name < st.Containers[j].Name })
	return st, nil
}

type composePS struct {
	ID         string
	Name       string
	Image      string
	State      string
	Status     string
	Health     string
	Project    string
	Service    string
	Publishers []struct {
		URL           string
		TargetPort    int
		PublishedPort int
		Protocol      string
	}
}

func (c composePS) container() Container {
	out := Container{ID: c.ID, Name: c.Name, Image: c.Image, State: c.State, Status: c.Status, Health: c.Health,
		Project: c.Project, Service: c.Service, Ports: []Port{}}
	seen := map[Port]bool{}
	for _, p := range c.Publishers {
		// IPv4 and IPv6 bindings of one port show once.
		ip := p.URL
		if ip == "::" || ip == "0.0.0.0" {
			ip = ""
		}
		port := Port{IP: ip, Published: p.PublishedPort, Target: p.TargetPort, Protocol: p.Protocol}
		if !seen[port] {
			seen[port] = true
			out.Ports = append(out.Ports, port)
		}
	}
	return out
}

type dockerPS struct {
	ID     string
	Names  string
	Image  string
	State  string
	Status string
	Labels string
	Ports  string
}

// Containers lists every container of the host.
func (d *Docker) Containers(ctx context.Context) ([]Container, error) {
	out, err := d.run.Output(ctx, []string{"docker", "ps", "-a", "--no-trunc", "--format", "json"}, "")
	if err != nil {
		return nil, err
	}
	raw, err := jsonLines[dockerPS](out)
	if err != nil {
		return nil, err
	}
	list := []Container{}
	for _, c := range raw {
		labels := map[string]string{}
		for _, kv := range strings.Split(c.Labels, ",") {
			if k, v, ok := strings.Cut(kv, "="); ok {
				labels[k] = v
			}
		}
		id := c.ID
		if len(id) > 12 {
			id = id[:12]
		}
		list = append(list, Container{ID: id, Name: c.Names, Image: c.Image, State: c.State, Status: c.Status,
			Health: health(c.Status), Project: labels["com.docker.compose.project"], Service: labels["com.docker.compose.service"],
			Ports: parsePorts(c.Ports)})
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].Project != list[j].Project {
			return list[i].Project < list[j].Project
		}
		return list[i].Name < list[j].Name
	})
	return list, nil
}

func health(status string) string {
	for _, h := range []string{"healthy", "unhealthy", "starting"} {
		if strings.Contains(status, "("+h+")") || strings.Contains(status, "(health: "+h+")") {
			return h
		}
	}
	return ""
}

// parsePorts reads the Ports column of docker ps: "0.0.0.0:8080->80/tcp, [::]:8080->80/tcp, 5432/tcp".
func parsePorts(s string) []Port {
	ports := []Port{}
	seen := map[Port]bool{}
	for _, f := range strings.Split(s, ",") {
		f = strings.TrimSpace(f)
		if f == "" {
			continue
		}
		var p Port
		target := f
		if host, t, ok := strings.Cut(f, "->"); ok {
			target = t
			if i := strings.LastIndex(host, ":"); i >= 0 {
				p.IP = strings.Trim(host[:i], "[]")
				p.Published = atoi(host[i+1:])
			}
			if p.IP == "0.0.0.0" || p.IP == "::" {
				p.IP = ""
			}
		}
		num, proto, _ := strings.Cut(target, "/")
		p.Target, p.Protocol = atoi(num), proto
		if !seen[p] {
			seen[p] = true
			ports = append(ports, p)
		}
	}
	return ports
}

func atoi(s string) int {
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			return n
		}
		n = n*10 + int(r-'0')
	}
	return n
}

// ---------- actions ----------

// Compose runs a short Compose command (start, stop, restart) on the stack or one service.
func (d *Docker) Compose(ctx context.Context, profiles []string, action, service string) error {
	var args []string
	switch action {
	case "start":
		args = []string{"up", "-d"}
	case "stop", "restart":
		args = []string{action}
	default:
		return i18n.Errorf("unknown action %s", action)
	}
	if service != "" {
		args = append(args, service)
	}
	_, err := d.run.Output(ctx, ComposeArgv(profiles, args...), d.root)
	return err
}

// Container runs an action on one container of the host.
func (d *Docker) Container(ctx context.Context, action, id string) error {
	var argv []string
	switch action {
	case "start", "stop", "restart":
		argv = []string{"docker", action, id}
	case "remove":
		argv = []string{"docker", "rm", "-f", id}
	default:
		return i18n.Errorf("unknown action %s", action)
	}
	_, err := d.run.Output(ctx, argv, "")
	return err
}

// ---------- details ----------

type Mount struct {
	Type        string `json:"type"`
	Name        string `json:"name,omitempty"`
	Source      string `json:"source"`
	Destination string `json:"destination"`
	ReadOnly    bool   `json:"readOnly"`
}

type Network struct {
	Name    string   `json:"name"`
	IP      string   `json:"ip"`
	Aliases []string `json:"aliases"`
}

type Details struct {
	ID       string    `json:"id"`
	Name     string    `json:"name"`
	Image    string    `json:"image"`
	State    string    `json:"state"`
	Health   string    `json:"health,omitempty"`
	ExitCode int       `json:"exitCode"`
	Command  string    `json:"command"`
	Created  string    `json:"created"`
	Started  string    `json:"started,omitempty"`
	Ports    []Port    `json:"ports"`
	Mounts   []Mount   `json:"mounts"`
	Networks []Network `json:"networks"`
}

type inspect struct {
	ID      string
	Name    string
	Created string
	Path    string
	Args    []string
	State   struct {
		Status    string
		ExitCode  int
		StartedAt string
		Health    *struct{ Status string }
	}
	Config struct {
		Image        string
		ExposedPorts map[string]struct{}
	}
	Mounts []struct {
		Type        string
		Name        string
		Source      string
		Destination string
		RW          bool
	}
	NetworkSettings struct {
		Ports    map[string][]struct{ HostIp, HostPort string }
		Networks map[string]struct {
			IPAddress string
			Aliases   []string
			DNSNames  []string
		}
	}
}

func (d *Docker) Inspect(ctx context.Context, id string) (*Details, error) {
	out, err := d.run.Output(ctx, []string{"docker", "inspect", "--type", "container", id}, "")
	if err != nil {
		return nil, err
	}
	var list []inspect
	if err := json.Unmarshal(out, &list); err != nil {
		return nil, err
	}
	if len(list) == 0 {
		return nil, errors.New("no such container: " + id)
	}
	in := list[0]
	det := &Details{ID: in.ID, Name: strings.TrimPrefix(in.Name, "/"), Image: in.Config.Image, State: in.State.Status,
		ExitCode: in.State.ExitCode, Command: execx.Join(append([]string{in.Path}, in.Args...)), Created: in.Created,
		Ports: []Port{}, Mounts: []Mount{}, Networks: []Network{}}
	if len(det.ID) > 12 {
		det.ID = det.ID[:12]
	}
	if in.State.Status == "running" {
		det.Started = in.State.StartedAt
	}
	if in.State.Health != nil {
		det.Health = in.State.Health.Status
	}
	keys := map[string]bool{}
	for k := range in.Config.ExposedPorts {
		keys[k] = true
	}
	for k := range in.NetworkSettings.Ports {
		keys[k] = true
	}
	seen := map[Port]bool{}
	for k := range keys {
		num, proto, _ := strings.Cut(k, "/")
		bindings := in.NetworkSettings.Ports[k]
		if len(bindings) == 0 {
			det.Ports = append(det.Ports, Port{Target: atoi(num), Protocol: proto})
			continue
		}
		for _, b := range bindings {
			ip := b.HostIp
			if ip == "0.0.0.0" || ip == "::" {
				ip = ""
			}
			p := Port{IP: ip, Published: atoi(b.HostPort), Target: atoi(num), Protocol: proto}
			if !seen[p] {
				seen[p] = true
				det.Ports = append(det.Ports, p)
			}
		}
	}
	sort.Slice(det.Ports, func(i, j int) bool {
		if det.Ports[i].Target != det.Ports[j].Target {
			return det.Ports[i].Target < det.Ports[j].Target
		}
		return det.Ports[i].Published < det.Ports[j].Published
	})
	for _, m := range in.Mounts {
		det.Mounts = append(det.Mounts, Mount{Type: m.Type, Name: m.Name, Source: m.Source, Destination: m.Destination, ReadOnly: !m.RW})
	}
	for name, n := range in.NetworkSettings.Networks {
		aliases := n.DNSNames
		if len(aliases) == 0 {
			aliases = n.Aliases
		}
		det.Networks = append(det.Networks, Network{Name: name, IP: n.IPAddress, Aliases: append([]string{}, aliases...)})
	}
	sort.Slice(det.Networks, func(i, j int) bool { return det.Networks[i].Name < det.Networks[j].Name })
	return det, nil
}

// Stats are the instant values of docker stats, as docker formats them.
type Stats struct {
	CPU     string `json:"cpu"`
	Mem     string `json:"mem"`
	MemPerc string `json:"memPerc"`
	Net     string `json:"net"`
	Block   string `json:"block"`
	PIDs    string `json:"pids"`
}

// Stats returns the values of running containers by short id (12 characters).
func (d *Docker) Stats(ctx context.Context, ids []string) (map[string]Stats, error) {
	res := map[string]Stats{}
	if len(ids) == 0 {
		return res, nil
	}
	out, err := d.run.Output(ctx, append([]string{"docker", "stats", "--no-stream", "--format", "json"}, ids...), "")
	if err != nil {
		return nil, err
	}
	raw, err := jsonLines[struct {
		ID, CPUPerc, MemUsage, MemPerc, NetIO, BlockIO, PIDs string
	}](out)
	if err != nil {
		return nil, err
	}
	for _, s := range raw {
		id := s.ID
		if len(id) > 12 {
			id = id[:12]
		}
		res[id] = Stats{CPU: s.CPUPerc, Mem: s.MemUsage, MemPerc: s.MemPerc, Net: s.NetIO, Block: s.BlockIO, PIDs: s.PIDs}
	}
	return res, nil
}

// ---------- logs ----------

// Logs follows the logs of a container, or of the stack when id is empty (lines prefixed by
// the service), timestamps included, stderr merged into stdout.
func (d *Docker) Logs(id string, profiles []string, tail int) (execx.Process, error) {
	n := strconv.Itoa(tail)
	argv := []string{"docker", "logs", "-f", "--tail", n, "--timestamps", id}
	if id == "" {
		argv = ComposeArgv(profiles, "logs", "-f", "--tail", n, "--timestamps", "--no-color")
	}
	return d.run.Start([]string{"sh", "-c", "exec " + execx.Join(argv) + " 2>&1"}, d.root)
}

// LogsTail returns the last lines of the logs of a container, or of a service of the stack
// (service set), without following them.
func (d *Docker) LogsTail(ctx context.Context, id, service string, profiles []string, tail int) (string, error) {
	n := strconv.Itoa(tail)
	argv := []string{"docker", "logs", "--tail", n, "--timestamps", id}
	if service != "" {
		argv = ComposeArgv(profiles, "logs", "--tail", n, "--timestamps", "--no-color", "--no-log-prefix", service)
	}
	out, err := d.run.Output(ctx, []string{"sh", "-c", execx.Join(argv) + " 2>&1"}, d.root)
	return string(out), err
}
