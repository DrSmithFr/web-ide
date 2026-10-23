//go:build linux

package confine

import (
	"encoding/json"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
)

// The test binary plays the three parts: the test, the plugin-exec it starts (CONFINE_EXEC),
// and the backend it executes (CONFINE_PROBE), which tries what a backend should not do.
func TestMain(m *testing.M) {
	switch {
	case os.Getenv("CONFINE_EXEC") != "":
		var o Options
		_ = json.Unmarshal([]byte(os.Getenv("CONFINE_EXEC")), &o)
		o.Env = []string{"CONFINE_PROBE=" + os.Getenv("CONFINE_ASK")}
		err := Exec(o)
		os.Stderr.WriteString(err.Error())
		os.Exit(126)
	case os.Getenv("CONFINE_PROBE") != "":
		probe(os.Getenv("CONFINE_PROBE"))
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// ask is what the probe tries.
type ask struct {
	Read, Write []string
	TCP         string
}

// probe prints, for each thing tried, "ok" or the error.
func probe(raw string) {
	var a ask
	_ = json.Unmarshal([]byte(raw), &a)
	out := map[string]string{}
	res := func(k string, err error) {
		if err == nil {
			out[k] = "ok"
		} else {
			out[k] = err.Error()
		}
	}
	for _, p := range a.Read {
		_, err := os.ReadFile(p)
		res("read "+p, err)
	}
	for _, p := range a.Write {
		res("write "+p, os.WriteFile(p, []byte("x"), 0o600))
	}
	if a.TCP != "" {
		c, err := net.Dial("tcp", a.TCP)
		if err == nil {
			c.Close()
		}
		res("tcp", err)
	}
	fd, err := unix.Socket(unix.AF_UNIX, unix.SOCK_STREAM, 0)
	if err == nil {
		unix.Close(fd)
	}
	res("unix", err)
	fd, err = unix.Socket(unix.AF_INET, unix.SOCK_DGRAM, 0)
	if err == nil {
		unix.Close(fd)
	}
	res("udp", err)
	_, err = unix.PtracePeekData(1, 0, make([]byte, 8))
	res("ptrace", err)
	json.NewEncoder(os.Stdout).Encode(out)
}

// confined runs the probe under o, returns what it found.
func confined(t *testing.T, o Options, a ask) map[string]string {
	t.Helper()
	exe, _ := os.Executable()
	o.Argv = []string{exe}
	o.Read = append(o.Read, filepath.Dir(exe))
	oj, _ := json.Marshal(o)
	aj, _ := json.Marshal(a)
	cmd := exec.Command(exe)
	cmd.Env = []string{"CONFINE_EXEC=" + string(oj), "CONFINE_ASK=" + string(aj)}
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("plugin-exec: %v %s", err, exitText(err))
	}
	got := map[string]string{}
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("probe: %v %q", err, out)
	}
	return got
}

func exitText(err error) string {
	if e, ok := err.(*exec.ExitError); ok {
		return string(e.Stderr)
	}
	return ""
}

func TestConfined(t *testing.T) {
	if ABI() < 1 {
		t.Skip("no Landlock on this kernel")
	}
	root := t.TempDir()
	mk := func(rel, body string) string {
		p := filepath.Join(root, rel)
		os.MkdirAll(filepath.Dir(p), 0o700)
		os.WriteFile(p, []byte(body), 0o600)
		return p
	}
	plugin := filepath.Dir(mk("plugins/greet/1.0.0/plugin.json", "{}"))
	data := filepath.Dir(mk("plugin-data/greet/.keep", ""))
	key := mk("device.key", "secret") // the pod's key, beside them: out of reach
	project := filepath.Dir(mk("ws/demo/a.txt", "a"))
	home := mk("home/.ssh/id_ed25519", "private")
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			c.Close()
		}
	}()
	a := ask{
		Read:  []string{key, home, filepath.Join(plugin, "plugin.json"), filepath.Join(project, "a.txt"), "/proc/1/environ", "/etc/hostname"},
		Write: []string{filepath.Join(data, "state"), filepath.Join(project, "b.txt")},
		TCP:   ln.Addr().String(),
	}
	denied := func(got map[string]string, keys ...string) {
		t.Helper()
		for _, k := range keys {
			if got[k] == "ok" || got[k] == "" {
				t.Errorf("%s: should be refused, got %q", k, got[k])
			}
		}
	}
	allowed := func(got map[string]string, keys ...string) {
		t.Helper()
		for _, k := range keys {
			if got[k] != "ok" {
				t.Errorf("%s: should work, got %q", k, got[k])
			}
		}
	}

	// files:read, no network.
	got := confined(t, Options{Plugin: plugin, Data: data, Project: project}, a)
	denied(got, "read "+key, "read "+home, "read /proc/1/environ", "write "+filepath.Join(project, "b.txt"), "unix", "udp", "ptrace")
	allowed(got, "read "+filepath.Join(plugin, "plugin.json"), "read "+filepath.Join(project, "a.txt"), "write "+filepath.Join(data, "state"))
	if ABI() >= 4 {
		denied(got, "tcp")
	}
	if os.Getenv("CI") == "" && !strings.Contains(got["read /etc/hostname"], "no such file") {
		allowed(got, "read /etc/hostname")
	}

	// files:write and net.
	got = confined(t, Options{Plugin: plugin, Data: data, Project: project, Write: true, Net: true}, a)
	allowed(got, "write "+filepath.Join(project, "b.txt"), "tcp", "udp")
	denied(got, "read "+key, "read "+home, "unix")

	// No project: nothing of it.
	got = confined(t, Options{Plugin: plugin, Data: data}, a)
	denied(got, "read "+filepath.Join(project, "a.txt"))
}

func TestFilterProgram(t *testing.T) {
	for _, net := range []bool{false, true} {
		p, err := program(net)
		if err != nil {
			t.Skip(err)
		}
		if len(p) > 255 {
			t.Fatalf("program too long for its jumps: %d", len(p))
		}
		if p[len(p)-1].K != allow {
			t.Fatal("allows by default")
		}
	}
}
