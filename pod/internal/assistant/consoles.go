package assistant

// The console tools shared by the assistant and the MCP.

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/DrSmithFr/web-ide/pod/internal/runtime"
)

// ListConsoles lists the consoles of the project of rt.
func ListConsoles(rt *runtime.Runtime) ToolResult {
	list := rt.Consoles.List()
	var rows []string
	for _, c := range list {
		kind := "terminal"
		if c.Kind == "task" {
			kind = "command"
		}
		state := " · running"
		if c.Exited {
			state = fmt.Sprintf(" · exited (code %d)", c.Code)
		}
		rows = append(rows, fmt.Sprintf("%s · %s · %s%s", c.ID, kind, c.Title, state))
	}
	text := strings.Join(rows, "\n")
	if text == "" {
		text = "No open console."
	}
	return OK(text, Tn(len(list), "{n} console", "{n} consoles", nil))
}

// ReadConsole is the end of the output of a console.
func ReadConsole(rt *runtime.Runtime, id string, lines int) (ToolResult, error) {
	if id == "" {
		return ToolResult{}, Usagef("console_id is missing")
	}
	text, info, err := ConsoleText(rt, id)
	if err != nil {
		return ToolResult{}, err
	}
	if lines <= 0 {
		lines = 200
	}
	state, sum := "running", T("running", nil)
	if info.Exited {
		state = fmt.Sprintf("exited (code %d)", info.Code)
		sum = T("exited (code {code})", map[string]any{"code": info.Code})
	}
	out := Tail(text, lines, 12000)
	if out == "" {
		out = "(no output)"
	}
	return OK(fmt.Sprintf("Console \"%s\", %s:\n%s", info.Title, state, out), sum.With(info.Title+" · ", "")), nil
}

// ConsoleInput types text in a console and reads its output after.
func ConsoleInput(ctx context.Context, rt *runtime.Runtime, id, text string, enter bool) (ToolResult, error) {
	if text != "" || enter {
		data := text
		if enter {
			data += "\r"
		}
		if err := rt.Consoles.Input(id, []byte(data)); err != nil {
			return ToolResult{}, fmt.Errorf("console not found: %s", id)
		}
	}
	select {
	case <-time.After(800 * time.Millisecond):
	case <-ctx.Done():
	}
	out, _, err := ConsoleText(rt, id)
	if err != nil {
		return ToolResult{}, err
	}
	return OK("Text sent. End of the output:\n"+Tail(out, 40, 12000), T("typed in {id}", map[string]any{"id": id})), nil
}

// ConsoleText is the output of a console without its colors, and its state.
func ConsoleText(rt *runtime.Runtime, id string) (string, *ConsoleInfo, error) {
	info, buf, err := rt.Consoles.Attach(id)
	if err != nil {
		return "", nil, err
	}
	return PlainOutput(string(buf)), &ConsoleInfo{Title: info.Title, Exited: info.Exited, Code: info.Code}, nil
}

// ConsoleInfo is the state of a console.
type ConsoleInfo struct {
	Title  string
	Exited bool
	Code   int
}

// FollowConsole waits until the command of a console ends, its deadline or ctx, showing its
// output meanwhile (progress may be nil); answers its output and state.
func FollowConsole(ctx context.Context, rt *runtime.Runtime, id string, until time.Time, progress func(string)) (string, *ConsoleInfo, error) {
	var last string
	shown := time.Now()
	for {
		text, now, err := ConsoleText(rt, id)
		if err != nil {
			return "", nil, err
		}
		if now.Exited || time.Now().After(until) || ctx.Err() != nil {
			return text, now, nil
		}
		if progress != nil && time.Since(shown) >= 500*time.Millisecond && text != last {
			progress(text)
			shown, last = time.Now(), text
		}
		time.Sleep(150 * time.Millisecond)
	}
}

// ConsoleResult is what the model reads of a command followed in a console.
func ConsoleResult(command, console, text string, now *ConsoleInfo) ToolResult {
	out := Tail(text, 300, 12000)
	if !now.Exited {
		return OK(fmt.Sprintf("The command is still running (console %s). Output so far:\n%s\n\nFollow it with read_console, interact with console_input.", console, out), T("{command}: still running", map[string]any{"command": command}))
	}
	if out == "" {
		out = "(no output)"
	}
	tr := ToolResult{Content: fmt.Sprintf("Exit code: %d (console %s)\n%s", now.Code, console, out), Summary: Plain(fmt.Sprintf("code %d", now.Code)), Status: "ok"}
	if now.Code != 0 {
		tr.Status, tr.Failure = "error", "exit"
	}
	return tr
}
