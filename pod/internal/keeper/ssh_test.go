package keeper

import (
	"crypto/ed25519"
	"crypto/rand"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/DrSmithFr/web-ide/pod/internal/sshtest"
	"github.com/DrSmithFr/web-ide/pod/internal/sshx"
)

// The keeper dials SSH with the host key the pod accepted; terminals and commands run on it.
func TestSSH(t *testing.T) {
	s, path := start(t)
	c := dial(t, path)
	port := sshtest.Start(t)
	target := sshx.Target{Host: "127.0.0.1", Port: port, Auth: "password"}
	pool := sshx.NewPool(sshx.NewHostKeys(filepath.Join(t.TempDir(), "known_hosts")))
	t.Cleanup(pool.CloseAll)
	if _, err := pool.Get(target, sshx.Creds{Password: "pw"}); err != nil {
		t.Fatal(err)
	}
	key, creds, ok := pool.Pinned(target)
	if !ok || creds.Password != "pw" {
		t.Fatal("nothing pinned")
	}
	name := sshx.Key(target)

	// Another host key is refused.
	_, other, _ := ed25519.GenerateKey(rand.Reader)
	otherPub, _ := ssh.NewPublicKey(other.Public())
	if err := c.SSHDial(SSHDial{Key: "other", Target: target, Creds: creds, HostKey: string(ssh.MarshalAuthorizedKey(otherPub))}); err == nil {
		t.Fatal("a host key that is not the pinned one accepted")
	}
	if err := c.SSHDial(SSHDial{Key: name, Target: target, Creds: creds, HostKey: string(ssh.MarshalAuthorizedKey(key))}); err != nil {
		t.Fatal(err)
	}

	// A terminal: the login shell, input, resize, hang up.
	term, err := c.Spawn(Spawn{Owner: "p", Target: "ssh:" + name, Argv: []string{""}, PTY: true, Cols: 80, Rows: 24})
	if err != nil {
		t.Fatal(err)
	}
	o, _ := follow(t, c, term.ID, 0)
	_ = c.Input(term.ID, []byte("echo hi$((1+1))\n"))
	o.wait(t, "hi2")
	if err := c.Resize(term.ID, 100, 30); err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond) // the size and the input go by different requests
	_ = c.Input(term.ID, []byte("stty size\n"))
	o.wait(t, "30 100")
	if err := c.Signal(term.ID, "HUP"); err != nil {
		t.Fatal(err)
	}
	o.exit(t)

	// A command, its output and its code.
	cmd, _ := c.Spawn(Spawn{Owner: "p", Target: "ssh:" + name, Argv: []string{"echo piped; exit 3"}})
	co, _ := follow(t, c, cmd.ID, 0)
	_ = c.CloseStdin(cmd.ID) // as runShell does: no input
	if code := co.exit(t); code != 3 || !strings.Contains(co.text(), "piped") {
		t.Fatalf("command: %d %q", code, co.text())
	}
	if l, _ := c.List("p"); len(l) != 2 || l[1].Target != "ssh:"+name {
		t.Fatalf("list: %+v", l)
	}

	// The connection is lost: its processes end, said -1.
	long, _ := c.Spawn(Spawn{Owner: "p", Target: "ssh:" + name, Argv: []string{"sleep 30"}})
	lo, _ := follow(t, c, long.ID, 0)
	s.mu.Lock()
	s.sshConns[name].Close()
	s.mu.Unlock()
	if code := lo.exit(t); code != -1 {
		t.Fatalf("lost connection: code %d", code)
	}
	if _, err := c.Spawn(Spawn{Owner: "p", Target: "ssh:nope", Argv: []string{"true"}}); err == nil {
		t.Fatal("spawned on no connection")
	}
}
