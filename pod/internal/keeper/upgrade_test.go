package keeper

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// The test binary runs as a keeper when KEEPER_HELPER is set (the re-exec runs it again).
func TestMain(m *testing.M) {
	if os.Getenv("KEEPER_HELPER") != "" {
		helperMain()
		return
	}
	os.Exit(m.Run())
}

func helperMain() {
	if len(os.Args) > 2 && os.Args[1] == "keeper" && os.Args[2] == "-state-version" {
		fmt.Println(StateVersion)
		return
	}
	var s *Server
	var ln net.Listener
	var err error
	if state := os.Getenv(StateEnv); state != "" {
		s, ln, err = Restore(state, "v2")
	} else if ln, err = Listen(os.Getenv("KEEPER_SOCK")); err == nil {
		s = NewServer("v1")
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	_ = s.Serve(ln)
}

func startHelper(t *testing.T) (string, *exec.Cmd) {
	t.Helper()
	dir, _ := os.MkdirTemp("", "ku")
	t.Cleanup(func() { os.RemoveAll(dir) })
	sock := filepath.Join(dir, "k.sock")
	cmd := exec.Command(os.Args[0], "-test.run=^$")
	cmd.Env = append(os.Environ(), "KEEPER_HELPER=1", "KEEPER_SOCK="+sock)
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _, _ = cmd.Process.Wait() })
	for i := 0; i < 100; i++ {
		if _, err := os.Stat(sock); err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	return sock, cmd
}

func TestUpgrade(t *testing.T) {
	sock, helper := startHelper(t)
	c := dial2(t, sock)
	before := hello(t, sock)

	// A terminal counting, a piped process waiting for input, an ended HTTP request.
	p, err := c.Spawn(Spawn{Owner: "p", Argv: []string{"sh", "-c", "i=0; while :; do echo tick $i; i=$((i+1)); sleep 0.03; done"}, PTY: true})
	if err != nil {
		t.Fatal(err)
	}
	cat, _ := c.Spawn(Spawn{Owner: "p", Argv: []string{"cat"}})
	o, _ := follow(t, c, p.ID, 0)
	co, _ := follow(t, c, cat.ID, 0)
	o.wait(t, "tick 5\r\n")
	ts := stream(t, 3)
	_ = c.StartHTTP(HTTPStart{ID: "done", Method: "POST", URL: ts.URL}, []byte("hello"))
	resp, _ := c.OpenHTTP(context.Background(), "done", 0)
	io.ReadAll(resp.Body)

	// A binary that does not exist: the keeper goes on.
	if err := c.Upgrade(Upgrade{Path: "/nonexistent/keeper"}, func(string) {}); err == nil {
		t.Fatal("upgraded with no binary")
	}
	if _, err := c.List(""); err != nil {
		t.Fatal("the keeper stopped serving:", err)
	}

	// A request in flight: the update waits for it, then gives up; with force it goes on.
	_ = c.StartHTTP(HTTPStart{ID: "inflight", Method: "POST", URL: stream(t, -1).URL}, []byte("hello"))
	time.Sleep(50 * time.Millisecond)
	var lines []string
	if err := c.Upgrade(Upgrade{Wait: 300 * time.Millisecond}, func(l string) { lines = append(lines, l) }); err == nil || !strings.Contains(err.Error(), "inflight") {
		t.Fatalf("not waiting for the request: %v %v", err, lines)
	}
	if len(lines) == 0 || !strings.Contains(lines[0], "waiting for HTTP request inflight") {
		t.Fatalf("progress: %v", lines)
	}
	o.wait(t, "tick 15\r\n") // still serving after the wait

	uc, _, _ := DialAny(sock)
	if err := uc.Upgrade(Upgrade{Force: true}, func(l string) { t.Log(l) }); err != nil {
		t.Fatal(err)
	}
	uc.Close()

	// The same keeper process, a new binary, its processes kept.
	after := hello(t, sock)
	if after.Pid != before.Pid || after.Pid != helper.Process.Pid || after.Version != "v2" || after.Started != before.Started {
		t.Fatalf("hello %+v, before %+v", after, before)
	}
	var list []Proc
	for i := 0; i < 100; i++ { // the client reconnects by itself
		if list, err = c.List("p"); err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if len(list) != 2 || list[0].Pid != p.Pid || list[0].Exited {
		t.Fatalf("processes: %+v", list)
	}
	last := strings.Count(o.text(), "tick ")
	o.wait(t, fmt.Sprintf("tick %d\r\n", last+10))
	n := 0
	for _, l := range strings.Split(o.text(), "\r\n") {
		if strings.HasPrefix(l, "tick ") {
			if l != fmt.Sprintf("tick %d", n) {
				t.Fatalf("line %q after tick %d", l, n-1)
			}
			n++
		}
	}
	if err := c.Input(cat.ID, []byte("after\n")); err != nil {
		t.Fatal(err)
	}
	co.wait(t, "after")
	if err := c.Resize(p.ID, 90, 20); err != nil {
		t.Fatal(err)
	}
	resp, err = c.OpenHTTP(context.Background(), "done", 0)
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := io.ReadAll(resp.Body); string(b) != want(3) {
		t.Fatalf("ended request after the update: %q", b)
	}
	if hs, _ := c.ListHTTP(""); len(hs) != 2 {
		t.Fatalf("requests: %+v", hs)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(sock), "keeper-state.json")); !os.IsNotExist(err) {
		t.Fatal("the state file stays")
	}
	// It updates again (the descriptors are pollable again).
	uc, _, _ = DialAny(sock)
	if err := uc.Upgrade(Upgrade{}, func(string) {}); err != nil {
		t.Fatal(err)
	}
	uc.Close()
	again := hello(t, sock)
	if again.Pid != before.Pid {
		t.Fatal("restarted on the second update")
	}
	o.wait(t, fmt.Sprintf("tick %d\r\n", strings.Count(o.text(), "tick ")+10)) // output after it
	// Ending the processes: the shell is reaped by the new keeper.
	if err := c.Signal(p.ID, "HUP"); err != nil {
		t.Fatal("hang up:", err)
	}
	if code := o.exit(t); code == 0 {
		t.Log("exit", code)
	}
	if err := syscall.Kill(p.Pid, 0); err == nil {
		t.Fatal("the shell outlives its hang up")
	}
}

func dial2(t *testing.T, sock string) *Client {
	t.Helper()
	c, _, err := Dial(sock)
	if err != nil {
		t.Fatal(err)
	}
	c.Retry = 50 * time.Millisecond
	t.Cleanup(c.Close)
	return c
}

func hello(t *testing.T, sock string) Hello {
	t.Helper()
	var h Hello
	var err error
	for i := 0; i < 100; i++ {
		var c *Client
		if c, h, err = DialAny(sock); err == nil {
			c.Close()
			return h
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("no keeper:", err)
	return h
}
