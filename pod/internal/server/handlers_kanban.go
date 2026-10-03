package server

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"path/filepath"

	"webide/pod/internal/kanban"
	"webide/pod/internal/projects"
)

// kanbanProject is the project whose kanban (and conversations) a project uses: itself,
// or its parent for the worktree of a ticket.
func (s *Server) kanbanProject(id string) (*projects.Project, error) {
	p, ok := s.Projects.Get(id)
	if !ok {
		return nil, errors.New("aucun projet ouvert sur cette connexion")
	}
	if p.Parent != "" {
		if parent, ok := s.Projects.Get(p.Parent); ok {
			return parent, nil
		}
		return nil, errors.New("projet parent introuvable")
	}
	return p, nil
}

func kanbanLoc(p *projects.Project) kanban.Location {
	l := kanban.Location{Project: p.ID}
	if p.Type == "local" {
		l.IdeDir = filepath.Join(p.Path, ".ide")
	}
	return l
}

// emitKanban tells the windows of a project and of its worktrees that a ticket changed.
func (s *Server) emitKanban(root string, id int64, except *Client) {
	s.mu.Lock()
	var targets []*Client
	for c := range s.clients {
		if c == except || c.project == "" {
			continue
		}
		if c.project == root {
			targets = append(targets, c)
		} else if p, ok := s.Projects.Get(c.project); ok && p.Parent == root {
			targets = append(targets, c)
		}
	}
	s.mu.Unlock()
	for _, c := range targets {
		c.push("kanban.changed", map[string]any{"project": root, "id": id})
	}
}

func (s *Server) registerKanban() {
	type kctx struct {
		loc  kanban.Location
		root *projects.Project
	}
	// Changes are announced to every window, the asking one included (it may show the
	// ticket in several places).
	h := func(f func(ctx context.Context, c *Client, k kctx, p json.RawMessage) (any, error)) handler {
		return func(ctx context.Context, c *Client, p json.RawMessage) (any, error) {
			root, err := s.kanbanProject(c.project)
			if err != nil {
				return nil, err
			}
			return f(ctx, c, kctx{loc: kanbanLoc(root), root: root}, p)
		}
	}
	type idArg struct {
		ID int64  `json:"id"`
		By string `json:"by"`
	}
	by := func(b string) string {
		if b == kanban.ByModel {
			return b
		}
		return kanban.ByUser
	}
	// change binds the arguments, runs f and announces the ticket.
	change := func(f func(k kctx, a idArg, p json.RawMessage) error) handler {
		return h(func(ctx context.Context, c *Client, k kctx, p json.RawMessage) (any, error) {
			a, err := bind[idArg](p)
			if err != nil {
				return nil, err
			}
			a.By = by(a.By)
			if err := f(k, a, p); err != nil {
				return nil, err
			}
			s.emitKanban(k.root.ID, a.ID, nil)
			return s.Kanban.Get(k.loc, a.ID)
		})
	}

	s.handle("kanban.list", h(func(ctx context.Context, c *Client, k kctx, p json.RawMessage) (any, error) {
		list, err := s.Kanban.List(k.loc)
		if err != nil {
			return nil, err
		}
		meta, err := s.Kanban.Meta(k.loc)
		return map[string]any{"project": k.root.ID, "tickets": list, "meta": meta}, err
	}))
	s.handle("kanban.get", h(func(ctx context.Context, c *Client, k kctx, p json.RawMessage) (any, error) {
		a, err := bind[idArg](p)
		if err != nil {
			return nil, err
		}
		return s.Kanban.Get(k.loc, a.ID)
	}))
	s.handle("kanban.create", h(func(ctx context.Context, c *Client, k kctx, p json.RawMessage) (any, error) {
		a, err := bind[struct {
			kanban.Patch
			By string `json:"by"`
		}](p)
		if err != nil {
			return nil, err
		}
		id, err := s.Kanban.Create(k.loc, a.Patch, by(a.By))
		if err != nil {
			return nil, err
		}
		s.emitKanban(k.root.ID, id, nil)
		return s.Kanban.Get(k.loc, id)
	}))
	s.handle("kanban.update", change(func(k kctx, a idArg, p json.RawMessage) error {
		b, err := bind[struct {
			Patch kanban.Patch `json:"patch"`
		}](p)
		if err != nil {
			return err
		}
		return s.Kanban.Update(k.loc, a.ID, b.Patch, a.By)
	}))
	s.handle("kanban.move", change(func(k kctx, a idArg, p json.RawMessage) error {
		b, err := bind[struct{ Status, Comment string }](p)
		if err != nil {
			return err
		}
		return s.Kanban.Move(k.loc, a.ID, b.Status, a.By, b.Comment)
	}))
	s.handle("kanban.delete", h(func(ctx context.Context, c *Client, k kctx, p json.RawMessage) (any, error) {
		a, err := bind[idArg](p)
		if err != nil {
			return nil, err
		}
		// The worktree of the ticket goes with it (its branch stays).
		if t, err := s.Kanban.Get(k.loc, a.ID); err == nil && t.Worktree != "" {
			if rt, err := c.runtime(); err == nil {
				wt := t.Worktree
				if _, err := rt.FS.Stat(wt); err != nil {
					wt = ""
				}
				if err := s.removeWorktree(ctx, k.loc, k.root, kanban.Git{Run: rt.Runner, Root: k.root.Path}, t, wt); err != nil {
					return nil, err
				}
			}
		}
		if err := s.Kanban.Delete(k.loc, a.ID); err != nil {
			return nil, err
		}
		s.emitKanban(k.root.ID, a.ID, nil)
		return nil, nil
	}))
	s.handle("kanban.note", change(func(k kctx, a idArg, p json.RawMessage) error {
		b, err := bind[struct{ Kind, Text string }](p)
		if err != nil {
			return err
		}
		if b.Kind == "" {
			b.Kind = "note"
		}
		return s.Kanban.AddNote(k.loc, a.ID, b.Kind, b.Text, a.By)
	}))
	s.handle("kanban.note.delete", change(func(k kctx, a idArg, p json.RawMessage) error {
		b, err := bind[struct {
			NoteID int64 `json:"noteId"`
		}](p)
		if err != nil {
			return err
		}
		return s.Kanban.DeleteNote(k.loc, a.ID, b.NoteID)
	}))
	s.handle("kanban.plan", change(func(k kctx, a idArg, p json.RawMessage) error {
		b, err := bind[struct {
			Plan  string   `json:"plan"`
			Goals []string `json:"goals"`
		}](p)
		if err != nil {
			return err
		}
		return s.Kanban.SetPlan(k.loc, a.ID, b.Plan, b.Goals, a.By)
	}))
	s.handle("kanban.goal", change(func(k kctx, a idArg, p json.RawMessage) error {
		b, err := bind[struct {
			Goal kanban.GoalOp `json:"goal"`
		}](p)
		if err != nil {
			return err
		}
		return s.Kanban.Goal(k.loc, a.ID, b.Goal)
	}))
	s.handle("kanban.chat.link", change(func(k kctx, a idArg, p json.RawMessage) error {
		b, err := bind[struct {
			ChatID string `json:"chatId"`
			Role   string `json:"role"`
			Title  string `json:"title"`
		}](p)
		if err != nil {
			return err
		}
		return s.Kanban.LinkChat(k.loc, a.ID, b.ChatID, b.Role, b.Title)
	}))
	s.handle("kanban.chat.unlink", change(func(k kctx, a idArg, p json.RawMessage) error {
		b, err := bind[struct {
			ChatID string `json:"chatId"`
		}](p)
		if err != nil {
			return err
		}
		return s.Kanban.UnlinkChat(k.loc, a.ID, b.ChatID)
	}))
	s.handle("kanban.commit.link", change(func(k kctx, a idArg, p json.RawMessage) error {
		b, err := bind[struct{ Hash, Subject string }](p)
		if err != nil {
			return err
		}
		return s.Kanban.LinkCommit(k.loc, a.ID, b.Hash, b.Subject)
	}))
	s.handle("kanban.commit.unlink", change(func(k kctx, a idArg, p json.RawMessage) error {
		b, err := bind[struct{ Hash string }](p)
		if err != nil {
			return err
		}
		return s.Kanban.UnlinkCommit(k.loc, a.ID, b.Hash)
	}))
	s.handle("kanban.attachment.add", change(func(k kctx, a idArg, p json.RawMessage) error {
		b, err := bind[struct{ Name, Mime, Data string }](p)
		if err != nil {
			return err
		}
		data, err := base64.StdEncoding.DecodeString(b.Data)
		if err != nil {
			return err
		}
		_, err = s.Kanban.AddAttachment(k.loc, a.ID, b.Name, b.Mime, data)
		return err
	}))
	s.handle("kanban.attachment.get", h(func(ctx context.Context, c *Client, k kctx, p json.RawMessage) (any, error) {
		a, err := bind[struct {
			ID  int64 `json:"id"`
			AID int64 `json:"aid"`
		}](p)
		if err != nil {
			return nil, err
		}
		att, data, err := s.Kanban.Attachment(k.loc, a.ID, a.AID)
		if err != nil {
			return nil, err
		}
		return map[string]any{"attachment": att, "data": base64.StdEncoding.EncodeToString(data)}, nil
	}))
	s.handle("kanban.attachment.delete", change(func(k kctx, a idArg, p json.RawMessage) error {
		b, err := bind[struct {
			AID int64 `json:"aid"`
		}](p)
		if err != nil {
			return err
		}
		return s.Kanban.DeleteAttachment(k.loc, a.ID, b.AID)
	}))
	s.handle("kanban.meta.set", h(func(ctx context.Context, c *Client, k kctx, p json.RawMessage) (any, error) {
		a, err := bind[struct {
			Values map[string]string `json:"values"`
		}](p)
		if err != nil {
			return nil, err
		}
		if err := s.Kanban.SetMeta(k.loc, a.Values); err != nil {
			return nil, err
		}
		s.emitKanban(k.root.ID, 0, nil)
		return s.Kanban.Meta(k.loc)
	}))
}
