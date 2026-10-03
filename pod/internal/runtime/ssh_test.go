package runtime

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"webide/pod/internal/projects"
	"webide/pod/internal/search"
	"webide/pod/internal/sshtest"
	"webide/pod/internal/sshx"
	"webide/pod/internal/store"
)

func TestSSHProject(t *testing.T) {
	port := sshtest.Start(t)
	st, _ := store.Open(t.TempDir())
	pool := sshx.NewPool(sshx.NewHostKeys(st.Path("known_hosts")))
	t.Cleanup(pool.CloseAll)
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "index.php"), []byte("<?php echo 'remote';\n"), 0o644)

	p := projects.Project{ID: "p1", Type: "ssh", Path: dir, SSH: &projects.SSHTarget{Host: "127.0.0.1", Port: port, Auth: "password"}}
	emit := func(string, any, string) {}
	if _, err := Open(p, sshx.Creds{}, Deps{Pool: pool, Store: st}, emit); err == nil {
		t.Fatal("password required but not asked")
	} else {
		var ar *sshx.AuthRequired
		if !errors.As(err, &ar) || ar.Kind != "password" {
			t.Fatalf("expected AuthRequired, got %v", err)
		}
	}
	rt, err := Open(p, sshx.Creds{Password: "pw"}, Deps{Pool: pool, Store: st}, emit)
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()
	if rt.Local || rt.Root != dir {
		t.Fatalf("root = %s local=%v", rt.Root, rt.Local)
	}
	// Host key recorded on first use.
	if data, _ := os.ReadFile(st.Path("known_hosts")); !strings.Contains(string(data), strconv.Itoa(port)) {
		t.Fatal("host key not recorded")
	}

	es, err := rt.List(dir)
	if err != nil || len(es) != 1 || es[0].Name != "index.php" {
		t.Fatalf("list = %+v %v", es, err)
	}
	f, err := rt.Read(filepath.Join(dir, "index.php"))
	if err != nil || !strings.Contains(f.Content, "remote") {
		t.Fatalf("read = %+v %v", f, err)
	}
	if _, err := rt.Write(filepath.Join(dir, "sub", "new.txt"), "hello over sftp\n", ""); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(filepath.Join(dir, "sub", "new.txt")); string(data) != "hello over sftp\n" {
		t.Fatalf("written = %q", data)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	out, err := rt.Runner.Output(ctx, []string{"echo", "it's", "ok"}, dir)
	if err != nil || strings.TrimSpace(string(out)) != "it's ok" {
		t.Fatalf("exec = %q %v", out, err)
	}
	r, err := search.Remote(ctx, rt.Runner, dir, search.Options{Query: "sftp"})
	if err != nil || len(r.Matches) != 1 || r.Matches[0].Line != 1 {
		t.Fatalf("remote search = %+v %v", r, err)
	}
	files, err := rt.Files(ctx)
	if err != nil || len(files) != 2 {
		t.Fatalf("files = %v %v", files, err)
	}
	// The pooled connection is reused with the remembered password.
	if _, err := Open(p, sshx.Creds{}, Deps{Pool: pool, Store: st}, emit); err != nil {
		t.Fatalf("reopen with pooled connection: %v", err)
	}
}
