package db

import (
	"bufio"
	"context"
	"net"
	"strings"
	"testing"

	"github.com/DrSmithFr/web-ide/pod/internal/sshtest"
	"github.com/DrSmithFr/web-ide/pod/internal/sshx"
	"github.com/DrSmithFr/web-ide/pod/internal/store"
)

// The database connection goes through the SSH host: the pod opens the tunnel, then dials the
// database address as seen from the server.
func TestSSHTunnel(t *testing.T) {
	// A line echo server standing for the database, reachable only "from the SSH host".
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			go func() {
				line, _ := bufio.NewReader(c).ReadString('\n')
				c.Write([]byte("echo:" + line))
				c.Close()
			}()
		}
	}()
	port := sshtest.Start(t)
	st, _ := store.Open(t.TempDir())
	pool := sshx.NewPool(sshx.NewHostKeys(st.Path("known_hosts")))
	defer pool.CloseAll()
	m := NewManager(Deps{Pool: pool, Store: st})
	cfg := ConnConfig{Kind: "redis", Host: "127.0.0.1", SSH: &SSHTunnel{Enabled: true, Host: "127.0.0.1", Port: port, Auth: "password"}}

	if _, err := m.dialer(cfg, Secret{}); err == nil {
		t.Fatal("SSH password not asked")
	}
	dial, err := m.dialer(cfg, Secret{SSHPassword: "pw"})
	if err != nil {
		t.Fatal(err)
	}
	c, err := dial(context.Background(), "tcp", l.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.Write([]byte("PING\n"))
	reply, _ := bufio.NewReader(c).ReadString('\n')
	if strings.TrimSpace(reply) != "echo:PING" {
		t.Fatalf("reply through the tunnel = %q", reply)
	}
}
