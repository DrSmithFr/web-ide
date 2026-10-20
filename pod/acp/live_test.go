package acp

import (
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestPiContext(t *testing.T) {
	home := t.TempDir()
	old := piHome
	piHome = func() string { return home }
	defer func() { piHome = old }()
	file, _ := filepath.Abs(filepath.Join("testdata", "pi", "sessions", "s1.jsonl"))
	_ = os.MkdirAll(filepath.Join(home, "pi-acp"), 0o755)
	_ = os.WriteFile(filepath.Join(home, "pi-acp", "session-map.json"), []byte(`{"sessions":{"pi-1":{"sessionFile":"`+file+`"}}}`), 0o644)
	if got := piContext("pi-1"); got != 2508 {
		t.Fatalf("context: %d", got)
	}
	if got := piContext("other"); got != 0 {
		t.Fatalf("unknown session: %d", got)
	}
}

func TestOpencodeContext(t *testing.T) {
	path := filepath.Join(t.TempDir(), "opencode.db")
	old := opencodeDB
	opencodeDB = func() string { return path }
	defer func() { opencodeDB = old }()
	if got := opencodeContext("ses_1"); got != 0 {
		t.Fatalf("no database: %d", got)
	}
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`CREATE TABLE message (id text PRIMARY KEY, session_id text NOT NULL, time_created integer NOT NULL, time_updated integer NOT NULL, data text NOT NULL);
		INSERT INTO message VALUES
		('m1', 'ses_1', 1, 1, '{"role":"assistant","tokens":{"total":12000}}'),
		('m2', 'ses_1', 2, 2, '{"role":"assistant","tokens":{"total":15000}}'),
		('m3', 'ses_1', 3, 3, '{"role":"assistant","tokens":{"total":0}}'),
		('m4', 'ses_2', 4, 4, '{"role":"assistant","tokens":{"total":99}}');`)
	db.Close()
	if err != nil {
		t.Fatal(err)
	}
	// The step running has no tokens yet: the last one done counts.
	if got := opencodeContext("ses_1"); got != 15000 {
		t.Fatalf("context: %d", got)
	}
}

func TestHiddenCommands(t *testing.T) {
	names := func(raw json.RawMessage) (out []string) {
		var l []Command
		_ = json.Unmarshal(raw, &l)
		for _, c := range l {
			out = append(out, c.Name)
		}
		return
	}
	got := names(withHidden(json.RawMessage(`[{"name":"init","description":"x"}]`), "opencode"))
	if len(got) != 2 || got[1] != "compact" {
		t.Fatalf("opencode: %v", got)
	}
	if got := names(withHidden(json.RawMessage(`[{"name":"compact","description":"x"}]`), "opencode")); len(got) != 1 {
		t.Fatalf("listed already: %v", got)
	}
	if got := names(withHidden(json.RawMessage(`[{"name":"init","description":"x"}]`), "claude")); len(got) != 1 {
		t.Fatalf("claude: %v", got)
	}
}
