package runtime

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/DrSmithFr/web-ide/pod/internal/keeper"
	"github.com/DrSmithFr/web-ide/pod/internal/projects"
	"github.com/DrSmithFr/web-ide/pod/internal/sshtest"
	"github.com/DrSmithFr/web-ide/pod/internal/sshx"
	"github.com/DrSmithFr/web-ide/pod/internal/store"
)

// An SSH terminal runs in the keeper on its own connection: it survives the pod, whose next
// runtime adopts it.
func TestSSHTerminalInKeeper(t *testing.T) {
	port := sshtest.Start(t)
	kdir, _ := os.MkdirTemp("", "kr")
	defer os.RemoveAll(kdir)
	sock := filepath.Join(kdir, "k.sock")
	ln, err := keeper.Listen(sock)
	if err != nil {
		t.Fatal(err)
	}
	ks := keeper.NewServer("test")
	go ks.Serve(ln)
	defer ks.Close()
	st, _ := store.Open(t.TempDir())
	p := projects.Project{ID: "p1", Type: "ssh", Path: t.TempDir(), SSH: &projects.SSHTarget{Host: "127.0.0.1", Port: port, Auth: "password"}}

	var mu sync.Mutex
	out := map[string]*strings.Builder{}
	emit := func(ev string, data any, _ string) {
		if ev != "console.output" {
			return
		}
		d := data.(map[string]any)
		mu.Lock()
		defer mu.Unlock()
		id := d["id"].(string)
		if out[id] == nil {
			out[id] = &strings.Builder{}
		}
		out[id].WriteString(d["data"].(string))
	}
	open := func() (*Runtime, *keeper.Client) {
		c, _, err := keeper.Dial(sock)
		if err != nil {
			t.Fatal(err)
		}
		pool := sshx.NewPool(sshx.NewHostKeys(st.Path("known_hosts")))
		rt, err := Open(p, sshx.Creds{Password: "pw"}, Deps{Pool: pool, Store: st, Keeper: c}, emit)
		if err != nil {
			t.Fatal(err)
		}
		return rt, c
	}

	rt, c1 := open()
	if rt.Keeper == nil || !strings.HasPrefix(rt.KeeperTarget, "ssh:") {
		t.Fatalf("the keeper does not run the SSH processes: %q", rt.KeeperTarget)
	}
	info, err := rt.Consoles.Create("task", "Counter", []string{"sh", "-c", "i=0; while :; do echo tick $i; i=$((i+1)); sleep 0.05; done"}, "", 80, 24)
	if err != nil {
		t.Fatal(err)
	}
	ticks := func(rt *Runtime) int {
		_, buf, _, _ := rt.Consoles.Snapshot(info.ID)
		return strings.Count(string(buf), "tick ")
	}
	deadline := time.Now().Add(5 * time.Second)
	for ticks(rt) < 5 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if ticks(rt) < 5 {
		t.Fatal("no output")
	}
	procs, _ := c1.List("p1")
	if len(procs) != 1 || procs[0].Target != rt.KeeperTarget {
		t.Fatalf("keeper processes: %+v", procs)
	}

	// The pod stops, another starts.
	rt.Release()
	c1.Close()
	rt2, c2 := open()
	defer func() { rt2.Close(); c2.Close() }()
	list := rt2.Consoles.List()
	if len(list) != 1 || list[0].ID != info.ID || list[0].Title != "Counter" {
		t.Fatalf("adopted: %+v", list)
	}
	before := ticks(rt2)
	deadline = time.Now().Add(5 * time.Second)
	for ticks(rt2) < before+10 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	_, buf, _, _ := rt2.Consoles.Snapshot(info.ID)
	n := 0
	for _, l := range strings.Split(strings.ReplaceAll(string(buf), "\r", ""), "\n") {
		if strings.HasPrefix(l, "tick ") {
			if l != "tick "+strconv.Itoa(n) {
				t.Fatalf("line %q after tick %d", l, n-1)
			}
			n++
		}
	}
	if n < before+10 {
		t.Fatalf("the counter stopped at %d", n)
	}
}
