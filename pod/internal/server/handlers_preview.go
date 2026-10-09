package server

import (
	"context"
	"encoding/json"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/DrSmithFr/web-ide/pod/internal/agent"
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
func (s *Server) openPreview(ctx context.Context, sp preview.Spec) (preview.State, error) {
	s.previewMu.Lock()
	defer s.previewMu.Unlock()
	if st, ok := s.Previews.Find(sp); ok {
		return st, nil
	}
	if strings.TrimSpace(sp.Command) == "" {
		return preview.State{}, i18n.New("command is missing")
	}
	rt, err := s.openRuntime(sp.Project, sshx.Creds{})
	if err != nil {
		return preview.State{}, err
	}
	dial := previewDialer(rt)
	if listens(ctx, dial, sp.AppPort) {
		// Already running (started by hand): the preview lives while the port answers.
		return s.Previews.Start(sp, "", dial, func() bool { return listens(context.Background(), dial, sp.AppPort) })
	}
	info, err := s.startCommand(rt, rt.P.ID, sp.Command, sp.Cwd)
	if err != nil {
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
func sharePreview(r *agentRun, rt *runtime.Runtime, a toolArgs) (toolResult, error) {
	pv := &agent.Preview{Project: r.project, Title: strings.TrimSpace(a.str("title")), Command: strings.TrimSpace(a.str("command")),
		Cwd: a.str("cwd"), Port: a.num("port")}
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
	res := ok("A card is shown to the user: the app starts when they click it, and opens on a temporary URL. Do not run the command yourself.",
		agent.T("{command} → port {port}", map[string]any{"command": pv.Command, "port": pv.Port}))
	res.Preview = pv
	return res, nil
}
