package acp

import (
	"bufio"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

// pi and opencode tell their context (usage_update) only when a session opens and when a turn
// ends: during a turn, the context is read where they keep the tokens of each step.
var liveContext = map[string]func(session string) int64{"pi": piContext, "opencode": opencodeContext}

// liveEvery is the least time between two reads (ms).
const liveEvery = 2000

// readLive reads the context of the turn running, at most every liveEvery, off the lock of the
// session; the count read is applied on its queue (under s.mu).
func (s *Session) readLive() {
	read := liveContext[s.info.Agent]
	if read == nil || s.turn == nil || s.info.Usage == nil || now()-s.liveAt < liveEvery {
		return
	}
	s.liveAt = now()
	gen, session := s.gen, s.acp
	go func() {
		used := read(session)
		if used <= 0 {
			return
		}
		s.q.push(func() {
			s.mu.Lock()
			defer s.mu.Unlock()
			if gen != s.gen || s.closed || s.turn == nil || s.info.Usage == nil || s.info.Usage.Used == used {
				return
			}
			s.info.Usage = &Usage{Used: used, Size: s.info.Usage.Size}
			s.changed()
		})
	}()
}

// piContext is the context of the last answer of a pi session: its tokens, in the session file.
func piContext(session string) int64 {
	f, err := os.Open(piSessionFile(session))
	if err != nil {
		return 0
	}
	defer f.Close()
	var used int64
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 16<<20)
	for sc.Scan() {
		var l struct {
			Type    string `json:"type"`
			Message struct {
				Role  string `json:"role"`
				Usage *struct {
					Input      int64 `json:"input"`
					Output     int64 `json:"output"`
					CacheRead  int64 `json:"cacheRead"`
					CacheWrite int64 `json:"cacheWrite"`
				} `json:"usage"`
			} `json:"message"`
		}
		if json.Unmarshal(sc.Bytes(), &l) != nil || l.Type != "message" || l.Message.Role != "assistant" || l.Message.Usage == nil {
			continue
		}
		u := l.Message.Usage
		if t := u.Input + u.Output + u.CacheRead + u.CacheWrite; t > 0 {
			used = t
		}
	}
	return used
}

// opencodeDB is the database of opencode (a variable for the tests).
var opencodeDB = func() string {
	dir := os.Getenv("XDG_DATA_HOME")
	if dir == "" {
		h, _ := os.UserHomeDir()
		dir = filepath.Join(h, ".local", "share")
	}
	return filepath.Join(dir, "opencode", "opencode.db")
}

// opencodeContext is the context of the last step of an opencode session: the tokens of its last
// assistant message (one per step), in the database of opencode, read only.
func opencodeContext(session string) int64 {
	path := opencodeDB()
	if _, err := os.Stat(path); err != nil {
		return 0
	}
	db, err := sql.Open("sqlite", "file:"+path+"?mode=ro&_pragma=busy_timeout(1000)")
	if err != nil {
		return 0
	}
	defer db.Close()
	rows, err := db.Query(`SELECT data FROM message WHERE session_id = ? ORDER BY time_created DESC LIMIT 10`, session)
	if err != nil {
		return 0
	}
	defer rows.Close()
	for rows.Next() {
		var data string
		var m struct {
			Role   string `json:"role"`
			Tokens struct {
				Total int64 `json:"total"`
			} `json:"tokens"`
		}
		if rows.Scan(&data) == nil && json.Unmarshal([]byte(data), &m) == nil && m.Role == "assistant" && m.Tokens.Total > 0 {
			return m.Tokens.Total
		}
	}
	return 0
}
