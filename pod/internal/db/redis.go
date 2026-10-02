package db

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

type redisDriver struct {
	base    redis.Options
	mu      sync.Mutex
	clients map[int]*redis.Client
}

func openRedis(ctx context.Context, c ConnConfig, password string, dial dialFunc) (Driver, error) {
	port := c.Port
	if port == 0 {
		port = 6379
	}
	opts := redis.Options{
		Addr:        net.JoinHostPort(c.Host, strconv.Itoa(port)),
		Username:    c.User,
		Password:    password,
		DB:          c.RedisDB,
		DialTimeout: 10 * time.Second,
		PoolSize:    4,
	}
	if dial != nil {
		opts.Dialer = dial
	}
	d := &redisDriver{base: opts, clients: map[int]*redis.Client{}}
	if err := d.client(c.RedisDB).Ping(ctx).Err(); err != nil {
		d.Close()
		return nil, err
	}
	return d, nil
}

func (d *redisDriver) client(db int) *redis.Client {
	d.mu.Lock()
	defer d.mu.Unlock()
	if c, ok := d.clients[db]; ok {
		return c
	}
	o := d.base
	o.DB = db
	c := redis.NewClient(&o)
	d.clients[db] = c
	return c
}

func dbIndex(name string) int {
	n, _ := strconv.Atoi(strings.TrimPrefix(name, "db"))
	return n
}

func (d *redisDriver) Children(ctx context.Context, n Node) ([]Node, error) {
	c := d.client(d.base.DB)
	switch n.Kind {
	case "":
		count := 16
		if v, err := c.ConfigGet(ctx, "databases").Result(); err == nil {
			if s, ok := v["databases"]; ok {
				count, _ = strconv.Atoi(s)
			}
		}
		keys := map[int]string{}
		if info, err := c.Info(ctx, "keyspace").Result(); err == nil {
			for _, line := range strings.Split(info, "\n") {
				name, rest, ok := strings.Cut(strings.TrimSpace(line), ":")
				if ok && strings.HasPrefix(name, "db") {
					k, _, _ := strings.Cut(strings.TrimPrefix(rest, "keys="), ",")
					keys[dbIndex(name)] = k
				}
			}
		}
		var out []Node
		for i := 0; i < count; i++ {
			k, has := keys[i]
			if !has && i != d.base.DB {
				continue
			}
			detail := "vide"
			if has {
				detail = k + " clés"
			}
			name := "db" + strconv.Itoa(i)
			out = append(out, Node{ID: "db:" + name, Label: name, Kind: "database", Detail: detail, DB: name})
		}
		return out, nil
	case "database":
		dc := d.client(dbIndex(n.DB))
		var keys []string
		var cursor uint64
		for len(keys) < 1000 {
			batch, next, err := dc.Scan(ctx, cursor, "*", 500).Result()
			if err != nil {
				return nil, err
			}
			keys = append(keys, batch...)
			if next == 0 {
				break
			}
			cursor = next
		}
		sort.Strings(keys)
		pipe := dc.Pipeline()
		types := make([]*redis.StatusCmd, len(keys))
		ttls := make([]*redis.DurationCmd, len(keys))
		for i, k := range keys {
			types[i] = pipe.Type(ctx, k)
			ttls[i] = pipe.PTTL(ctx, k)
		}
		_, _ = pipe.Exec(ctx)
		out := make([]Node, 0, len(keys))
		for i, k := range keys {
			detail := types[i].Val()
			if ttl := ttls[i].Val(); ttl > 0 {
				detail += " · TTL " + ttl.Round(time.Second).String()
			}
			out = append(out, Node{ID: "k:" + n.DB + ":" + k, Label: k, Kind: "key", Detail: detail, Leaf: true, DB: n.DB, Table: k})
		}
		return out, nil
	}
	return nil, nil
}

func (d *redisDriver) DDL(context.Context, string, string) (string, error) {
	return "", errors.New("pas de DDL pour Redis")
}

func (d *redisDriver) IndexDef(context.Context, string, string, string) (string, error) {
	return "", errors.New("pas d'index pour Redis")
}

// Page shows the content of a key, paginated for the collection types.
func (d *redisDriver) Page(ctx context.Context, db, key string, offset, limit int) (*Result, error) {
	c := d.client(dbIndex(db))
	start := time.Now()
	typ, err := c.Type(ctx, key).Result()
	if err != nil {
		return nil, err
	}
	res := &Result{Total: -1, Rows: [][]any{}, Command: typ}
	stop := int64(offset + limit - 1)
	switch typ {
	case "string":
		v, err := c.Get(ctx, key).Result()
		if err != nil {
			return nil, err
		}
		res.Columns, res.Rows, res.Total = []string{"valeur"}, [][]any{{v}}, 1
	case "hash":
		m, err := c.HGetAll(ctx, key).Result()
		if err != nil {
			return nil, err
		}
		fields := make([]string, 0, len(m))
		for f := range m {
			fields = append(fields, f)
		}
		sort.Strings(fields)
		res.Columns, res.Total = []string{"champ", "valeur"}, int64(len(fields))
		for _, f := range fields[min(offset, len(fields)):min(offset+limit, len(fields))] {
			res.Rows = append(res.Rows, []any{f, m[f]})
		}
	case "list":
		vals, err := c.LRange(ctx, key, int64(offset), stop).Result()
		if err != nil {
			return nil, err
		}
		res.Columns = []string{"index", "valeur"}
		res.Total, _ = c.LLen(ctx, key).Result()
		for i, v := range vals {
			res.Rows = append(res.Rows, []any{offset + i, v})
		}
	case "set":
		vals, err := c.SMembers(ctx, key).Result()
		if err != nil {
			return nil, err
		}
		sort.Strings(vals)
		res.Columns, res.Total = []string{"membre"}, int64(len(vals))
		for _, v := range vals[min(offset, len(vals)):min(offset+limit, len(vals))] {
			res.Rows = append(res.Rows, []any{v})
		}
	case "zset":
		vals, err := c.ZRangeWithScores(ctx, key, int64(offset), stop).Result()
		if err != nil {
			return nil, err
		}
		res.Columns = []string{"membre", "score"}
		res.Total, _ = c.ZCard(ctx, key).Result()
		for _, z := range vals {
			res.Rows = append(res.Rows, []any{fmt.Sprint(z.Member), z.Score})
		}
	case "stream":
		vals, err := c.XRangeN(ctx, key, "-", "+", int64(offset+limit)).Result()
		if err != nil {
			return nil, err
		}
		res.Columns = []string{"id", "champs"}
		res.Total, _ = c.XLen(ctx, key).Result()
		for _, m := range vals[min(offset, len(vals)):] {
			res.Rows = append(res.Rows, []any{m.ID, fmt.Sprint(m.Values)})
		}
	case "none":
		return nil, errors.New("clé introuvable")
	default:
		return nil, fmt.Errorf("type %s non pris en charge", typ)
	}
	res.DurationMs = ms(start)
	return res, nil
}

func (d *redisDriver) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	for k, c := range d.clients {
		_ = c.Close()
		delete(d.clients, k)
	}
	return nil
}

func (d *redisDriver) Session(_ context.Context, db string) (Session, error) {
	n := d.base.DB
	if db != "" {
		n = dbIndex(db)
	}
	// A dedicated connection keeps SELECT and MULTI / EXEC on the same socket.
	return &redisSession{conn: d.client(n).Conn()}, nil
}

type redisSession struct {
	conn *redis.Conn
}

// splitArgs splits a command line, honoring single and double quotes.
func splitArgs(s string) ([]any, error) {
	var out []any
	var cur strings.Builder
	var quote rune
	in := false
	esc := false
	for _, r := range s {
		switch {
		case esc:
			cur.WriteRune(r)
			esc = false
		case r == '\\' && quote == '"':
			esc = true
		case quote != 0:
			if r == quote {
				quote = 0
			} else {
				cur.WriteRune(r)
			}
		case r == '"' || r == '\'':
			quote, in = r, true
		case r == ' ' || r == '\t' || r == '\n' || r == '\r':
			if in {
				out = append(out, cur.String())
				cur.Reset()
				in = false
			}
		default:
			cur.WriteRune(r)
			in = true
		}
	}
	if quote != 0 {
		return nil, errors.New("guillemet non fermé")
	}
	if in {
		out = append(out, cur.String())
	}
	return out, nil
}

func (s *redisSession) Exec(ctx context.Context, q string) (*Result, error) {
	args, err := splitArgs(strings.TrimSuffix(strings.TrimSpace(q), ";"))
	if err != nil {
		return nil, err
	}
	if len(args) == 0 {
		return nil, errors.New("commande vide")
	}
	start := time.Now()
	name := strings.ToUpper(fmt.Sprint(args[0]))
	v, err := s.conn.Do(ctx, args...).Result()
	if errors.Is(err, redis.Nil) {
		v, err = nil, nil
	}
	if err != nil {
		return nil, err
	}
	res := formatReply(v)
	res.Command = name
	res.DurationMs = ms(start)
	return res, nil
}

func formatReply(v any) *Result {
	res := &Result{Total: -1, Rows: [][]any{}}
	switch t := v.(type) {
	case nil:
		res.Columns, res.Rows = []string{"réponse"}, [][]any{{"(nil)"}}
	case []any:
		res.Columns = []string{"#", "valeur"}
		for i, e := range t {
			if len(res.Rows) >= maxRows {
				res.Truncated = true
				break
			}
			res.Rows = append(res.Rows, []any{i + 1, value(e)})
		}
	case map[any]any:
		res.Columns = []string{"clé", "valeur"}
		keys := make([]string, 0, len(t))
		vals := map[string]any{}
		for k, e := range t {
			ks := fmt.Sprint(k)
			keys = append(keys, ks)
			vals[ks] = e
		}
		sort.Strings(keys)
		for _, k := range keys {
			res.Rows = append(res.Rows, []any{k, value(vals[k])})
		}
	default:
		res.Columns, res.Rows = []string{"réponse"}, [][]any{{value(t)}}
	}
	res.Affected = int64(len(res.Rows))
	return res
}

func (s *redisSession) SetAutoCommit(on bool) error {
	if !on {
		return errors.New("Redis : pas de transaction manuelle (utiliser MULTI / EXEC dans la console)")
	}
	return nil
}
func (s *redisSession) AutoCommit() bool               { return true }
func (s *redisSession) InTx() bool                     { return false }
func (s *redisSession) Commit(context.Context) error   { return nil }
func (s *redisSession) Rollback(context.Context) error { return nil }
func (s *redisSession) Close() error                   { return s.conn.Close() }
