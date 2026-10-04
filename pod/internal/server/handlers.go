package server

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/DrSmithFr/web-ide/pod/internal/config"
	"github.com/DrSmithFr/web-ide/pod/internal/fsx"
	"github.com/DrSmithFr/web-ide/pod/internal/i18n"
	"github.com/DrSmithFr/web-ide/pod/internal/lsp"
	"github.com/DrSmithFr/web-ide/pod/internal/projects"
	"github.com/DrSmithFr/web-ide/pod/internal/runtime"
	"github.com/DrSmithFr/web-ide/pod/internal/search"
	"github.com/DrSmithFr/web-ide/pod/internal/sshx"
)

func bind[T any](p json.RawMessage) (T, error) {
	var v T
	if len(p) == 0 || string(p) == "null" {
		return v, nil
	}
	err := json.Unmarshal(p, &v)
	return v, err
}

func (c *Client) runtime() (*runtime.Runtime, error) {
	c.srv.mu.Lock()
	defer c.srv.mu.Unlock()
	rt := c.srv.runtimes[c.project]
	if rt == nil {
		return nil, i18n.New("no project open on this connection")
	}
	return rt, nil
}

// ---------- global: workspace, projects, settings ----------

func (s *Server) registerGlobal() {
	// The window tells its language: the messages sent to it are translated.
	s.handle("client.lang", func(ctx context.Context, c *Client, p json.RawMessage) (any, error) {
		a, err := bind[struct{ Lang string }](p)
		if err != nil {
			return nil, err
		}
		if i18n.Supported(a.Lang) {
			c.lang.Store(a.Lang)
		}
		return nil, nil
	})
	s.handle("workspace.get", func(ctx context.Context, c *Client, p json.RawMessage) (any, error) {
		home, _ := os.UserHomeDir()
		return map[string]string{"workspace": s.Cfg.Workspace, "home": home, "dataDir": s.Store.Dir()}, nil
	})
	s.handle("workspace.set", func(ctx context.Context, c *Client, p json.RawMessage) (any, error) {
		a, err := bind[struct{ Path string }](p)
		if err != nil {
			return nil, err
		}
		dir := config.ExpandHome(strings.TrimSpace(a.Path))
		if st, err := os.Stat(dir); err != nil || !st.IsDir() {
			return nil, i18n.Errorf("folder not found: %s", dir)
		}
		s.Cfg.Workspace = dir
		return nil, s.Cfg.Save(s.Store)
	})
	// Local folders, for the project creation form.
	s.handle("workspace.dirs", func(ctx context.Context, c *Client, p json.RawMessage) (any, error) {
		a, err := bind[struct{ Path string }](p)
		if err != nil {
			return nil, err
		}
		dir := config.ExpandHome(a.Path)
		if dir == "" {
			dir = s.Cfg.Workspace
		}
		dir, _ = filepath.Abs(dir)
		es, err := fsx.Local{}.List(dir)
		if err != nil {
			return nil, err
		}
		out := []fsx.Entry{}
		for _, e := range es {
			if e.Dir && !strings.HasPrefix(e.Name, ".") {
				out = append(out, e)
			}
		}
		return map[string]any{"path": dir, "parent": filepath.Dir(dir), "dirs": out}, nil
	})
	s.handle("ssh.info", func(ctx context.Context, c *Client, p json.RawMessage) (any, error) {
		var hosts []sshx.ConfigHost
		for _, h := range sshx.ConfigHosts() {
			hosts = append(hosts, h)
		}
		return map[string]any{"hosts": hosts, "keys": sshx.LocalKeys(), "agent": os.Getenv("SSH_AUTH_SOCK") != ""}, nil
	})

	s.handle("projects.list", func(ctx context.Context, c *Client, p json.RawMessage) (any, error) {
		return s.Projects.List(), nil
	})
	s.handle("projects.create", func(ctx context.Context, c *Client, p json.RawMessage) (any, error) {
		a, err := bind[projects.Project](p)
		if err != nil {
			return nil, err
		}
		g, _ := bind[struct{ Remote string }](p)
		a.Parent, a.Ticket, a.GitSetup = "", 0, &projects.GitSetup{Remote: strings.TrimSpace(g.Remote)}
		if a.Type == "local" {
			a.Path = config.ExpandHome(a.Path)
			if st, err := os.Stat(a.Path); err != nil || !st.IsDir() {
				return nil, i18n.Errorf("folder not found: %s", a.Path)
			}
		}
		v, err := s.Projects.Create(a)
		if err != nil {
			return nil, err
		}
		// Every project is a git repository. An SSH host not reachable without a password
		// gets it at the first opening.
		var gitErr string
		if rt, err := s.openRuntime(v.ID, sshx.Creds{}); err == nil {
			if err := s.gitSetup(ctx, rt); err != nil {
				gitErr = err.Error()
			}
		}
		s.broadcast("projects.changed", nil, nil)
		return struct {
			projects.View
			GitError string `json:"gitError,omitempty"`
		}{v, gitErr}, nil
	})
	s.handle("projects.update", func(ctx context.Context, c *Client, p json.RawMessage) (any, error) {
		a, err := bind[projects.Project](p)
		if err != nil {
			return nil, err
		}
		v, err := s.Projects.Update(a)
		if err == nil {
			s.closeRuntime(a.ID)
			s.broadcast("projects.changed", nil, nil)
		}
		return v, err
	})
	s.handle("projects.delete", func(ctx context.Context, c *Client, p json.RawMessage) (any, error) {
		a, err := bind[struct{ ID string }](p)
		if err != nil {
			return nil, err
		}
		children := s.Projects.Children(a.ID)
		if err := s.Projects.Delete(a.ID); err != nil {
			return nil, err
		}
		s.closeRuntime(a.ID)
		s.Sessions.Delete(a.ID)
		_ = s.Store.Remove(iconCache(a.ID))
		// The worktrees of its tickets stay on disk, their projects go.
		for _, id := range children {
			_ = s.Projects.Delete(id)
			s.closeRuntime(id)
			s.Sessions.Delete(id)
		}
		s.broadcast("projects.changed", nil, nil)
		return nil, nil
	})

	s.handle("settings.get", func(ctx context.Context, c *Client, p json.RawMessage) (any, error) {
		return s.Settings.Current(), nil
	})
	s.handle("settings.save", func(ctx context.Context, c *Client, p json.RawMessage) (any, error) {
		a, err := bind[struct {
			Settings json.RawMessage
			Label    string
		}](p)
		if err != nil {
			return nil, err
		}
		e, err := s.Settings.Save(a.Settings, a.Label)
		if err == nil {
			s.broadcast("settings.changed", e, c)
		}
		return e, err
	})
	s.handle("settings.history", func(ctx context.Context, c *Client, p json.RawMessage) (any, error) {
		return s.Settings.History(), nil
	})
	s.handle("settings.snapshot", func(ctx context.Context, c *Client, p json.RawMessage) (any, error) {
		a, _ := bind[struct{ Label string }](p)
		return s.Settings.Snapshot(a.Label), nil
	})
	s.handle("settings.rollback", func(ctx context.Context, c *Client, p json.RawMessage) (any, error) {
		a, err := bind[struct{ ID int }](p)
		if err != nil {
			return nil, err
		}
		e, err := s.Settings.Rollback(a.ID)
		if err == nil {
			s.broadcast("settings.changed", e, nil)
		}
		return e, err
	})
}

func (s *Server) closeRuntime(id string) {
	s.mu.Lock()
	rt := s.runtimes[id]
	delete(s.runtimes, id)
	for c := range s.clients {
		if c.project == id {
			c.project = ""
			go c.push("project.closed", map[string]string{"id": id})
		}
	}
	s.mu.Unlock()
	if rt != nil {
		go rt.Close()
	}
}

// gitSetup prepares the repository of a project created with a pending git setup.
func (s *Server) gitSetup(ctx context.Context, rt *runtime.Runtime) error {
	p, ok := s.Projects.Get(rt.P.ID)
	if !ok || p.GitSetup == nil {
		return nil
	}
	empty := true
	if es, err := rt.FS.List(rt.Root); err == nil {
		for _, e := range es {
			if e.Name != ".git" && e.Name != ".ide" {
				empty = false
			}
		}
	}
	if err := rt.Git.Setup(ctx, p.GitSetup.Remote, empty); err != nil {
		return err
	}
	s.Projects.SetGitSetup(p.ID, nil)
	return nil
}

func (s *Server) openRuntime(id string, creds sshx.Creds) (*runtime.Runtime, error) {
	s.mu.Lock()
	if rt := s.runtimes[id]; rt != nil {
		s.mu.Unlock()
		return rt, nil
	}
	lock := s.opening[id]
	if lock == nil {
		lock = &sync.Mutex{}
		s.opening[id] = lock
	}
	s.mu.Unlock()
	lock.Lock()
	defer lock.Unlock()
	s.mu.Lock()
	if rt := s.runtimes[id]; rt != nil {
		s.mu.Unlock()
		return rt, nil
	}
	s.mu.Unlock()
	p, ok := s.Projects.Get(id)
	if !ok {
		return nil, i18n.New("project not found")
	}
	rt, err := runtime.Open(*p, creds, runtime.Deps{Pool: s.Pool, Store: s.Store}, s.emitter(id))
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	s.runtimes[id] = rt
	s.mu.Unlock()
	return rt, nil
}

// ---------- project ----------

type pathArg struct {
	Path string `json:"path"`
}

func (s *Server) registerProject() {
	s.handle("project.open", func(ctx context.Context, c *Client, p json.RawMessage) (any, error) {
		a, err := bind[struct {
			ID    string
			Creds sshx.Creds
		}](p)
		if err != nil {
			return nil, err
		}
		rt, err := s.openRuntime(a.ID, a.Creds)
		if err != nil {
			return nil, err
		}
		if err := s.gitSetup(ctx, rt); err != nil {
			log.Printf("git setup of %s: %v", a.ID, err) // the Git panel still offers the init
		}
		s.openTunnels(rt)
		s.mu.Lock()
		prev := s.runtimes[c.project]
		same := c.project == a.ID
		c.project = a.ID
		s.mu.Unlock()
		if !same {
			if prev != nil {
				prev.Detach()
			}
			rt.Attach()
		}
		s.Projects.Touch(a.ID)
		v, _ := s.Projects.Get(a.ID)
		return map[string]any{
			"project":  projects.View{Project: v, DisplayName: v.Name()},
			"root":     rt.Root,
			"local":    rt.Local,
			"session":  s.Sessions.Get(a.ID),
			"consoles": rt.Consoles.List(),
		}, nil
	})
	s.handle("session.update", func(ctx context.Context, c *Client, p json.RawMessage) (any, error) {
		a, err := bind[struct{ Session json.RawMessage }](p)
		if err != nil {
			return nil, err
		}
		if c.project == "" {
			return nil, i18n.New("no open project")
		}
		s.Sessions.Put(c.project, a.Session)
		s.emitter(c.project)("session.changed", a.Session, c.id)
		return nil, nil
	})

	withRT := func(f func(ctx context.Context, c *Client, rt *runtime.Runtime, p json.RawMessage) (any, error)) handler {
		return func(ctx context.Context, c *Client, p json.RawMessage) (any, error) {
			rt, err := c.runtime()
			if err != nil {
				return nil, err
			}
			return f(ctx, c, rt, p)
		}
	}
	withPath := func(f func(ctx context.Context, c *Client, rt *runtime.Runtime, path string, p json.RawMessage) (any, error)) handler {
		return withRT(func(ctx context.Context, c *Client, rt *runtime.Runtime, p json.RawMessage) (any, error) {
			a, err := bind[pathArg](p)
			if err != nil {
				return nil, err
			}
			abs, err := rt.Abs(a.Path)
			if err != nil {
				return nil, err
			}
			return f(ctx, c, rt, abs, p)
		})
	}

	s.handle("fs.list", withPath(func(ctx context.Context, c *Client, rt *runtime.Runtime, path string, p json.RawMessage) (any, error) {
		return rt.List(path)
	}))
	s.handle("fs.read", withPath(func(ctx context.Context, c *Client, rt *runtime.Runtime, path string, p json.RawMessage) (any, error) {
		return rt.Read(path)
	}))
	s.handle("fs.stat", withPath(func(ctx context.Context, c *Client, rt *runtime.Runtime, path string, p json.RawMessage) (any, error) {
		return rt.FS.Stat(path)
	}))
	s.handle("fs.write", withPath(func(ctx context.Context, c *Client, rt *runtime.Runtime, path string, p json.RawMessage) (any, error) {
		a, err := bind[struct {
			Content string
			runtime.Format
		}](p)
		if err != nil {
			return nil, err
		}
		rev, err := rt.Write(path, a.Content, a.Format, c.id)
		return map[string]int{"rev": rev}, err
	}))
	s.handle("fs.create", withPath(func(ctx context.Context, c *Client, rt *runtime.Runtime, path string, p json.RawMessage) (any, error) {
		a, err := bind[struct{ Dir bool }](p)
		if err != nil {
			return nil, err
		}
		return nil, rt.Create(path, a.Dir)
	}))
	s.handle("fs.delete", withPath(func(ctx context.Context, c *Client, rt *runtime.Runtime, path string, p json.RawMessage) (any, error) {
		return nil, rt.Delete(path)
	}))
	s.handle("fs.rename", withPath(func(ctx context.Context, c *Client, rt *runtime.Runtime, path string, p json.RawMessage) (any, error) {
		a, err := bind[struct{ To string }](p)
		if err != nil {
			return nil, err
		}
		to, _ := rt.Abs(a.To)
		return nil, rt.Rename(path, to)
	}))
	s.handle("buffer.sync", withPath(func(ctx context.Context, c *Client, rt *runtime.Runtime, path string, p json.RawMessage) (any, error) {
		a, err := bind[struct{ Content *string }](p)
		if err != nil {
			return nil, err
		}
		rt.SyncBuffer(path, a.Content, c.id)
		return nil, nil
	}))
	s.handle("fs.related", withPath(func(ctx context.Context, c *Client, rt *runtime.Runtime, path string, p json.RawMessage) (any, error) {
		return rt.FindRelated(ctx, path)
	}))

	s.handle("search.grep", withRT(func(ctx context.Context, c *Client, rt *runtime.Runtime, p json.RawMessage) (any, error) {
		o, err := bind[search.Options](p)
		if err != nil {
			return nil, err
		}
		if o.Query == "" {
			return search.Result{Matches: []search.Match{}}, nil
		}
		o.Exclude = rt.Excluded()
		if rt.Local {
			return search.Local(ctx, rt.Root, o)
		}
		return search.Remote(ctx, rt.Runner, rt.Root, o)
	}))
	s.handle("search.files", withRT(func(ctx context.Context, c *Client, rt *runtime.Runtime, p json.RawMessage) (any, error) {
		files, err := rt.Files(ctx)
		return search.WithoutExcluded(files, rt.Excluded()), err
	}))
	s.handle("folders.get", withRT(func(ctx context.Context, c *Client, rt *runtime.Runtime, p json.RawMessage) (any, error) {
		return rt.Folders(), nil
	}))
	s.handle("folders.mark", withPath(func(ctx context.Context, c *Client, rt *runtime.Runtime, path string, p json.RawMessage) (any, error) {
		a, err := bind[struct{ Mark string }](p)
		if err != nil {
			return nil, err
		}
		m, err := rt.MarkFolder(path, a.Mark)
		if err == nil {
			s.emitter(c.project)("folders.changed", m, "")
		}
		return m, err
	}))

	s.handle("console.list", withRT(func(ctx context.Context, c *Client, rt *runtime.Runtime, p json.RawMessage) (any, error) {
		return rt.Consoles.List(), nil
	}))
	s.handle("console.create", withRT(func(ctx context.Context, c *Client, rt *runtime.Runtime, p json.RawMessage) (any, error) {
		a, err := bind[struct {
			Kind, Title, Cwd string
			Command          []string
			Cols, Rows       int
		}](p)
		if err != nil {
			return nil, err
		}
		info, err := rt.Consoles.Create(a.Kind, a.Title, a.Command, a.Cwd, a.Cols, a.Rows)
		if err == nil {
			s.emitter(c.project)("console.created", info, c.id)
		}
		return info, err
	}))
	s.handle("console.attach", withRT(func(ctx context.Context, c *Client, rt *runtime.Runtime, p json.RawMessage) (any, error) {
		a, err := bind[struct{ ID string }](p)
		if err != nil {
			return nil, err
		}
		info, buf, err := rt.Consoles.Attach(a.ID)
		if err != nil {
			return nil, err
		}
		return map[string]any{"info": info, "data": base64.StdEncoding.EncodeToString(buf)}, nil
	}))
	s.handle("console.input", withRT(func(ctx context.Context, c *Client, rt *runtime.Runtime, p json.RawMessage) (any, error) {
		a, err := bind[struct{ ID, Data string }](p)
		if err != nil {
			return nil, err
		}
		return nil, rt.Consoles.Input(a.ID, []byte(a.Data))
	}))
	s.handle("console.resize", withRT(func(ctx context.Context, c *Client, rt *runtime.Runtime, p json.RawMessage) (any, error) {
		a, err := bind[struct {
			ID         string
			Cols, Rows int
		}](p)
		if err != nil {
			return nil, err
		}
		return nil, rt.Consoles.Resize(a.ID, a.Cols, a.Rows)
	}))
	s.handle("console.rename", withRT(func(ctx context.Context, c *Client, rt *runtime.Runtime, p json.RawMessage) (any, error) {
		a, err := bind[struct{ ID, Title string }](p)
		if err != nil {
			return nil, err
		}
		err = rt.Consoles.Rename(a.ID, a.Title)
		if err == nil {
			s.emitter(c.project)("console.renamed", map[string]string{"id": a.ID, "title": a.Title}, "")
		}
		return nil, err
	}))
	s.handle("console.close", withRT(func(ctx context.Context, c *Client, rt *runtime.Runtime, p json.RawMessage) (any, error) {
		a, err := bind[struct{ ID string }](p)
		if err != nil {
			return nil, err
		}
		err = rt.Consoles.Close(a.ID)
		if err == nil {
			s.emitter(c.project)("console.closed", map[string]string{"id": a.ID}, "")
		}
		return nil, err
	}))

	s.handle("lsp.status", withRT(func(ctx context.Context, c *Client, rt *runtime.Runtime, p json.RawMessage) (any, error) {
		return rt.LSP.Status(), nil
	}))
	s.handle("lsp.langOf", func(ctx context.Context, c *Client, p json.RawMessage) (any, error) {
		a, err := bind[pathArg](p)
		return lsp.LangOf(a.Path), err
	})
	s.handle("lsp.capabilities", withRT(func(ctx context.Context, c *Client, rt *runtime.Runtime, p json.RawMessage) (any, error) {
		a, err := bind[struct{ Lang string }](p)
		if err != nil {
			return nil, err
		}
		return rt.LSP.Capabilities(a.Lang)
	}))
	s.handle("lsp.request", withRT(func(ctx context.Context, c *Client, rt *runtime.Runtime, p json.RawMessage) (any, error) {
		a, err := bind[struct {
			Lang, Method string
			Params       json.RawMessage
		}](p)
		if err != nil {
			return nil, err
		}
		return rt.LSP.Request(ctx, a.Lang, a.Method, a.Params)
	}))
	s.handle("lsp.notify", withRT(func(ctx context.Context, c *Client, rt *runtime.Runtime, p json.RawMessage) (any, error) {
		a, err := bind[struct {
			Lang, Method string
			Params       json.RawMessage
		}](p)
		if err != nil {
			return nil, err
		}
		return nil, rt.LSP.Notify(a.Lang, a.Method, a.Params)
	}))
}
