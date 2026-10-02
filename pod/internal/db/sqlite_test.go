package db

import (
	"context"
	"path/filepath"
	"testing"

	"webide/pod/internal/execx"
)

func TestSQLite(t *testing.T) {
	ctx := context.Background()
	drv, err := openSQLite(ctx, filepath.Join(t.TempDir(), "t.db"), true, execx.Local{})
	if err != nil {
		t.Fatal(err)
	}
	defer drv.Close()
	s, _ := drv.Session(ctx, "main")
	defer s.Close()
	for _, q := range []string{
		"CREATE TABLE users (id INTEGER PRIMARY KEY, name TEXT NOT NULL)",
		"CREATE INDEX users_name ON users(name)",
		"INSERT INTO users(name) VALUES ('ada'), ('linus')",
	} {
		if _, err := s.Exec(ctx, q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	r, err := s.Exec(ctx, "SELECT id, name FROM users ORDER BY id")
	if err != nil || len(r.Rows) != 2 || r.Rows[1][1] != "linus" {
		t.Fatalf("select: %v %+v", err, r)
	}
	tables, _ := drv.Children(ctx, Node{Kind: "database", DB: "main"})
	if len(tables) != 1 || tables[0].Label != "users" {
		t.Fatalf("tables = %+v", tables)
	}
	cols, _ := drv.Children(ctx, tables[0])
	if len(cols) != 3 || cols[2].Kind != "index" {
		t.Fatalf("columns = %+v", cols)
	}

	// Manual transaction: the insert stays pending until rollback.
	if err := s.SetAutoCommit(false); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Exec(ctx, "INSERT INTO users(name) VALUES ('grace')"); err != nil || !s.InTx() {
		t.Fatalf("insert in tx: %v inTx=%v", err, s.InTx())
	}
	if err := s.SetAutoCommit(true); err == nil {
		t.Fatal("switching to autocommit with an open transaction must fail")
	}
	if err := s.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	p, _ := drv.Page(ctx, "main", "users", 0, 10)
	if p.Total != 2 {
		t.Fatalf("total after rollback = %d", p.Total)
	}
	if ddl, _ := drv.DDL(ctx, "main", "users"); ddl == "" {
		t.Fatal("empty DDL")
	}
}

func TestSplitArgs(t *testing.T) {
	a, err := splitArgs(`SET "my key" 'a b' 3`)
	if err != nil || len(a) != 4 || a[1] != "my key" || a[2] != "a b" {
		t.Fatalf("%v %#v", err, a)
	}
}
