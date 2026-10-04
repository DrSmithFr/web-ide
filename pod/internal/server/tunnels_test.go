package server

import (
	"bufio"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/DrSmithFr/web-ide/pod/internal/sshtest"
)

// echoServer answers each line with "echo:<line>"; it stands for a service of the SSH host.
func echoServer(t *testing.T) int {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				r := bufio.NewReader(c)
				for {
					line, err := r.ReadString('\n')
					if err != nil {
						return
					}
					c.Write([]byte("echo:" + line))
				}
			}()
		}
	}()
	return l.Addr().(*net.TCPAddr).Port
}

func freePort(t *testing.T) int {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

func roundTrip(addr string) (string, error) {
	c, err := net.DialTimeout("tcp", addr, time.Second)
	if err != nil {
		return "", err
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(2 * time.Second))
	c.Write([]byte("ping\n"))
	return bufio.NewReader(c).ReadString('\n')
}

func TestTunnels(t *testing.T) {
	old := TunnelIdle
	TunnelIdle = 200 * time.Millisecond
	t.Cleanup(func() { TunnelIdle = old })
	sshPort := sshtest.Start(t)
	remote := echoServer(t)
	local := freePort(t)
	dir := t.TempDir()

	_, ts := newServer(t)
	a, _ := dial(t, ts, "secret-token-0123456789abcdef0123")
	created := a.call("projects.create", map[string]any{"type": "ssh", "path": dir,
		"ssh": map[string]any{"host": "127.0.0.1", "port": sshPort, "auth": "password"}})["result"].(map[string]any)
	id := created["id"].(string)
	a.call("project.open", map[string]any{"id": id, "creds": map[string]any{"password": "pw"}})

	res := a.call("tunnels.save", map[string]any{"remotePort": remote, "localPort": local, "enabled": true})
	list := res["result"].([]any)
	if len(list) != 1 || list[0].(map[string]any)["open"] != true || list[0].(map[string]any)["remoteHost"] != "127.0.0.1" {
		t.Fatalf("saved = %v", list)
	}
	addr := "127.0.0.1:" + strconv.Itoa(local)
	if got, err := roundTrip(addr); err != nil || got != "echo:ping\n" {
		t.Fatalf("through the tunnel: %q %v", got, err)
	}
	if data, _ := os.ReadFile(filepath.Join(dir, ".ide", "tunnels.json")); !strings.Contains(string(data), strconv.Itoa(remote)) {
		t.Fatalf("tunnels.json = %s", data)
	}
	all := a.call("tunnels.all", nil)["result"].([]any)
	if len(all) != 1 || all[0].(map[string]any)["host"] != "127.0.0.1" {
		t.Fatalf("all = %v", all)
	}
	// A second tunnel on the same local port cannot listen: its error is kept in its state.
	res = a.callRaw("tunnels.save", map[string]any{"remotePort": remote, "localPort": local, "enabled": true})
	if res["error"] == nil {
		t.Fatal("local port in use accepted")
	}
	st := a.call("tunnels.get", nil)["result"].([]any)
	if len(st) != 2 || st[1].(map[string]any)["error"] == nil {
		t.Fatalf("state = %v", st)
	}
	a.call("tunnels.delete", map[string]any{"id": st[1].(map[string]any)["id"]})

	// The last window leaves: the tunnel closes after TunnelIdle.
	a.c.CloseNow()
	time.Sleep(600 * time.Millisecond)
	if _, err := roundTrip(addr); err == nil {
		t.Fatal("tunnel still open without window")
	}
	// Opening the project again opens the enabled tunnels.
	b, _ := dial(t, ts, "secret-token-0123456789abcdef0123")
	b.call("project.open", map[string]any{"id": id})
	if got, err := roundTrip(addr); err != nil || got != "echo:ping\n" {
		t.Fatalf("tunnel not reopened: %q %v", got, err)
	}
	// Closed from the home page: still enabled, the project opens it again.
	b.call("tunnels.closeAll", nil)
	if _, err := roundTrip(addr); err == nil {
		t.Fatal("close all left the tunnel open")
	}
	if st := b.call("tunnels.get", nil)["result"].([]any); st[0].(map[string]any)["enabled"] != true {
		t.Fatalf("closing disabled the tunnel: %v", st)
	}
	// Disabled: closed and not opened with the project.
	sp := st[0].(map[string]any)
	sp["enabled"] = false
	b.call("tunnels.save", sp)
	b.call("project.open", map[string]any{"id": id})
	if _, err := roundTrip(addr); err == nil {
		t.Fatal("disabled tunnel opened")
	}
}
