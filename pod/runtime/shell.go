package runtime

import (
	"bytes"
	"context"
	"io"
	"sync"
	"time"

	"antennae.space/ide/lib/i18n"
	"antennae.space/ide/pod/execx"
)

// Output kept of a command run for the assistant: its start and its end.
const (
	execHead = 16 * 1024
	execTail = 48 * 1024
)

// capped keeps the first execHead bytes and the last execTail bytes written to it.
type capped struct {
	mu         sync.Mutex
	head, tail []byte
	total      int
}

func (c *capped) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	n0 := len(p)
	c.total += n0
	if room := execHead - len(c.head); room > 0 {
		n := min(room, len(p))
		c.head = append(c.head, p[:n]...)
		p = p[n:]
	}
	c.tail = append(c.tail, p...)
	if over := len(c.tail) - execTail; over > 0 {
		c.tail = append(c.tail[:0], c.tail[over:]...)
	}
	return n0, nil
}

func (c *capped) String() (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.total <= execHead+execTail {
		return string(c.head) + string(c.tail), false
	}
	var b bytes.Buffer
	b.Write(c.head)
	b.WriteString("\n… (output cut) …\n")
	b.Write(c.tail)
	return b.String(), true
}

// ShellResult is the end of a command run without a terminal.
type ShellResult struct {
	Output     string
	Code       int
	TimedOut   bool
	Canceled   bool
	Truncated  bool
	DurationMs int64
	Cwd        string
}

// RunShell runs a shell command without a terminal (no input): the output (stdout and stderr
// together) and the exit code once it ends, or when the time limit or ctx stops it; progress (if
// any) gets its output so far while it runs.
func (rt *Runtime) RunShell(ctx context.Context, command, cwd string, timeout int, progress func(string)) (ShellResult, error) {
	return rt.RunShellIn(ctx, command, cwd, timeout, progress, nil)
}

// Kept runs a command in the keeper, so that a pod that restarts follows it again
// (FollowShell); Started records the process and its deadline.
type Kept struct {
	Owner   string
	Started func(id string, until time.Time)
	Ended   func(id string) // the result is known: the keeper forgets the process
}

// RunShellIn is RunShell, in the keeper when k is given.
func (rt *Runtime) RunShellIn(ctx context.Context, command, cwd string, timeout int, progress func(string), k *Kept) (ShellResult, error) {
	var err error
	dir := rt.Root
	if cwd != "" {
		if dir, err = rt.Abs(cwd); err != nil {
			return ShellResult{}, err
		}
	}
	limit := time.Duration(min(max(timeout, 1), 1800)) * time.Second
	if timeout <= 0 {
		limit = 120 * time.Second
	}
	start := time.Now()
	argv := []string{"sh", "-c", "{\n" + command + "\n} 2>&1"}
	var proc execx.Process
	if k != nil && rt.Keeper != nil {
		kp, err := execx.StartKeeper(rt.Keeper, k.Owner, rt.KeeperTarget, argv, dir)
		if err != nil {
			return ShellResult{}, err
		}
		k.Started(kp.ID, start.Add(limit))
		proc = kp
		defer k.Ended(kp.ID)
	} else if proc, err = rt.Runner.Start(argv, dir); err != nil {
		return ShellResult{}, err
	}
	proc.Stdin().Close() // no input: a command waiting for one gets end of file
	return waitShell(ctx, proc, start, limit, progress, dir), nil
}

// FollowShell waits again for a command run in the keeper (the pod restarted), until the
// deadline it was given.
func (rt *Runtime) FollowShell(ctx context.Context, id string, started, until time.Time, progress func(string)) (ShellResult, error) {
	if rt.Keeper == nil {
		return ShellResult{}, i18n.New("the command is not reachable anymore")
	}
	proc, err := execx.FollowKeeper(rt.Keeper, id)
	if err != nil {
		return ShellResult{}, err
	}
	return waitShell(ctx, proc, started, max(time.Until(until), time.Second), progress, ""), nil
}

// waitShell gathers the output of a command until it ends, its time limit or ctx.
func waitShell(ctx context.Context, proc execx.Process, start time.Time, limit time.Duration, progress func(string), dir string) ShellResult {
	out := &capped{}
	copied := make(chan struct{})
	go func() {
		_, _ = io.Copy(out, proc.Stdout())
		close(copied)
	}()
	timer := time.NewTimer(limit)
	defer timer.Stop()
	var tick <-chan time.Time
	if progress != nil {
		ticker := time.NewTicker(500 * time.Millisecond)
		defer ticker.Stop()
		tick = ticker.C
	}
	timedOut, canceled := false, false
	// After a kill, a process left in the background may still hold the output open.
	drain := func() {
		select {
		case <-copied:
		case <-time.After(2 * time.Second):
		}
	}
	sent := 0
wait:
	for {
		select {
		case <-copied:
			break wait
		case <-tick:
			out.mu.Lock()
			total := out.total
			out.mu.Unlock()
			if total != sent {
				sent = total
				text, _ := out.String()
				progress(text)
			}
		case <-timer.C:
			timedOut = true
			_ = proc.Kill()
			drain()
			break wait
		case <-ctx.Done():
			canceled = true
			_ = proc.Kill()
			drain()
			break wait
		}
	}
	code := execx.ExitCode(proc.Wait())
	// Read the buffer only once the copy is over (or abandoned).
	text, truncated := out.String()
	return ShellResult{Output: text, Code: code, TimedOut: timedOut, Canceled: canceled, Truncated: truncated, DurationMs: time.Since(start).Milliseconds(), Cwd: dir}
}
