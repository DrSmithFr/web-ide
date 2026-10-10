package server

import (
	"context"
	"encoding/json"

	"github.com/DrSmithFr/web-ide/pod/internal/projects"
	"github.com/DrSmithFr/web-ide/pod/internal/runtime"
)

// The .ide folder is ignored by git (package idedir), but a project may have committed
// some of its files before: the page offers once to take them out of the index.

// ideAsked lists the projects whose user already answered (store file).
const ideAsked = "ide-untrack.json"

func (s *Server) ideAnswered() map[string]bool {
	m := map[string]bool{}
	_ = s.Store.ReadJSON(ideAsked, &m)
	return m
}

// ideTracked returns the tracked files of .ide of a main project, while not answered yet.
func (s *Server) ideTracked(ctx context.Context, p *projects.Project, rt *runtime.Runtime) []string {
	if p.Parent != "" || s.ideAnswered()[p.ID] {
		return nil
	}
	return rt.Git.TrackedIn(ctx, ".ide")
}

func (s *Server) registerIdeDir() {
	// The answer of the user: untrack (git rm --cached, the files stay on disk) or not.
	s.handle("project.ideUntrack", func(ctx context.Context, c *Client, p json.RawMessage) (any, error) {
		a, err := bind[struct{ Untrack bool }](p)
		if err != nil {
			return nil, err
		}
		rt, err := c.runtime()
		if err != nil {
			return nil, err
		}
		if a.Untrack {
			if err := rt.Git.Untrack(ctx, ".ide"); err != nil {
				return nil, err
			}
		}
		m := s.ideAnswered()
		m[c.project] = true
		return nil, s.Store.WriteJSON(ideAsked, m)
	})
}
