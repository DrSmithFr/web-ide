package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/DrSmithFr/web-ide/pod/internal/agent"
	"github.com/DrSmithFr/web-ide/pod/internal/commands"
	"github.com/DrSmithFr/web-ide/pod/internal/console"
	"github.com/DrSmithFr/web-ide/pod/internal/i18n"
	"github.com/DrSmithFr/web-ide/pod/internal/preview"
	"github.com/DrSmithFr/web-ide/pod/internal/runtime"
	"github.com/DrSmithFr/web-ide/pod/internal/sshx"
)

// PreviewWait is how long preview.open waits for the app to listen.
var PreviewWait = 60 * time.Second

// previewDialer reaches the ports of the machine of a project.
func previewDialer(rt *runtime.Runtime) preview.Dialer {
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

// openPreview starts the command of an app (unless its port answers already), waits for
// its port, then serves it.
// A saved command (CommandID) runs in its own console, one per command: a console of it
// still running is the one of the preview.
func (s *Server) openPreview(ctx context.Context, sp preview.Spec) (preview.State, error) {
	s.previewMu.Lock()
	defer s.previewMu.Unlock()
	rt, err := s.openRuntime(sp.Project, sshx.Creds{})
	if err != nil {
		return preview.State{}, err
	}
	var saved *commands.Command
	if sp.CommandID != "" {
		cmd, err := s.findCommand(rt, sp.CommandID)
		if err != nil {
			return preview.State{}, err
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
	if st, ok := s.Previews.Find(sp); ok {
		return st, nil
	}
	if strings.TrimSpace(sp.Command) == "" {
		return preview.State{}, i18n.New("command is missing")
	}
	dial := previewDialer(rt)
	if listens(ctx, dial, sp.AppPort) {
		// Already running (started by hand): the preview lives while the port answers.
		return s.Previews.Start(sp, "", dial, func() bool { return listens(context.Background(), dial, sp.AppPort) })
	}
	var info console.Info
	if saved != nil {
		r, err := s.runSaved(rt, *saved, false, "")
		if err != nil {
			return preview.State{}, err
		}
		if r.Running != nil {
			info = *r.Running
		} else {
			info = *r.Console
		}
	} else if info, err = s.startCommand(rt, rt.P.ID, sp.Command, sp.Cwd); err != nil {
		return preview.State{}, err
	}
	until := time.Now().Add(PreviewWait)
	for !listens(ctx, dial, sp.AppPort) {
		if !consoleRuns(rt, info.ID) {
			text, _, _ := consoleText(rt, info.ID)
			return preview.State{}, i18n.Errorf("the command ended before listening on port %d: %s", sp.AppPort, agent.Tail(text, 5, 400))
		}
		if time.Now().After(until) || ctx.Err() != nil {
			return preview.State{}, i18n.Errorf("nothing listens on port %d after %d s (see the console)", sp.AppPort, int(PreviewWait/time.Second))
		}
		time.Sleep(300 * time.Millisecond)
	}
	return s.Previews.Start(sp, info.ID, dial, func() bool { return consoleRuns(rt, info.ID) })
}

func (s *Server) registerPreviews() {
	s.handle("preview.open", func(ctx context.Context, c *Client, p json.RawMessage) (any, error) {
		sp, err := bind[preview.Spec](p)
		if err != nil {
			return nil, err
		}
		if sp.Project == "" {
			sp.Project = c.project
		}
		return s.openPreview(ctx, sp)
	})
	s.handle("preview.list", func(ctx context.Context, c *Client, p json.RawMessage) (any, error) {
		return s.Previews.List(), nil
	})
	s.handle("preview.public", func(ctx context.Context, c *Client, p json.RawMessage) (any, error) {
		a, err := bind[struct {
			ID     string `json:"id"`
			Public bool   `json:"public"`
		}](p)
		if err != nil {
			return nil, err
		}
		return s.Previews.SetPublic(a.ID, a.Public)
	})
	// preview.close stops the preview and the command it started.
	s.handle("preview.close", func(ctx context.Context, c *Client, p json.RawMessage) (any, error) {
		a, err := bind[struct {
			ID string `json:"id"`
		}](p)
		if err != nil {
			return nil, err
		}
		var st *preview.State
		for _, x := range s.Previews.List() {
			if x.ID == a.ID {
				st = &x
			}
		}
		if st == nil {
			return nil, nil
		}
		s.Previews.Close(a.ID)
		s.mu.Lock()
		rt := s.runtimes[st.Project]
		s.mu.Unlock()
		if rt != nil && st.Console != "" && rt.Consoles.Close(st.Console) == nil {
			s.emitter(st.Project)("console.closed", map[string]string{"id": st.Console}, "")
		}
		return nil, nil
	})
}

// sharePreview is the tool share_preview: a card in the conversation, nothing runs yet.
// The app is a saved serve command when there is one: command_id, else the only one with a
// port when no command is given, else the one whose text is the command.
func (s *Server) sharePreview(r *agentRun, rt *runtime.Runtime, a toolArgs) (toolResult, error) {
	pv := &agent.Preview{Project: r.project, Title: strings.TrimSpace(a.str("title")), Command: strings.TrimSpace(a.str("command")),
		Cwd: a.str("cwd"), Port: a.num("port"), CommandID: strings.TrimSpace(a.str("command_id"))}
	list, err := s.projectCommands(rt)
	if err != nil && (pv.CommandID != "" || pv.Command == "") {
		return toolResult{}, err
	}
	var saved *commands.Command
	switch {
	case pv.CommandID != "":
		cmd, ok := commands.Find(list, pv.CommandID)
		if !ok {
			return toolResult{}, usagef("no saved command %q (see list_commands)", pv.CommandID)
		}
		saved = &cmd
	case pv.Command == "":
		var serve []string
		for _, c := range commands.OfFlag(list, "serve") {
			if c.Port > 0 || pv.Port > 0 {
				serve = append(serve, fmt.Sprintf("%s (%s)", c.ID, c.Name))
				if saved == nil {
					saved = &c
				}
			}
		}
		if len(serve) == 0 {
			return toolResult{}, usagef("command is missing (the project has no saved serve command with a port)")
		}
		if len(serve) > 1 {
			return toolResult{}, usagef("several saved serve commands: pass command_id, one of %s", strings.Join(serve, ", "))
		}
	default:
		for _, c := range commands.OfFlag(list, "serve") {
			if strings.TrimSpace(c.Command) == pv.Command && (pv.Port == 0 || c.Port == 0 || pv.Port == c.Port) && saved == nil {
				saved = &c
			}
		}
	}
	if saved != nil {
		pv.CommandID, pv.Command, pv.Cwd = saved.ID, saved.Command, saved.Cwd
		if pv.Port == 0 {
			pv.Port = saved.Port
		}
		if pv.Title == "" {
			pv.Title = saved.Name
		}
	}
	if pv.Command == "" {
		return toolResult{}, usagef("command is missing")
	}
	if pv.Port < 1 || pv.Port > 65535 {
		return toolResult{}, usagef("port must go from 1 to 65535")
	}
	if pv.Cwd != "" {
		if pv.Cwd = relPath(rt.Root, absPath(rt.Root, pv.Cwd)); pv.Cwd == "." {
			pv.Cwd = ""
		}
	}
	if pv.Title == "" {
		pv.Title = pv.Command
	}
	what := "the command"
	sum := pv.Command
	if saved != nil {
		what = fmt.Sprintf("the saved command %q (%s)", saved.Name, saved.ID)
		sum = saved.Name
	}
	res := ok(fmt.Sprintf("A card is shown to the user: %s starts when they click it (port %d), and the app opens on a temporary URL. Do not run the command yourself.", what, pv.Port),
		agent.T("{command} → port {port}", map[string]any{"command": sum, "port": pv.Port}))
	res.Preview = pv
	return res, nil
}
