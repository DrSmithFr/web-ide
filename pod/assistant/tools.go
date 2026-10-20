package assistant

// The kit of the tools of the assistant (the AI plugin) and of the MCP endpoint (the server):
// their arguments, their results, the paths given by a model.

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path"
	"strconv"
	"strings"
	"time"
)

// ToolResult is the result of a tool of the assistant or of the MCP: the text read by the model
// and what the page shows.
type ToolResult struct {
	Content string
	Failure string // usage, exit or error (see Message)
	Term    string // output with its colors (see Message)
	Exit    *int
	Summary json.RawMessage
	Status  string
	Diff    []DiffLine
	Page    *Page
	Preview *Preview
	Child   string
	Card    *ActionCard
	Opened  string
}

// OK is a result whose summary is translated by the page.
func OK(content string, summary Text) ToolResult {
	return ToolResult{Content: content, Summary: summary.Raw(), Status: "ok"}
}

// OKPlain is a result with a summary already in the language of the user.
func OKPlain(content, summary string) ToolResult {
	return ToolResult{Content: content, Summary: Plain(summary), Status: "ok"}
}

// UsageError: the call itself is wrong (a parameter missing or invalid, an unknown tool…), as
// counted by the statistics.
type UsageError struct{ Msg string }

func (e UsageError) Error() string { return e.Msg }

func Usagef(format string, a ...any) error { return UsageError{fmt.Sprintf(format, a...)} }

// Args of a tool call, read field by field.
type Args map[string]json.RawMessage

func (a Args) Str(k string) string {
	var s string
	if json.Unmarshal(a[k], &s) != nil {
		var n json.Number
		if json.Unmarshal(a[k], &n) == nil {
			return n.String()
		}
	}
	return s
}

func (a Args) Num(k string) int {
	var f float64
	if json.Unmarshal(a[k], &f) == nil {
		return int(f)
	}
	var s string
	if json.Unmarshal(a[k], &s) == nil {
		var n int
		fmt.Sscan(s, &n)
		return n
	}
	return 0
}

func (a Args) Bool(k string, def bool) bool {
	var b bool
	if json.Unmarshal(a[k], &b) == nil {
		return b
	}
	return def
}

func (a Args) Has(k string) bool { return len(a[k]) > 0 && string(a[k]) != "null" }

// AbsPath is the absolute path of a path given by the model, relative to the root.
func AbsPath(root, p string) string {
	p = strings.TrimSpace(p)
	base := root
	if strings.HasPrefix(p, "/") {
		base = ""
	}
	return path.Clean("/" + base + "/" + p)
}

// RelPath is p relative to root ("." for the root).
func RelPath(root, p string) string {
	if p == root {
		return "."
	}
	return strings.TrimPrefix(p, strings.TrimSuffix(root, "/")+"/")
}

func (a Args) Strs(k string) []string {
	var out []string
	_ = json.Unmarshal(a[k], &out)
	return out
}

func (a Args) OptStr(k string) *string {
	if !a.Has(k) {
		return nil
	}
	v := a.Str(k)
	if v == "" {
		return nil
	}
	return &v
}

// links reads the lineage fields of a call: parent (0 takes it out) and depends_on.
// NewID is a new id of a conversation, a message or a request: time-ordered.
func NewID() string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return strconv.FormatInt(time.Now().UnixMilli(), 36) + hex.EncodeToString(b)[:6]
}

// Cut keeps the start and the end of s within n characters.
func Cut(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n/2] + "\n… (" + strconv.Itoa(len(s)-n) + " characters cut) …\n" + s[len(s)-n/2:]
}

// MaxImage is the largest image read from the project; ImageTypes the types by extension.
const MaxImage = 10 << 20

var ImageTypes = map[string]string{".png": "image/png", ".jpg": "image/jpeg", ".jpeg": "image/jpeg", ".webp": "image/webp", ".gif": "image/gif", ".svg": "image/svg+xml"}

// CommandWait is how long run_saved_command waits (20 s by default, 600 at most).
func CommandWait(timeout int) time.Time {
	if timeout <= 0 {
		timeout = 20
	}
	return time.Now().Add(time.Duration(min(timeout, 600)) * time.Second)
}
