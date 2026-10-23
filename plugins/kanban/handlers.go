package kanban

import (
	"context"
	"encoding/base64"
	"encoding/json"

	"github.com/DrSmithFr/web-ide/lib/i18n"
	kb "github.com/DrSmithFr/web-ide/plugins/kanban/kanban"
	"github.com/DrSmithFr/web-ide/pod/projects"
)

// kanbanProject is the project whose kanban (and conversations) a project uses: itself,
// or its parent for the worktree of a ticket.
func (s *K) kanbanProject(id string) (*projects.Project, error) { return s.Projects.Root(id) }

func kanbanLoc(p *projects.Project) kb.Location { return kb.LocationOf(p) }

// emitKanban tells the windows of a project and of its worktrees that a ticket changed.
func (s *K) emitKanban(root string, id int64, except *client) {
	ex := ""
	if except != nil {
		ex = except.id
	}
	s.pod.EmitFamily(root, "kanban.changed", map[string]any{"project": root, "id": id}, ex)
}

func (s *K) registerKanban() {
	type kctx struct {
		loc  kb.Location
		root *projects.Project
	}
	// Changes are announced to every window, the asking one included (it may show the
	// ticket in several places).
	h := func(f func(ctx context.Context, c *client, k kctx, p json.RawMessage) (any, error)) handler {
		return func(ctx context.Context, c *client, p json.RawMessage) (any, error) {
			root, err := s.kanbanProject(c.project)
			if err != nil {
				return nil, err
			}
			return s.withTimes(root.ID)(f(ctx, c, kctx{loc: kanbanLoc(root), root: root}, p))
		}
	}
	type idArg struct {
		ID int64  `json:"id"`
		By string `json:"by"`
	}
	by := func(b string) string {
		if b == kb.ByModel {
			return b
		}
		return kb.ByUser
	}
	// change binds the arguments, runs f and announces the ticket.
	change := func(f func(k kctx, a idArg, p json.RawMessage) error) handler {
		return h(func(ctx context.Context, c *client, k kctx, p json.RawMessage) (any, error) {
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

	s.handle("kanban.list", h(func(ctx context.Context, c *client, k kctx, p json.RawMessage) (any, error) {
		list, err := s.Kanban.List(k.loc)
		if err != nil {
			return nil, err
		}
		s.fillBlockers(ctx, s.clientGit(c, k.root), list)
		if times := s.chatTimes(k.root.ID); times != nil {
			for i := range list {
				list[i].GenerationMs = times.of(list[i].ChatIDs...)
			}
		}
		meta, err := s.Kanban.Meta(k.loc)
		if err != nil {
			return nil, err
		}
		milestones, err := s.Kanban.Milestones(k.loc)
		if err != nil {
			return nil, err
		}
		objectives, err := s.Kanban.Objectives(k.loc)
		return map[string]any{"project": k.root.ID, "tickets": list, "meta": meta, "milestones": milestones, "objectives": objectives}, err
	}))
	s.handle("kanban.get", h(func(ctx context.Context, c *client, k kctx, p json.RawMessage) (any, error) {
		a, err := bind[idArg](p)
		if err != nil {
			return nil, err
		}
		t, err := s.Kanban.Get(k.loc, a.ID)
		if err == nil && kb.Startable(t.Status) && (t.Parent != 0 || len(t.DependsOn) > 0) {
			t.Blockers, _ = s.ticketBlockers(ctx, s.clientGit(c, k.root), t.ID)
		}
		return t, err
	}))
	s.handle("kanban.create", h(func(ctx context.Context, c *client, k kctx, p json.RawMessage) (any, error) {
		a, err := bind[struct {
			kb.Patch
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
			Patch kb.Patch `json:"patch"`
		}](p)
		if err != nil {
			return err
		}
		return s.Kanban.Update(k.loc, a.ID, b.Patch, a.By)
	}))
	s.handle("kanban.move", h(func(ctx context.Context, c *client, k kctx, p json.RawMessage) (any, error) {
		a, err := bind[struct {
			ID      int64  `json:"id"`
			By      string `json:"by"`
			Status  string `json:"status"`
			Comment string `json:"comment"`
			Force   bool   `json:"force"`
		}](p)
		if err != nil {
			return nil, err
		}
		// Developing without a worktree (no git) keeps the order of the lineages too.
		if a.Status == kb.InProgress {
			if t, err := s.Kanban.Get(k.loc, a.ID); err == nil && t.Status == kb.Todo {
				if bl, err := s.ticketBlockers(ctx, s.clientGit(c, k.root), t.ID); err == nil && len(bl) > 0 {
					if !a.Force || by(a.By) != kb.ByUser {
						return nil, blockedError(bl)
					}
					_ = s.Kanban.Event(k.loc, t.ID, kb.ByUser, "Started despite: {blockers}", kb.Params{"blockers": kb.BlockersText(bl)})
				}
			}
		}
		if err := s.Kanban.Move(k.loc, a.ID, a.Status, by(a.By), a.Comment); err != nil {
			return nil, err
		}
		s.emitKanban(k.root.ID, a.ID, nil)
		return s.Kanban.Get(k.loc, a.ID)
	}))
	// kanban.step validates the own work of a parent: its first child may start.
	s.handle("kanban.step", change(func(k kctx, a idArg, p json.RawMessage) error {
		if err := s.Kanban.ValidateStep(k.loc, a.ID, kb.ByUser); err != nil {
			return err
		}
		if t, err := s.Kanban.Get(k.loc, a.ID); err == nil {
			for _, ch := range t.Children {
				s.emitKanban(k.root.ID, ch.ID, nil)
			}
		}
		return nil
	}))
	s.handle("kanban.child.move", change(func(k kctx, a idArg, p json.RawMessage) error {
		b, err := bind[struct{ Delta int }](p)
		if err != nil {
			return err
		}
		return s.Kanban.MoveChild(k.loc, a.ID, b.Delta, a.By)
	}))
	s.handle("kanban.delete", h(func(ctx context.Context, c *client, k kctx, p json.RawMessage) (any, error) {
		a, err := bind[idArg](p)
		if err != nil {
			return nil, err
		}
		// The worktree of the ticket goes with it (its branch stays); a child shares the one
		// of its lineage, and a parent keeps its children.
		t, err := s.Kanban.Get(k.loc, a.ID)
		if err == nil && len(t.Children) > 0 {
			return nil, i18n.New("this ticket has steps: take them out of its lineage first")
		}
		if err == nil && t.Worktree != "" && t.Parent == 0 {
			if rt, err := c.runtime(); err == nil {
				wt := t.Worktree
				if _, err := rt.FS.Stat(wt); err != nil {
					wt = ""
				}
				if err := s.removeWorktree(ctx, k.loc, k.root, kb.Git{Run: rt.Runner, Root: k.root.Path}, t, wt); err != nil {
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
		b, err := bind[struct {
			Text   string `json:"text"`
			ChatID string `json:"chatId"`
		}](p)
		if err != nil {
			return err
		}
		return s.Kanban.AddNote(k.loc, a.ID, b.Text, a.By, b.ChatID)
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
			Plan       string         `json:"plan"`
			Goals      []kb.GoalInput `json:"goals"`
			Size       *string        `json:"size"`
			Complexity *string        `json:"complexity"`
		}](p)
		if err != nil {
			return err
		}
		if b.Size != nil || b.Complexity != nil {
			if err := s.Kanban.Update(k.loc, a.ID, kb.Patch{Size: b.Size, Complexity: b.Complexity, PlanSize: true}, a.By); err != nil {
				return err
			}
		}
		return s.Kanban.SetPlan(k.loc, a.ID, b.Plan, b.Goals, a.By)
	}))
	s.handle("kanban.goal", change(func(k kctx, a idArg, p json.RawMessage) error {
		b, err := bind[struct {
			Goal kb.GoalOp `json:"goal"`
		}](p)
		if err != nil {
			return err
		}
		return s.Kanban.Goal(k.loc, a.ID, b.Goal)
	}))
	s.handle("kanban.feedback", change(func(k kctx, a idArg, p json.RawMessage) error {
		b, err := bind[struct {
			Feedback kb.FeedbackOp `json:"feedback"`
		}](p)
		if err != nil {
			return err
		}
		_, err = s.Kanban.Feedback(k.loc, a.ID, b.Feedback, a.By)
		return err
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
	s.handle("kanban.attachment.get", h(func(ctx context.Context, c *client, k kctx, p json.RawMessage) (any, error) {
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
	s.handle("kanban.meta.set", h(func(ctx context.Context, c *client, k kctx, p json.RawMessage) (any, error) {
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
