package server

import (
	"context"
	"encoding/json"
	"path"
	"strings"

	"github.com/DrSmithFr/web-ide/pod/internal/fsx"
	"github.com/DrSmithFr/web-ide/pod/internal/i18n"
	"github.com/DrSmithFr/web-ide/pod/internal/projects"
)

// Project icons: the page draws them (glyph or text on a shape), the pod keeps the image
// and its description in <project>/.ide/icon.svg and icon.json, plus a copy in
// ~/.web-ide/icons/<id>.svg for the home page (an SSH project is not reached to list it).
// The worktree of a ticket shows the icon of its parent.

const maxIcon = 64 << 10

type iconData struct {
	Spec json.RawMessage `json:"spec"`
	SVG  string          `json:"svg"`
}

// iconOwner is the project whose icon a project shows: itself, or the parent of a worktree.
func (s *Server) iconOwner(id string) (*projects.Project, error) {
	p, ok := s.Projects.Get(id)
	if !ok {
		return nil, i18n.New("project not found")
	}
	if p.Parent != "" {
		if parent, ok := s.Projects.Get(p.Parent); ok {
			return parent, nil
		}
	}
	return p, nil
}

// iconFS gives the file system holding the .ide folder of p: the local disk, or the SSH
// host when the project is open. ok is false when the folder is not reachable now.
func (s *Server) iconFS(p *projects.Project) (fs fsx.FS, dir string, ok bool) {
	if p.Type == "local" {
		return fsx.Local{}, path.Join(p.Path, ".ide"), true
	}
	s.mu.Lock()
	rt := s.runtimes[p.ID]
	s.mu.Unlock()
	if rt == nil {
		return nil, "", false
	}
	return rt.FS, path.Join(rt.Root, ".ide"), true
}

func iconCache(id string) string { return path.Join("icons", id+".svg") }

func (s *Server) readIcon(p *projects.Project) iconData {
	var d iconData
	if fs, dir, ok := s.iconFS(p); ok {
		if svg, err := fs.Read(path.Join(dir, "icon.svg")); err == nil && len(svg) <= maxIcon {
			d.SVG = string(svg)
			if spec, err := fs.Read(path.Join(dir, "icon.json")); err == nil && json.Valid(spec) {
				d.Spec = spec
			}
			if cached, _ := s.Store.ReadFile(iconCache(p.ID)); string(cached) != d.SVG {
				_ = s.Store.WriteFile(iconCache(p.ID), svg)
			}
			return d
		}
		// No icon in the folder: the page generates one.
		_ = s.Store.Remove(iconCache(p.ID))
		return d
	}
	if svg, err := s.Store.ReadFile(iconCache(p.ID)); err == nil {
		d.SVG = string(svg)
	}
	return d
}

func (s *Server) registerIcons() {
	// The icons of the listed projects, by id ("" when there is none yet).
	s.handle("projects.icons", func(ctx context.Context, c *Client, p json.RawMessage) (any, error) {
		out := map[string]string{}
		for _, v := range s.Projects.List() {
			out[v.ID] = s.readIcon(v.Project).SVG
		}
		return out, nil
	})
	s.handle("projects.icon", func(ctx context.Context, c *Client, p json.RawMessage) (any, error) {
		a, err := bind[struct{ ID string }](p)
		if err != nil {
			return nil, err
		}
		owner, err := s.iconOwner(a.ID)
		if err != nil {
			return nil, err
		}
		d := s.readIcon(owner)
		return map[string]any{"owner": owner.ID, "name": owner.Name(), "spec": d.Spec, "svg": d.SVG}, nil
	})
	s.handle("projects.icon.save", func(ctx context.Context, c *Client, p json.RawMessage) (any, error) {
		a, err := bind[struct {
			ID string
			iconData
		}](p)
		if err != nil {
			return nil, err
		}
		owner, err := s.iconOwner(a.ID)
		if err != nil {
			return nil, err
		}
		if !strings.HasPrefix(strings.TrimSpace(a.SVG), "<svg") || len(a.SVG) > maxIcon || !json.Valid(a.Spec) {
			return nil, i18n.New("invalid icon")
		}
		fs, dir, ok := s.iconFS(owner)
		if !ok {
			return nil, i18n.New("open the project to change its icon")
		}
		_ = fs.Mkdir(dir)
		if err := fs.Write(path.Join(dir, "icon.svg"), []byte(a.SVG)); err != nil {
			return nil, err
		}
		if err := fs.Write(path.Join(dir, "icon.json"), a.Spec); err != nil {
			return nil, err
		}
		_ = s.Store.WriteFile(iconCache(owner.ID), []byte(a.SVG))
		s.broadcast("projects.iconChanged", map[string]string{"id": owner.ID, "svg": a.SVG}, nil)
		return nil, nil
	})
}
