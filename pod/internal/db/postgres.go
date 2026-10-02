package db

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
)

type dialFunc func(ctx context.Context, network, addr string) (net.Conn, error)

type pgDriver struct {
	base  *pgconn.Config
	mu    sync.Mutex
	conns map[string]*pgconn.PgConn
}

func pgKV(s string) string { return "'" + strings.NewReplacer(`\`, `\\`, `'`, `\'`).Replace(s) + "'" }

func openPostgres(ctx context.Context, c ConnConfig, password string, dial dialFunc) (Driver, error) {
	port := c.Port
	if port == 0 {
		port = 5432
	}
	dbname := c.Database
	if dbname == "" {
		dbname = "postgres"
	}
	ssl := c.SSLMode
	if ssl == "" {
		ssl = "prefer"
	}
	dsn := fmt.Sprintf("host=%s port=%d user=%s dbname=%s sslmode=%s connect_timeout=10 application_name=web-ide",
		pgKV(c.Host), port, pgKV(c.User), pgKV(dbname), pgKV(ssl))
	cfg, err := pgconn.ParseConfig(dsn)
	if err != nil {
		return nil, err
	}
	cfg.Password = password
	if dial != nil {
		cfg.DialFunc = pgconn.DialFunc(dial)
		cfg.LookupFunc = func(ctx context.Context, host string) ([]string, error) { return []string{host}, nil }
	}
	d := &pgDriver{base: cfg, conns: map[string]*pgconn.PgConn{}}
	if _, err := d.conn(ctx, dbname); err != nil {
		return nil, err
	}
	return d, nil
}

func (d *pgDriver) connect(ctx context.Context, db string) (*pgconn.PgConn, error) {
	cfg := d.base.Copy()
	if db != "" {
		cfg.Database = db
	}
	return pgconn.ConnectConfig(ctx, cfg)
}

// conn returns the introspection connection of a database.
func (d *pgDriver) conn(ctx context.Context, db string) (*pgconn.PgConn, error) {
	if db == "" {
		db = d.base.Database
	}
	d.mu.Lock()
	c := d.conns[db]
	d.mu.Unlock()
	if c != nil && !c.IsClosed() {
		return c, nil
	}
	c, err := d.connect(ctx, db)
	if err != nil {
		return nil, err
	}
	d.mu.Lock()
	d.conns[db] = c
	d.mu.Unlock()
	return c, nil
}

// pgQuery runs q with the simple protocol: every value comes back as text, ready to display.
func pgQuery(ctx context.Context, c *pgconn.PgConn, q string) (*Result, error) {
	start := time.Now()
	mrr := c.Exec(ctx, q)
	var last *Result
	for mrr.NextResult() {
		rr := mrr.ResultReader()
		res := &Result{Total: -1, Rows: [][]any{}}
		for _, fd := range rr.FieldDescriptions() {
			res.Columns = append(res.Columns, fd.Name)
		}
		for rr.NextRow() {
			if len(res.Rows) >= maxRows {
				res.Truncated = true
				continue
			}
			vals := rr.Values()
			row := make([]any, len(vals))
			for i, v := range vals {
				if v != nil {
					row[i] = string(v)
				}
			}
			res.Rows = append(res.Rows, row)
		}
		tag, err := rr.Close()
		if err != nil {
			mrr.Close()
			return nil, err
		}
		res.Command = tag.String()
		res.Affected = tag.RowsAffected()
		if last == nil || res.Columns != nil || last.Columns == nil {
			last = res
		}
	}
	if err := mrr.Close(); err != nil {
		return nil, err
	}
	if last == nil {
		last = &Result{Total: -1}
	}
	last.DurationMs = ms(start)
	return last, nil
}

func splitQualified(t string) (string, string) {
	if s, n, ok := strings.Cut(t, "."); ok {
		return s, n
	}
	return "public", t
}

func qualified(t string) string {
	s, n := splitQualified(t)
	return quoteIdent(s) + "." + quoteIdent(n)
}

func (d *pgDriver) q(ctx context.Context, db, q string) (*Result, error) {
	c, err := d.conn(ctx, db)
	if err != nil {
		return nil, err
	}
	return pgQuery(ctx, c, q)
}

func (d *pgDriver) Children(ctx context.Context, n Node) ([]Node, error) {
	switch n.Kind {
	case "":
		r, err := d.q(ctx, "", "SELECT datname FROM pg_database WHERE NOT datistemplate AND datallowconn ORDER BY datname")
		if err != nil {
			return nil, err
		}
		var out []Node
		for _, row := range r.Rows {
			name := fmt.Sprint(row[0])
			out = append(out, Node{ID: "db:" + name, Label: name, Kind: "database", DB: name})
		}
		return out, nil
	case "database":
		r, err := d.q(ctx, n.DB, `SELECT table_schema, table_name, table_type FROM information_schema.tables
			WHERE table_schema NOT IN ('pg_catalog','information_schema') AND table_schema NOT LIKE 'pg_toast%' ORDER BY 1, 2`)
		if err != nil {
			return nil, err
		}
		var out []Node
		for _, row := range r.Rows {
			schema, name := fmt.Sprint(row[0]), fmt.Sprint(row[1])
			kind := "table"
			if strings.Contains(fmt.Sprint(row[2]), "VIEW") {
				kind = "view"
			}
			label := name
			if schema != "public" {
				label = schema + "." + name
			}
			out = append(out, Node{ID: "t:" + n.DB + ":" + schema + "." + name, Label: label, Kind: kind, DB: n.DB, Table: schema + "." + name})
		}
		return out, nil
	case "table", "view":
		schema, name := splitQualified(n.Table)
		r, err := d.q(ctx, n.DB, fmt.Sprintf(`SELECT column_name, data_type, is_nullable, column_default FROM information_schema.columns
			WHERE table_schema=%s AND table_name=%s ORDER BY ordinal_position`, quoteLit(schema), quoteLit(name)))
		if err != nil {
			return nil, err
		}
		var out []Node
		for _, row := range r.Rows {
			detail := fmt.Sprint(row[1])
			if row[2] == "NO" {
				detail += " · not null"
			}
			if row[3] != nil {
				detail += " · défaut " + fmt.Sprint(row[3])
			}
			out = append(out, Node{ID: "c:" + n.DB + ":" + n.Table + "." + fmt.Sprint(row[0]), Label: fmt.Sprint(row[0]), Kind: "column", Detail: detail, Leaf: true, DB: n.DB, Table: n.Table})
		}
		idx, err := d.q(ctx, n.DB, fmt.Sprintf("SELECT indexname FROM pg_indexes WHERE schemaname=%s AND tablename=%s ORDER BY 1", quoteLit(schema), quoteLit(name)))
		if err == nil {
			for _, row := range idx.Rows {
				out = append(out, Node{ID: "i:" + n.DB + ":" + schema + "." + fmt.Sprint(row[0]), Label: fmt.Sprint(row[0]), Kind: "index", Leaf: true, DB: n.DB, Table: n.Table})
			}
		}
		return out, nil
	}
	return nil, nil
}

func (d *pgDriver) DDL(ctx context.Context, db, table string) (string, error) {
	schema, name := splitQualified(table)
	reg := quoteLit(qualified(table))
	kind, err := d.q(ctx, db, "SELECT relkind FROM pg_class WHERE oid = "+reg+"::regclass")
	if err != nil {
		return "", err
	}
	if len(kind.Rows) == 1 && (kind.Rows[0][0] == "v" || kind.Rows[0][0] == "m") {
		v, err := d.q(ctx, db, "SELECT pg_get_viewdef("+reg+"::regclass, true)")
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("CREATE VIEW %s AS\n%s", qualified(table), fmt.Sprint(v.Rows[0][0])), nil
	}
	cols, err := d.q(ctx, db, fmt.Sprintf(`SELECT a.attname, format_type(a.atttypid, a.atttypmod), a.attnotnull, pg_get_expr(ad.adbin, ad.adrelid)
		FROM pg_attribute a LEFT JOIN pg_attrdef ad ON ad.adrelid = a.attrelid AND ad.adnum = a.attnum
		WHERE a.attrelid = %s::regclass AND a.attnum > 0 AND NOT a.attisdropped ORDER BY a.attnum`, reg))
	if err != nil {
		return "", err
	}
	var lines []string
	for _, row := range cols.Rows {
		l := "    " + quoteIdent(fmt.Sprint(row[0])) + " " + fmt.Sprint(row[1])
		if row[2] == "t" {
			l += " NOT NULL"
		}
		if row[3] != nil {
			l += " DEFAULT " + fmt.Sprint(row[3])
		}
		lines = append(lines, l)
	}
	cons, err := d.q(ctx, db, "SELECT conname, pg_get_constraintdef(oid) FROM pg_constraint WHERE conrelid = "+reg+"::regclass ORDER BY contype, conname")
	if err == nil {
		for _, row := range cons.Rows {
			lines = append(lines, "    CONSTRAINT "+quoteIdent(fmt.Sprint(row[0]))+" "+fmt.Sprint(row[1]))
		}
	}
	ddl := fmt.Sprintf("CREATE TABLE %s (\n%s\n);", qualified(table), strings.Join(lines, ",\n"))
	idx, err := d.q(ctx, db, fmt.Sprintf(`SELECT indexdef FROM pg_indexes i WHERE schemaname=%s AND tablename=%s
		AND NOT EXISTS (SELECT 1 FROM pg_constraint c WHERE c.conname = i.indexname) ORDER BY indexname`, quoteLit(schema), quoteLit(name)))
	if err == nil {
		for _, row := range idx.Rows {
			ddl += "\n" + fmt.Sprint(row[0]) + ";"
		}
	}
	return ddl, nil
}

func (d *pgDriver) IndexDef(ctx context.Context, db, table, index string) (string, error) {
	schema, _ := splitQualified(table)
	r, err := d.q(ctx, db, fmt.Sprintf("SELECT indexdef FROM pg_indexes WHERE schemaname=%s AND indexname=%s", quoteLit(schema), quoteLit(index)))
	if err != nil {
		return "", err
	}
	if len(r.Rows) == 0 {
		return "", errors.New("index introuvable")
	}
	return fmt.Sprint(r.Rows[0][0]) + ";", nil
}

func (d *pgDriver) Page(ctx context.Context, db, table string, offset, limit int) (*Result, error) {
	r, err := d.q(ctx, db, fmt.Sprintf("SELECT * FROM %s LIMIT %d OFFSET %d", qualified(table), limit, offset))
	if err != nil {
		return nil, err
	}
	cctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	if c, err := d.q(cctx, db, "SELECT count(*) FROM "+qualified(table)); err == nil && len(c.Rows) == 1 {
		fmt.Sscan(fmt.Sprint(c.Rows[0][0]), &r.Total)
	} else if e, err := d.q(ctx, db, "SELECT reltuples::bigint FROM pg_class WHERE oid = "+quoteLit(qualified(table))+"::regclass"); err == nil && len(e.Rows) == 1 {
		fmt.Sscan(fmt.Sprint(e.Rows[0][0]), &r.Total)
		r.Message = "nombre de lignes estimé"
	}
	return r, nil
}

func (d *pgDriver) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	for k, c := range d.conns {
		_ = c.Close(context.Background())
		delete(d.conns, k)
	}
	return nil
}

func (d *pgDriver) Session(ctx context.Context, db string) (Session, error) {
	c, err := d.connect(ctx, db)
	if err != nil {
		return nil, err
	}
	return &pgSession{c: c, auto: true}, nil
}

type pgSession struct {
	c    *pgconn.PgConn
	auto bool
}

func (s *pgSession) InTx() bool { return s.c.TxStatus() != 'I' }

func (s *pgSession) Exec(ctx context.Context, q string) (*Result, error) {
	w := firstWord(q)
	if !s.auto && !s.InTx() && w != "BEGIN" && w != "START" && w != "COMMIT" && w != "ROLLBACK" && w != "END" {
		if _, err := pgQuery(ctx, s.c, "BEGIN"); err != nil {
			return nil, err
		}
	}
	r, err := pgQuery(ctx, s.c, q)
	if r != nil {
		r.InTx = s.InTx()
	}
	return r, err
}

func (s *pgSession) SetAutoCommit(on bool) error {
	if on && s.InTx() {
		return errors.New("transaction ouverte : faire un commit ou un rollback avant de repasser en automatique")
	}
	s.auto = on
	return nil
}

func (s *pgSession) AutoCommit() bool { return s.auto }

func (s *pgSession) Commit(ctx context.Context) error {
	_, err := pgQuery(ctx, s.c, "COMMIT")
	return err
}

func (s *pgSession) Rollback(ctx context.Context) error {
	_, err := pgQuery(ctx, s.c, "ROLLBACK")
	return err
}

func (s *pgSession) Close() error { return s.c.Close(context.Background()) }
