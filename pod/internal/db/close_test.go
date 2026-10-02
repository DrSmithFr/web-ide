package db

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"webide/pod/internal/execx"
	"webide/pod/internal/fsx"
	"webide/pod/internal/store"
)

// Closing the manager must not wait for the connections held by the SQL consoles.
func TestCloseWithOpenConsole(t *testing.T) {
	root := t.TempDir()
	st, _ := store.Open(t.TempDir())
	os.WriteFile(filepath.Join(root, "a.db"), nil, 0o644)
	m := NewManager(Deps{FS: fsx.Local{}, Root: root, ProjectID: "p", Local: true, Runner: execx.Local{}, Store: st})
	c, err := m.Save(ConnConfig{Kind: "sqlite", Path: "a.db"}, Secret{})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	m.ConsoleOpen("k1", c.ID, "main")
	if _, _, err := m.Exec(ctx, "k1", "CREATE TABLE t(x)"); err != nil {
		t.Fatal(err)
	}
	m.SetAutoCommit(ctx, "k1", false)
	m.Exec(ctx, "k1", "INSERT INTO t VALUES (1)")
	done := make(chan struct{})
	go func() {
		m.CloseAll()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("CloseAll blocked by an open console")
	}
}
