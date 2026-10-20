package previews

import (
	"context"
	"net"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/DrSmithFr/web-ide/lib/i18n"
	"github.com/DrSmithFr/web-ide/pod/assistant"
	"github.com/DrSmithFr/web-ide/pod/commands"
	"github.com/DrSmithFr/web-ide/pod/console"
	"github.com/DrSmithFr/web-ide/pod/plugins"
	"github.com/DrSmithFr/web-ide/pod/preview"
	"github.com/DrSmithFr/web-ide/pod/runtime"
)

// Wait is how long preview.open waits for the app to listen.
var Wait = 60 * time.Second

// openMu serializes preview.open (one command per app).
var openMu sync.Mutex

// dialer reaches the ports of the machine of a project.
func dialer(rt *runtime.Runtime) preview.Dialer {
	if rt.Local {
		return (&net.Dialer{}).DialContext
	}
	return func(ctx context.Context, network, addr string) (net.Conn, error) { return rt.Dial(ctx, addr) }
}

// consoleRuns tells whether a console of a project still runs its command.
func consoleRuns(rt *runtime.Runtime, id string) bool {
	for _, c := range rt.Consoles.List() {
		if c.ID == id {
			return !c.Exited
		}
	}
	return false
}

// listens tells whether the app answers on its port.
func listens(ctx context.Context, dial preview.Dialer, port int) bool {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	c, err := dial(ctx, "tcp", "127.0.0.1:"+strconv.Itoa(port))
	if err != nil {
		return false
	}
	_ = c.Close()
	return true
}

// open starts the command of an app (unless its port answers already), waits for its port,
// then serves it. A saved command (CommandID) runs in its own console, one per command: a
// console of it still running is the one of the preview.
func open(ctx context.Context, pod plugins.Pod, sp preview.Spec) (preview.State, error) {
	openMu.Lock()
	defer openMu.Unlock()
	rt, err := pod.Runtime(sp.Project)
	if err != nil {
		return preview.State{}, err
	}
	var saved *commands.Command
	if sp.CommandID != "" {
		list, err := commands.Load(rt.FS, rt.IdeDir)
		if err != nil {
			return preview.State{}, err
		}
		cmd, ok := commands.Find(list, sp.CommandID)
		if !ok {
			return preview.State{}, i18n.Errorf("command not found: %s", sp.CommandID)
		}
		saved = &cmd
		sp.Command, sp.Cwd = cmd.Command, cmd.Cwd
		if sp.AppPort == 0 {
			sp.AppPort = cmd.Port
		}
		if strings.TrimSpace(sp.Title) == "" {
			sp.Title = cmd.Name
		}
		if sp.AppPort == 0 {
			return preview.State{}, i18n.Errorf("the command “%s” has no port", cmd.Name)
		}
	}
	pv := pod.Previews()
	if st, ok := pv.Find(sp); ok {
		return st, nil
	}
	if strings.TrimSpace(sp.Command) == "" {
		return preview.State{}, i18n.New("command is missing")
	}
	dial := dialer(rt)
	if listens(ctx, dial, sp.AppPort) {
		// Already running (started by hand): the preview lives while the port answers.
		return pv.Start(sp, "", dial, func() bool { return listens(context.Background(), dial, sp.AppPort) })
	}
	emit := func(event string, data any, except string) { pod.Emit(rt.P.ID, event, data) }
	var info console.Info
	if saved != nil {
		r, err := commands.RunSaved(rt.Consoles, rt.Root, *saved, false, emit, "")
		if err != nil {
			return preview.State{}, err
		}
		if r.Running != nil {
			info = *r.Running
		} else {
			info = *r.Console
		}
	} else if info, err = start(rt, sp.Command, sp.Cwd); err != nil {
		return preview.State{}, err
	} else {
		pod.Emit(rt.P.ID, "console.created", info)
	}
	until := time.Now().Add(Wait)
	for !listens(ctx, dial, sp.AppPort) {
		if !consoleRuns(rt, info.ID) {
			_, buf, _ := rt.Consoles.Attach(info.ID)
			return preview.State{}, i18n.Errorf("the command ended before listening on port %d: %s", sp.AppPort, assistant.Tail(assistant.PlainOutput(string(buf)), 5, 400))
		}
		if time.Now().After(until) || ctx.Err() != nil {
			return preview.State{}, i18n.Errorf("nothing listens on port %d after %d s (see the console)", sp.AppPort, int(Wait/time.Second))
		}
		time.Sleep(300 * time.Millisecond)
	}
	return pv.Start(sp, info.ID, dial, func() bool { return consoleRuns(rt, info.ID) })
}

// start runs a command as a task console of the project.
func start(rt *runtime.Runtime, command, cwd string) (console.Info, error) {
	title := command
	if r := []rune(title); len(r) > 60 {
		title = string(r[:57]) + "…"
	}
	if cwd = strings.TrimSpace(cwd); cwd != "" {
		base := rt.Root
		if strings.HasPrefix(cwd, "/") {
			base = ""
		}
		cwd = path.Clean("/" + base + "/" + cwd)
	}
	return rt.Consoles.Create("task", title, []string{"sh", "-c", command}, cwd, 160, 40)
}
