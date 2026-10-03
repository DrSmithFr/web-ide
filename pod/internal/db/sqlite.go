package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"github.com/DrSmithFr/web-ide/pod/internal/execx"
)

// querier runs one statement. Local SQLite uses database/sql, a file on an SSH host
// goes through the sqlite3 command line of that host.
type querier func(ctx context.Context, q string) (*Result, error)

type sqliteDriver struct {
	db     *sql.DB      // local file
	runner execx.Runner // remote file
	path   string
}

func openSQLite(ctx context.Context, path string, local bool, runner execx.Runner) (Driver, error) {
	if !local {
		if !runner.Has("sqlite3") {
			return nil, errors.New("sqlite3 n'est pas installé sur l'hôte distant")
		}
		d := &sqliteDriver{runner: runner, path: path}
		_, err := d.cli(ctx, "SELECT 1")
		return d, err
	}
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=busy_timeout(3000)")
	if err != nil {
		return nil, err
	}
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, err
	}
	return &sqliteDriver{db: db, path: path}, nil
}

func (d *sqliteDriver) query(ctx context.Context, q string) (*Result, error) {
	if d.db == nil {
		return d.cli(ctx, q)
	}
	return sqlQuery(ctx, d.db, q)
}

type sqlRunner interface {
	QueryContext(ctx context.Context, q string, args ...any) (*sql.Rows, error)
	ExecContext(ctx context.Context, q string, args ...any) (sql.Result, error)
}

func returnsRows(q string) bool {
	switch firstWord(q) {
	case "SELECT", "WITH", "PRAGMA", "EXPLAIN", "VALUES", "SHOW", "TABLE":
		return true
	}
	return strings.Contains(strings.ToUpper(q), "RETURNING")
}

func sqlQuery(ctx context.Context, db sqlRunner, q string) (*Result, error) {
	start := time.Now()
	res := &Result{Total: -1, Command: firstWord(q)}
	if !returnsRows(q) {
		r, err := db.ExecContext(ctx, q)
		if err != nil {
			return nil, err
		}
		res.Affected, _ = r.RowsAffected()
		res.DurationMs = ms(start)
		return res, nil
	}
	rows, err := db.QueryContext(ctx, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	cols, _ := rows.Columns()
	res.Columns = cols
	res.Rows = [][]any{}
	for rows.Next() {
		if len(res.Rows) >= maxRows {
			res.Truncated = true
			break
		}
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, err
		}
		for i, v := range vals {
			vals[i] = value(v)
		}
		res.Rows = append(res.Rows, vals)
	}
	res.Affected = int64(len(res.Rows))
	res.DurationMs = ms(start)
	return res, rows.Err()
}

func ms(start time.Time) float64 { return float64(time.Since(start).Microseconds()) / 1000 }

const nullMark = "\x01NULL\x01"

func (d *sqliteDriver) cli(ctx context.Context, q string) (*Result, error) {
	start := time.Now()
	out, err := d.runner.Output(ctx, []string{"sqlite3", "-batch", "-bail", "-header", "-ascii", "-nullvalue", nullMark, "-cmd", ".timeout 3000", d.path, q}, "")
	if err != nil {
		return nil, fmt.Errorf("sqlite3 : %w", err)
	}
	res := &Result{Total: -1, Command: firstWord(q), Rows: [][]any{}}
	text := strings.TrimSuffix(string(out), "\x1e")
	if text != "" {
		lines := strings.Split(text, "\x1e")
		res.Columns = strings.Split(lines[0], "\x1f")
		for _, l := range lines[1:] {
			if len(res.Rows) >= maxRows {
				res.Truncated = true
				break
			}
			cells := strings.Split(l, "\x1f")
			row := make([]any, len(cells))
			for i, c := range cells {
				if c == nullMark {
					row[i] = nil
				} else {
					row[i] = c
				}
			}
			res.Rows = append(res.Rows, row)
		}
		res.Affected = int64(len(res.Rows))
	}
	res.DurationMs = ms(start)
	return res, nil
}

func (d *sqliteDriver) Children(ctx context.Context, n Node) ([]Node, error) {
	switch n.Kind {
	case "":
		return []Node{{ID: "db:main", Label: "main", Kind: "database", DB: "main"}}, nil
	case "database":
		r, err := d.query(ctx, "SELECT name, type FROM sqlite_master WHERE type IN ('table','view') AND name NOT LIKE 'sqlite_%' ORDER BY name")
		if err != nil {
			return nil, err
		}
		var out []Node
		for _, row := range r.Rows {
			name, kind := fmt.Sprint(row[0]), fmt.Sprint(row[1])
			out = append(out, Node{ID: "t:" + name, Label: name, Kind: kind, DB: "main", Table: name})
		}
		return out, nil
	case "table", "view":
		cols, err := d.query(ctx, "SELECT name, type, \"notnull\", pk FROM pragma_table_info("+quoteLit(n.Table)+")")
		if err != nil {
			return nil, err
		}
		var out []Node
		for _, row := range cols.Rows {
			detail := strings.ToLower(fmt.Sprint(row[1]))
			if fmt.Sprint(row[3]) != "0" {
				detail += " · clé primaire"
			}
			if fmt.Sprint(row[2]) == "1" {
				detail += " · not null"
			}
			out = append(out, Node{ID: "c:" + n.Table + "." + fmt.Sprint(row[0]), Label: fmt.Sprint(row[0]), Kind: "column", Detail: detail, Leaf: true, DB: "main", Table: n.Table})
		}
		idx, err := d.query(ctx, "SELECT name FROM sqlite_master WHERE type='index' AND tbl_name="+quoteLit(n.Table)+" ORDER BY name")
		if err == nil {
			for _, row := range idx.Rows {
				out = append(out, Node{ID: "i:" + fmt.Sprint(row[0]), Label: fmt.Sprint(row[0]), Kind: "index", Leaf: true, DB: "main", Table: n.Table})
			}
		}
		return out, nil
	}
	return nil, nil
}

func (d *sqliteDriver) master(ctx context.Context, name string) (string, error) {
	r, err := d.query(ctx, "SELECT sql FROM sqlite_master WHERE name="+quoteLit(name))
	if err != nil {
		return "", err
	}
	if len(r.Rows) == 0 || r.Rows[0][0] == nil {
		return "-- définition automatique (pas de SQL stocké)", nil
	}
	return fmt.Sprint(r.Rows[0][0]) + ";", nil
}

func (d *sqliteDriver) DDL(ctx context.Context, _, table string) (string, error) {
	return d.master(ctx, table)
}

func (d *sqliteDriver) IndexDef(ctx context.Context, _, _, index string) (string, error) {
	return d.master(ctx, index)
}

func (d *sqliteDriver) Page(ctx context.Context, _, table string, offset, limit int) (*Result, error) {
	r, err := d.query(ctx, fmt.Sprintf("SELECT * FROM %s LIMIT %d OFFSET %d", quoteIdent(table), limit, offset))
	if err != nil {
		return nil, err
	}
	if c, err := d.query(ctx, "SELECT count(*) FROM "+quoteIdent(table)); err == nil && len(c.Rows) == 1 {
		r.Total, _ = strconv.ParseInt(fmt.Sprint(c.Rows[0][0]), 10, 64)
	}
	return r, nil
}

func (d *sqliteDriver) Close() error {
	if d.db != nil {
		return d.db.Close()
	}
	return nil
}

func (d *sqliteDriver) Session(ctx context.Context, _ string) (Session, error) {
	if d.db == nil {
		return &cliSession{d: d}, nil
	}
	conn, err := d.db.Conn(ctx)
	if err != nil {
		return nil, err
	}
	return &sqlSession{conn: conn, auto: true}, nil
}

// sqlSession keeps one connection so that a manual transaction spans several statements.
type sqlSession struct {
	conn *sql.Conn
	auto bool
	inTx bool
}

func (s *sqlSession) Exec(ctx context.Context, q string) (*Result, error) {
	w := firstWord(q)
	if !s.auto && !s.inTx && w != "BEGIN" && w != "COMMIT" && w != "ROLLBACK" && w != "END" {
		if _, err := s.conn.ExecContext(ctx, "BEGIN"); err != nil {
			return nil, err
		}
		s.inTx = true
	}
	r, err := sqlQuery(ctx, s.conn, q)
	switch w {
	case "BEGIN":
		s.inTx = err == nil || s.inTx
	case "COMMIT", "END", "ROLLBACK":
		if err == nil {
			s.inTx = false
		}
	}
	if r != nil {
		r.InTx = s.inTx
	}
	return r, err
}

func (s *sqlSession) SetAutoCommit(on bool) error {
	if on && s.inTx {
		return errors.New("transaction ouverte : faire un commit ou un rollback avant de repasser en automatique")
	}
	s.auto = on
	return nil
}

func (s *sqlSession) AutoCommit() bool { return s.auto }
func (s *sqlSession) InTx() bool       { return s.inTx }

func (s *sqlSession) Commit(ctx context.Context) error {
	_, err := s.conn.ExecContext(ctx, "COMMIT")
	if err == nil {
		s.inTx = false
	}
	return err
}

func (s *sqlSession) Rollback(ctx context.Context) error {
	_, err := s.conn.ExecContext(ctx, "ROLLBACK")
	if err == nil {
		s.inTx = false
	}
	return err
}

func (s *sqlSession) Close() error {
	if s.inTx {
		_, _ = s.conn.ExecContext(context.Background(), "ROLLBACK")
	}
	return s.conn.Close()
}

// cliSession: each statement is its own sqlite3 process, so autocommit only.
type cliSession struct{ d *sqliteDriver }

func (s *cliSession) Exec(ctx context.Context, q string) (*Result, error) { return s.d.cli(ctx, q) }
func (s *cliSession) SetAutoCommit(on bool) error {
	if !on {
		return errors.New("SQLite distant (sqlite3 en ligne de commande) : transactions manuelles non disponibles")
	}
	return nil
}
func (s *cliSession) AutoCommit() bool               { return true }
func (s *cliSession) InTx() bool                     { return false }
func (s *cliSession) Commit(context.Context) error   { return nil }
func (s *cliSession) Rollback(context.Context) error { return nil }
func (s *cliSession) Close() error                   { return nil }
