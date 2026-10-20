package keeper

import (
	"bufio"
	"bytes"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func start(t *testing.T) (*Server, string) {
	t.Helper()
	dir, err := os.MkdirTemp("", "kp")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	path := filepath.Join(dir, "k.sock")
	ln, err := Listen(path)
	if err != nil {
		t.Fatal(err)
	}
	s := NewServer("test")
	go s.Serve(ln)
	t.Cleanup(s.Close)
	return s, path
}

func dial(t *testing.T, path string) *Client {
	t.Helper()
	c, h, err := Dial(path)
	if err != nil {
		t.Fatal(err)
	}
	if h.Protocol != Protocol || h.Version != "test" || h.Pid != os.Getpid() {
		t.Fatalf("hello: %+v", h)
	}
	c.Retry = 50 * time.Millisecond
	t.Cleanup(c.Close)
	return c
}

// output gathers what an attachment receives.
type output struct {
	mu        sync.Mutex
	buf       bytes.Buffer
	first     int64
	got       bool
	truncated bool
	code      chan int
}

func follow(t *testing.T, c *Client, id string, from int64) (*output, *Attachment) {
	t.Helper()
	o := &output{code: make(chan int, 1)}
	a, err := c.Attach(id, from, func(off int64, data []byte, truncated bool) {
		o.mu.Lock()
		defer o.mu.Unlock()
		if !o.got {
			o.first, o.got, o.truncated = off, true, truncated
		}
		o.buf.Write(data)
	}, func(code int) { o.code <- code })
	if err != nil {
		t.Fatal(err)
	}
	return o, a
}

func (o *output) text() string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.buf.String()
}

func (o *output) wait(t *testing.T, sub string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !strings.Contains(o.text(), sub) {
		if time.Now().After(deadline) {
			t.Fatalf("no %q in %q", sub, o.text())
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func (o *output) exit(t *testing.T) int {
	t.Helper()
	select {
	case c := <-o.code:
		return c
	case <-time.After(5 * time.Second):
		t.Fatalf("no exit; output %q", o.text())
	}
	return 0
}

func TestPTY(t *testing.T) {
	_, path := start(t)
	c := dial(t, path)
	p, err := c.Spawn(Spawn{Owner: "p1", Argv: []string{"sh", "-c", `echo hi; read x; echo "got $x"; exit 3`}, PTY: true, Cols: 80, Rows: 20, Meta: []byte(`{"title":"T"}`)})
	if err != nil || p.Pid == 0 {
		t.Fatal(p, err)
	}
	o, _ := follow(t, c, p.ID, 0)
	o.wait(t, "hi")
	if err := c.Resize(p.ID, 100, 30); err != nil {
		t.Fatal(err)
	}
	if err := c.Input(p.ID, []byte("yo\n")); err != nil {
		t.Fatal(err)
	}
	o.wait(t, "got yo")
	if code := o.exit(t); code != 3 {
		t.Fatalf("code %d", code)
	}
	list, _ := c.List("p1")
	if len(list) != 1 || !list[0].Exited || list[0].Code != 3 || !list[0].PTY || string(list[0].Meta) != `{"title":"T"}` {
		t.Fatalf("list: %+v", list)
	}
	if other, _ := c.List("p2"); len(other) != 0 {
		t.Fatalf("owner filter: %+v", other)
	}
}

func TestPipe(t *testing.T) {
	_, path := start(t)
	c := dial(t, path)
	p, err := c.Spawn(Spawn{Owner: "p", Argv: []string{"sh", "-c", "echo out; echo err >&2; cat"}, Env: []string{"KEEPER_TEST=1"}})
	if err != nil {
		t.Fatal(err)
	}
	o, _ := follow(t, c, p.ID, 0)
	o.wait(t, "err")
	_ = c.Input(p.ID, []byte("abc\n"))
	o.wait(t, "abc")
	if err := c.CloseStdin(p.ID); err != nil {
		t.Fatal(err)
	}
	if code := o.exit(t); code != 0 || !strings.Contains(o.text(), "out") {
		t.Fatalf("code %d, %q", code, o.text())
	}
	// Killed with its group.
	p, _ = c.Spawn(Spawn{Owner: "p", Argv: []string{"sh", "-c", "sleep 30 & wait"}})
	o, _ = follow(t, c, p.ID, 0)
	if err := c.Signal(p.ID, "KILL"); err != nil {
		t.Fatal(err)
	}
	if code := o.exit(t); code != -1 {
		t.Fatalf("killed: code %d", code)
	}
}

func TestAttachFromOffset(t *testing.T) {
	_, path := start(t)
	c := dial(t, path)
	p, _ := c.Spawn(Spawn{Owner: "p", Argv: []string{"printf", "0123456789"}})
	o, _ := follow(t, c, p.ID, 0)
	o.exit(t)
	o, _ = follow(t, c, p.ID, 5)
	o.exit(t)
	if o.text() != "56789" || o.first != 5 || o.truncated {
		t.Fatalf("from 5: %q at %d", o.text(), o.first)
	}
	// Two attachments at once get the same output.
	q, _ := c.Spawn(Spawn{Owner: "p", Argv: []string{"sh", "-c", "sleep 0.2; echo both"}})
	a1, _ := follow(t, c, q.ID, 0)
	a2, _ := follow(t, c, q.ID, 0)
	a1.exit(t)
	a2.exit(t)
	if a1.text() != "both\n" || a2.text() != "both\n" {
		t.Fatalf("two attachments: %q %q", a1.text(), a2.text())
	}
}

func TestRing(t *testing.T) {
	_, path := start(t)
	c := dial(t, path)
	p, _ := c.Spawn(Spawn{Owner: "p", Argv: []string{"sh", "-c", fmt.Sprintf("head -c %d /dev/zero | tr '\\0' a; printf END", ScrollbackMax+1000)}})
	o, _ := follow(t, c, p.ID, 0)
	o.exit(t)
	list, _ := c.List("p")
	pr := list[0]
	if pr.End != int64(ScrollbackMax+1003) || pr.End-pr.Base != ScrollbackMax {
		t.Fatalf("ring: base %d end %d", pr.Base, pr.End)
	}
	o, _ = follow(t, c, p.ID, 0)
	o.exit(t)
	if !o.truncated || o.first != pr.Base || len(o.text()) != ScrollbackMax || !strings.HasSuffix(o.text(), "END") {
		t.Fatalf("from 0: truncated %v at %d, %d bytes", o.truncated, o.first, len(o.text()))
	}
}

// The connection drops (the pod restarts, the keeper stays): the attachment resumes from
// its offset, without gap or duplicate.
func TestReconnection(t *testing.T) {
	s, path := start(t)
	c := dial(t, path)
	p, _ := c.Spawn(Spawn{Owner: "p", Argv: []string{"sh", "-c", "i=0; while [ $i -lt 60 ]; do echo $i; i=$((i+1)); sleep 0.01; done"}})
	o, _ := follow(t, c, p.ID, 0)
	o.wait(t, "\n5\n")
	s.mu.Lock()
	for sc := range s.conns {
		sc.c.Close()
	}
	s.mu.Unlock()
	if code := o.exit(t); code != 0 {
		t.Fatalf("code %d", code)
	}
	sc := bufio.NewScanner(strings.NewReader(o.text()))
	n := 0
	for sc.Scan() {
		if v, _ := strconv.Atoi(sc.Text()); v != n || sc.Text() != strconv.Itoa(n) {
			t.Fatalf("line %d is %q: %q", n, sc.Text(), o.text())
		}
		n++
	}
	if n != 60 {
		t.Fatalf("%d lines", n)
	}
	if _, err := c.List(""); err != nil {
		t.Fatal("requests after the reconnection:", err)
	}
}

func TestForgetAndGC(t *testing.T) {
	s, path := start(t)
	c := dial(t, path)
	p, _ := c.Spawn(Spawn{Owner: "p", Argv: []string{"sleep", "30"}})
	if err := c.Forget(p.ID); err != nil {
		t.Fatal(err)
	}
	if list, _ := c.List(""); len(list) != 0 {
		t.Fatalf("after forget: %+v", list)
	}
	deadline := time.Now().Add(3 * time.Second)
	for syscallAlive(p.Pid) {
		if time.Now().After(deadline) {
			t.Fatal("a forgotten process keeps running")
		}
		time.Sleep(20 * time.Millisecond)
	}
	q, _ := c.Spawn(Spawn{Owner: "p", Argv: []string{"true"}})
	o, a := follow(t, c, q.ID, 0)
	o.exit(t)
	a.Stop()
	s.gc(time.Now().Add(GCAfter / 2))
	if list, _ := c.List(""); len(list) != 1 {
		t.Fatal("dropped too early")
	}
	s.gc(time.Now().Add(GCAfter + time.Minute))
	if list, _ := c.List(""); len(list) != 0 {
		t.Fatalf("not dropped: %+v", list)
	}
}

func syscallAlive(pid int) bool {
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return false
	}
	f := strings.Fields(string(data))
	return len(f) > 2 && f[2] != "Z"
}

func TestProtocolMismatch(t *testing.T) {
	dir, _ := os.MkdirTemp("", "kp")
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "k.sock")
	ln, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		h, _, _ := readFrame(bufio.NewReader(conn))
		_ = writeFrame(conn, header{ID: h.ID, Kind: KindResult, Result: []byte(`{"protocol":99}`)}, nil)
	}()
	if _, _, err := Dial(path); err == nil || !strings.Contains(err.Error(), "protocol 99") {
		t.Fatalf("mismatch: %v", err)
	}
	// A socket that answers is not replaced.
	if _, err := Listen(path); err == nil {
		t.Fatal("a live socket replaced")
	}
}
