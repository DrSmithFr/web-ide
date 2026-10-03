package db

import (
	"context"
	"errors"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Opt-in tests against real servers, e.g.:
//   WEBIDE_TEST_PG=127.0.0.1:55432:postgres:secret WEBIDE_TEST_REDIS=127.0.0.1:56379:rpw go test ./internal/db/

func TestPostgres(t *testing.T) {
	spec := os.Getenv("WEBIDE_TEST_PG")
	if spec == "" {
		t.Skip("WEBIDE_TEST_PG not set")
	}
	p := strings.Split(spec, ":")
	port, _ := strconv.Atoi(p[1])
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cfg := ConnConfig{Kind: "postgres", Host: p[0], Port: port, User: p[2], Database: "postgres", SSLMode: "disable"}
	drv, err := openPostgres(ctx, cfg, p[3], nil)
	if err != nil {
		t.Fatal(err)
	}
	defer drv.Close()
	s, _ := drv.Session(ctx, "postgres")
	defer s.Close()
	for _, q := range []string{
		"DROP TABLE IF EXISTS webide_t",
		"CREATE TABLE webide_t (id serial PRIMARY KEY, name text NOT NULL DEFAULT 'x', at timestamptz)",
		"CREATE INDEX webide_t_name ON webide_t(name)",
		"INSERT INTO webide_t(name) SELECT 'n' || g FROM generate_series(1, 120) g",
	} {
		if _, err := s.Exec(ctx, q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	r, err := s.Exec(ctx, "SELECT id, name, at FROM webide_t ORDER BY id LIMIT 2")
	if err != nil || len(r.Rows) != 2 || r.Rows[0][1] != "n1" || r.Rows[0][2] != nil {
		t.Fatalf("select: %v %+v", err, r)
	}
	dbs, _ := drv.Children(ctx, Node{})
	if len(dbs) == 0 {
		t.Fatal("no database listed")
	}
	tables, _ := drv.Children(ctx, Node{Kind: "database", DB: "postgres"})
	var tbl *Node
	for i := range tables {
		if tables[i].Label == "webide_t" {
			tbl = &tables[i]
		}
	}
	if tbl == nil {
		t.Fatalf("table not listed: %+v", tables)
	}
	cols, _ := drv.Children(ctx, *tbl)
	if len(cols) != 5 || cols[2].Kind != "column" || cols[3].Kind != "index" || cols[4].Label != "webide_t_pkey" {
		t.Fatalf("columns/indexes = %+v", cols)
	}
	ddl, err := drv.DDL(ctx, "postgres", tbl.Table)
	if err != nil || !strings.Contains(ddl, "CREATE TABLE") || !strings.Contains(ddl, "webide_t_name") {
		t.Fatalf("ddl = %s %v", ddl, err)
	}
	page, _ := drv.Page(ctx, "postgres", tbl.Table, 100, 50)
	if page.Total != 120 || len(page.Rows) != 20 {
		t.Fatalf("page total=%d rows=%d", page.Total, len(page.Rows))
	}

	// Manual transaction, then cancel of a running statement.
	s.SetAutoCommit(false)
	if _, err := s.Exec(ctx, "DELETE FROM webide_t"); err != nil || !s.InTx() {
		t.Fatalf("delete in tx: %v %v", err, s.InTx())
	}
	if err := s.Rollback(ctx); err != nil || s.InTx() {
		t.Fatal("rollback", err)
	}
	s.SetAutoCommit(true)
	cctx, ccancel := context.WithTimeout(ctx, 300*time.Millisecond)
	defer ccancel()
	if _, err := s.Exec(cctx, "SELECT pg_sleep(5)"); err == nil {
		t.Fatal("statement not cancelled")
	}
	s.Exec(ctx, "DROP TABLE webide_t")

	// Wrong password: the page is asked for the password.
	m := NewManager(Deps{})
	if _, err := m.open(ctx, cfg, Secret{Password: "nope"}); !errors.As(err, new(*NeedPassword)) {
		t.Fatalf("expected NeedPassword, got %v", err)
	}
}

func TestRedis(t *testing.T) {
	spec := os.Getenv("WEBIDE_TEST_REDIS")
	if spec == "" {
		t.Skip("WEBIDE_TEST_REDIS not set")
	}
	p := strings.Split(spec, ":")
	port, _ := strconv.Atoi(p[1])
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cfg := ConnConfig{Kind: "redis", Host: p[0], Port: port, RedisDB: 2}
	m := NewManager(Deps{})
	if _, err := m.open(ctx, cfg, Secret{}); !errors.As(err, new(*NeedPassword)) {
		t.Fatalf("expected NeedPassword without password, got %v", err)
	}
	drv, err := openRedis(ctx, cfg, p[2], nil)
	if err != nil {
		t.Fatal(err)
	}
	defer drv.Close()
	s, _ := drv.Session(ctx, "db2")
	defer s.Close()
	for _, q := range []string{`FLUSHDB`, `SET "greeting key" "bonjour monde" EX 100`, `HSET user:1 name ada lang go`, `RPUSH q a b c`} {
		if _, err := s.Exec(ctx, q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	r, _ := s.Exec(ctx, "HGETALL user:1")
	if len(r.Rows) != 2 || r.Columns[0] != "key" {
		t.Fatalf("hgetall = %+v", r)
	}
	r, _ = s.Exec(ctx, "GET missing")
	if r.Rows[0][0] != "(nil)" {
		t.Fatalf("nil reply = %+v", r)
	}
	dbs, _ := drv.Children(ctx, Node{})
	found := false
	for _, d := range dbs {
		found = found || (d.DB == "db2" && strings.HasPrefix(d.Detail, "3"))
	}
	if !found {
		t.Fatalf("db2 not listed with 3 keys: %+v", dbs)
	}
	keys, _ := drv.Children(ctx, Node{Kind: "database", DB: "db2"})
	if len(keys) != 3 || !strings.Contains(keys[0].Detail, "string · TTL") {
		t.Fatalf("keys = %+v", keys)
	}
	page, _ := drv.Page(ctx, "db2", "q", 1, 10)
	if page.Total != 3 || len(page.Rows) != 2 || page.Rows[0][1] != "b" {
		t.Fatalf("list page = %+v", page)
	}
	// MULTI / EXEC on the console connection.
	for _, q := range []string{"MULTI", "INCR n", "INCR n"} {
		s.Exec(ctx, q)
	}
	if r, err := s.Exec(ctx, "EXEC"); err != nil || len(r.Rows) != 2 {
		t.Fatalf("exec = %+v %v", r, err)
	}
	s.Exec(ctx, "FLUSHDB")
}
