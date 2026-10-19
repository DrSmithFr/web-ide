package server

import (
	"context"
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/DrSmithFr/web-ide/pod/internal/config"
	"github.com/DrSmithFr/web-ide/pod/internal/desks"
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

// ---------- global: projects folder, projects, settings ----------

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
	// workspace.get, .set and .dirs are the projects folder (the answer field is still
	// "workspace"): the other pods of an account, of another release, and their pages call them.
	s.handle("workspace.get", func(ctx context.Context, c *Client, p json.RawMessage) (any, error) {
		home, _ := os.UserHomeDir()
		return map[string]string{"workspace": s.Cfg.Projects, "home": home, "dataDir": s.Store.Dir(), "version": s.Version, "name": s.Name}, nil
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
		s.Cfg.Projects = dir
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
			dir = s.Cfg.Projects
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
		s.Desks.Delete(a.ID)
		_ = s.Store.Remove(iconCache(a.ID))
		// The worktrees of its tickets stay on disk, their projects go.
		for _, id := range children {
			_ = s.Projects.Delete(id)
			s.closeRuntime(id)
			s.Desks.Delete(id)
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
			// The change is written to the other pods of the account (the merge keeps the fields
			// the other side changed).
			if s.Sync != nil {
				if err := s.Sync.PushAll(); err != nil {
					log.Printf("settings sync: %v", err)
				}
			}
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
	s.handle("clipboard.list", func(ctx context.Context, c *Client, p json.RawMessage) (any, error) {
		return s.Clipboard.List(), nil
	})
	s.handle("clipboard.add", func(ctx context.Context, c *Client, p json.RawMessage) (any, error) {
		a, err := bind[struct {
			Text string
			Max  int
		}](p)
		if err != nil {
			return nil, err
		}
		l, changed := s.Clipboard.Add(a.Text, a.Max)
		if changed {
			s.broadcast("clipboard.changed", l, nil)
		}
		return nil, nil
	})
	s.handle("clipboard.remove", func(ctx context.Context, c *Client, p json.RawMessage) (any, error) {
		a, err := bind[struct{ Text string }](p)
		if err != nil {
			return nil, err
		}
		s.broadcast("clipboard.changed", s.Clipboard.Remove(a.Text), nil)
		return nil, nil
	})
	s.handle("clipboard.clear", func(ctx context.Context, c *Client, p json.RawMessage) (any, error) {
		s.broadcast("clipboard.changed", s.Clipboard.Clear(), nil)
		return nil, nil
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
		if c.sees(id) {
			if c.project == id {
				c.project = ""
			}
			delete(c.attached, id)
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
	deps := runtime.Deps{Pool: s.Pool, Store: s.Store, Keeper: s.Keeper, Version: s.Version, AgentsMCP: s.agentsMCP()}
	if main, ok := s.Projects.Get(p.Parent); ok && p.Parent != "" {
		deps.SettingsRoot = main.Path
	}
	rt, err := runtime.Open(*p, creds, deps, s.emitter(id))
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
			Desk  int
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
		if s.capabilityOn(rt, "previews") {
			s.openForwards(rt)
		}
		// The setup command of the kanban settings moves to the saved commands, once.
		_, _ = s.projectCommands(rt)
		s.mu.Lock()
		prev := s.runtimes[c.project]
		same := c.project == a.ID
		held := c.attached[a.ID] // already counted as an attached worktree
		delete(c.attached, a.ID)
		c.project = a.ID
		// A desk removed meanwhile (an old link): the window opens desk 1.
		desk := a.Desk
		if !s.Desks.HasDesk(a.ID, desk) {
			desk = 1
		}
		c.desk = desk
		s.mu.Unlock()
		if !same {
			if prev != nil {
				prev.Detach()
			}
			if !held {
				rt.Attach()
			}
		}
		s.Projects.Touch(a.ID)
		v, _ := s.Projects.Get(a.ID)
		if v.Parent == "" {
			// The home pages of every pod sort the projects by their last opening.
			s.broadcast("projects.changed", nil, nil)
		}
		return map[string]any{
			"project":  projects.View{Project: v, DisplayName: v.Name()},
			"root":     rt.Root,
			"local":    rt.Local,
			"session":  s.Desks.Get(desks.Key(a.ID, desk)),
			"desk":     desk,
			"desks":    s.Desks.List(a.ID),
			"consoles": rt.Consoles.List(),
			"plugins":  s.pluginStates(rt),
			// Files of .ide still tracked by git: the page offers to untrack them once.
			"ideTracked": s.ideTracked(ctx, v, rt),
		}, nil
	})
	// session.update, session.get, session.put and the session.changed event carry the state of
	// a desk (the field "session" too): the old name stays on the wire, a browser can be served
	// the page of a pod of another release.
	s.handle("session.update", func(ctx context.Context, c *Client, p json.RawMessage) (any, error) {
		a, err := bind[struct{ Session json.RawMessage }](p)
		if err != nil {
			return nil, err
		}
		if c.project == "" {
			return nil, i18n.New("no open project")
		}
		s.mu.Lock()
		desk := c.desk
		s.mu.Unlock()
		// A desk removed by another window is not written back.
		if !s.Desks.HasDesk(c.project, desk) {
			return nil, nil
		}
		s.Desks.Put(desks.Key(c.project, desk), a.Session)
		s.emitDesk(c.project, desk, "session.changed", map[string]any{"session": a.Session, "desk": desk}, c.id)
		return nil, nil
	})
	s.registerDesks()

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
	// An image of the project as a data URL: the pictures of a rendered Markdown file.
	s.handle("fs.image", withPath(func(ctx context.Context, c *Client, rt *runtime.Runtime, path string, p json.RawMessage) (any, error) {
		return readImage(rt.FS, path)
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
