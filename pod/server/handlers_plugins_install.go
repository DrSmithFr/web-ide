package server

import (
	"context"
	"encoding/json"
	"log"
	"os"
	"slices"

	"github.com/DrSmithFr/web-ide/lib/i18n"
	"github.com/DrSmithFr/web-ide/pod/plugins"
	"github.com/DrSmithFr/web-ide/pod/plugins/install"
	"github.com/DrSmithFr/web-ide/pod/runtime"
)

// The external plugins of the pod (docs/design/plugins.md, *Phase 3*): resolved from their git
// repository, installed with the grants the user accepted, updated, removed. Listed, not
// runnable yet. Pod-wide: the lock is the pod's, each project says which ones it uses.

// installer is the one of the pod, made at registration, with the plugins installed now.
func (s *Server) installer() *install.Installer {
	in := *s.plugins.installer
	in.Installed = s.Plugins.Versions()
	return &in
}

func (s *Server) registerPluginInstall() {
	s.plugins.installer = &install.Installer{Store: s.Store, Resolver: install.Resolver{IDE: s.Version, Builtin: s.Plugins.Builtin,
		AllowHTTP: os.Getenv("WEBIDE_TEST_PLUGINS") != ""}}
	s.loadExternals()

	type resolveArgs struct {
		URL        string `json:"url"`
		Constraint string `json:"constraint"`
	}
	// plugins.resolve: the version an install would take, with what the grants dialog shows.
	s.handle("plugins.resolve", func(ctx context.Context, c *Client, p json.RawMessage) (any, error) {
		a, err := bind[resolveArgs](p)
		if err != nil {
			return nil, err
		}
		cand, err := s.resolvePlugin(ctx, a.URL, a.Constraint)
		if err != nil {
			return nil, err
		}
		return candidateView(cand, nil), nil
	})
	// plugins.install: the version resolved (version pins the one the dialog showed), with the
	// grants the user accepted.
	s.handle("plugins.install", func(ctx context.Context, c *Client, p json.RawMessage) (any, error) {
		a, err := bind[struct {
			resolveArgs
			Version string   `json:"version"`
			Grants  []string `json:"grants"`
		}](p)
		if err != nil {
			return nil, err
		}
		pin := a.Constraint
		if a.Version != "" {
			pin = "=" + a.Version
		}
		s.plugins.installMu.Lock()
		defer s.plugins.installMu.Unlock()
		cand, err := s.resolvePlugin(ctx, a.URL, pin)
		if err != nil {
			return nil, err
		}
		if _, ok := s.Plugins.External(cand.Manifest.ID); ok {
			return nil, i18n.Errorf("the plugin %s is installed already: update it", cand.Manifest.ID)
		}
		return s.installPlugin(ctx, cand, a.Constraint, a.Grants)
	})
	// plugins.update: the highest version of its constraint. A version asking a permission not
	// granted yet answers {grants}: the page asks the user, then calls again with them.
	s.handle("plugins.update", func(ctx context.Context, c *Client, p json.RawMessage) (any, error) {
		a, err := bind[struct {
			ID     string   `json:"id"`
			Grants []string `json:"grants"`
		}](p)
		if err != nil {
			return nil, err
		}
		s.plugins.installMu.Lock()
		defer s.plugins.installMu.Unlock()
		lock, err := install.ReadLock(s.Store)
		if err != nil {
			return nil, err
		}
		l, ok := lock.Plugins[a.ID]
		if !ok {
			return nil, i18n.Errorf("the plugin %s is not installed", a.ID)
		}
		cand, err := s.resolvePlugin(ctx, l.URL, l.Constraint)
		if err != nil {
			return nil, err
		}
		if cand.Version.String() == l.Version {
			return map[string]any{"upToDate": true, "version": l.Version}, nil
		}
		asked := install.NewGrants(cand.Manifest, l.Grants)
		if len(asked) > 0 && a.Grants == nil {
			return candidateView(cand, asked), nil
		}
		grants := []string{}
		for _, g := range cand.Manifest.Permissions {
			if slices.Contains(l.Grants, g) || slices.Contains(a.Grants, g) {
				grants = append(grants, g)
			}
		}
		return s.installPlugin(ctx, cand, l.Constraint, grants)
	})
	s.handle("plugins.remove", func(ctx context.Context, c *Client, p json.RawMessage) (any, error) {
		a, err := bind[struct {
			ID string `json:"id"`
		}](p)
		if err != nil {
			return nil, err
		}
		s.plugins.installMu.Lock()
		defer s.plugins.installMu.Unlock()
		if err := s.installer().Remove(a.ID); err != nil {
			return nil, err
		}
		s.Plugins.RemoveExternal(a.ID)
		s.externalsChanged()
		return nil, nil
	})
}

// resolvePlugin resolves a version of a repository, which must fit beside the built-ins.
func (s *Server) resolvePlugin(ctx context.Context, url, constraint string) (install.Candidate, error) {
	cand, err := s.installer().Resolve(ctx, url, constraint)
	if err != nil {
		return cand, err
	}
	if err := s.Plugins.CheckExternal(cand.Manifest); err != nil {
		return cand, i18n.Errorf("%s cannot be installed: %v", cand.Manifest.ID, err)
	}
	return cand, nil
}

func (s *Server) installPlugin(ctx context.Context, cand install.Candidate, constraint string, grants []string) (any, error) {
	l, err := s.installer().Install(ctx, cand, constraint, grants)
	if err != nil {
		return nil, err
	}
	if err := s.Plugins.SetExternal(plugins.External{Manifest: cand.Manifest, URL: l.URL, Version: l.Version, Grants: l.Grants}); err != nil {
		return nil, err
	}
	s.externalsChanged()
	return map[string]any{"id": cand.Manifest.ID, "version": l.Version, "grants": l.Grants}, nil
}

// candidateView is a resolved version as the page shows it: asked are the permissions to
// accept (all of them for an install).
func candidateView(c install.Candidate, asked []string) map[string]any {
	if asked == nil {
		asked = c.Manifest.Permissions
	}
	if asked == nil {
		asked = []string{}
	}
	return map[string]any{"url": c.URL, "tag": c.Tag, "version": c.Version.String(), "manifest": c.Manifest, "grants": asked}
}

// loadExternals reads the lock at start: the installed versions join the registry.
func (s *Server) loadExternals() {
	lock, err := install.ReadLock(s.Store)
	if err != nil {
		log.Printf("plugins: %s: %v", install.LockFile, err)
		return
	}
	for id, l := range lock.Plugins {
		m, err := s.plugins.installer.Manifest(id, l.Version)
		if err == nil {
			err = s.Plugins.SetExternal(plugins.External{Manifest: m, URL: l.URL, Version: l.Version, Grants: l.Grants})
		}
		if err != nil {
			log.Printf("plugins: %s %s: %v", id, l.Version, err)
		}
	}
}

// externalsChanged tells every open project its plugins changed.
func (s *Server) externalsChanged() {
	s.mu.Lock()
	roots := map[string]bool{}
	var rts []*runtime.Runtime
	for _, rt := range s.runtimes {
		if rt != nil && !roots[mainID(rt)] {
			roots[mainID(rt)] = true
			rts = append(rts, rt)
		}
	}
	s.mu.Unlock()
	for _, rt := range rts {
		s.emitFamily(mainID(rt), "plugins.changed", map[string]any{"plugins": s.pluginStates(rt)})
	}
}
