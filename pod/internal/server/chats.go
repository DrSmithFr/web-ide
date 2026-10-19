package server

import (
	"path/filepath"

	"github.com/DrSmithFr/web-ide/pod/internal/llm"
)

// chatLoc is where the conversations of a project are kept, and the project whose windows
// follow them: a worktree keeps its conversations with those of its parent.
func (s *Server) chatLoc(projectID string) (llm.ChatLocation, string, error) {
	root, err := s.Projects.Root(projectID)
	if err != nil {
		return llm.ChatLocation{}, "", err
	}
	l := llm.ChatLocation{Project: root.ID}
	if root.Type == "local" {
		l.IdeDir = filepath.Join(root.Path, ".ide")
	}
	return l, root.ID, nil
}
