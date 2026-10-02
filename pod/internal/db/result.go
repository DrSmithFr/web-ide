package db

import (
	"context"
	"encoding/hex"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

const maxRows = 1000

type Result struct {
	Columns    []string `json:"columns"`
	Rows       [][]any  `json:"rows"`
	Affected   int64    `json:"affected"`
	Command    string   `json:"command,omitempty"`
	DurationMs float64  `json:"durationMs"`
	Truncated  bool     `json:"truncated,omitempty"`
	Total      int64    `json:"total"` // -1 when unknown
	InTx       bool     `json:"inTx"`
	Message    string   `json:"message,omitempty"`
}

// Node is one entry of the database tree.
type Node struct {
	ID     string `json:"id"`
	Label  string `json:"label"`
	Kind   string `json:"kind"` // database | table | view | column | index | key
	Detail string `json:"detail,omitempty"`
	Leaf   bool   `json:"leaf"`
	DB     string `json:"db,omitempty"`
	Table  string `json:"table,omitempty"`
}

type Driver interface {
	// Children lists the tree under node ("" for the connection root).
	Children(ctx context.Context, node Node) ([]Node, error)
	DDL(ctx context.Context, db, table string) (string, error)
	IndexDef(ctx context.Context, db, table, index string) (string, error)
	Page(ctx context.Context, db, table string, offset, limit int) (*Result, error)
	Session(ctx context.Context, db string) (Session, error)
	Close() error
}

type Session interface {
	Exec(ctx context.Context, q string) (*Result, error)
	SetAutoCommit(on bool) error
	AutoCommit() bool
	Commit(ctx context.Context) error
	Rollback(ctx context.Context) error
	InTx() bool
	Close() error
}

// value turns a driver value into something JSON can carry.
func value(v any) any {
	switch t := v.(type) {
	case nil:
		return nil
	case []byte:
		if utf8.Valid(t) {
			return string(t)
		}
		if len(t) > 256 {
			return fmt.Sprintf("\\x%s… (%d octets)", hex.EncodeToString(t[:256]), len(t))
		}
		return "\\x" + hex.EncodeToString(t)
	case time.Time:
		return t.Format(time.RFC3339Nano)
	case string, bool, int64, int32, int, float64, float32:
		return t
	default:
		return fmt.Sprint(t)
	}
}

func firstWord(q string) string {
	q = strings.TrimSpace(stripComments(q))
	end := strings.IndexFunc(q, func(r rune) bool { return r == ' ' || r == '\n' || r == '\t' || r == '(' || r == ';' })
	if end < 0 {
		end = len(q)
	}
	return strings.ToUpper(q[:end])
}

func stripComments(q string) string {
	for {
		q = strings.TrimSpace(q)
		switch {
		case strings.HasPrefix(q, "--"):
			if i := strings.IndexByte(q, '\n'); i >= 0 {
				q = q[i+1:]
				continue
			}
			return ""
		case strings.HasPrefix(q, "/*"):
			if i := strings.Index(q, "*/"); i >= 0 {
				q = q[i+2:]
				continue
			}
			return ""
		}
		return q
	}
}

// readOnly tells whether a statement leaves the data unchanged (the result cache survives it).
func readOnly(q string) bool {
	switch firstWord(q) {
	case "SELECT", "WITH", "SHOW", "EXPLAIN", "PRAGMA", "VALUES", "TABLE",
		"GET", "MGET", "HGET", "HGETALL", "HMGET", "HKEYS", "HVALS", "HLEN", "LRANGE", "LLEN", "SMEMBERS", "SCARD",
		"ZRANGE", "ZCARD", "ZSCORE", "XRANGE", "XLEN", "SCAN", "HSCAN", "SSCAN", "ZSCAN", "KEYS", "TYPE", "TTL", "PTTL",
		"EXISTS", "STRLEN", "INFO", "DBSIZE", "PING":
		return true
	}
	return false
}

func quoteIdent(s string) string { return `"` + strings.ReplaceAll(s, `"`, `""`) + `"` }

func quoteLit(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }
