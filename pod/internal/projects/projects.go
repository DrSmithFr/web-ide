// Package projects is the project registry, persisted in ~/.web-ide/projects.json
// and kept in memory by the pod.
package projects

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"path"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/DrSmithFr/web-ide/pod/internal/store"
)

type SSHTarget struct {
	Host    string `json:"host"`
	Port    int    `json:"port"`
	User    string `json:"user"`
	Auth    string `json:"auth"` // agent | key | password
	KeyPath string `json:"keyPath,omitempty"`
}

type Project struct {
	ID          string     `json:"id"`
	Title       string     `json:"title"`
	Description string     `json:"description"`
	Type        string     `json:"type"` // local | ssh
	Path        string     `json:"path"`
	SSH         *SSHTarget `json:"ssh,omitempty"`
	// Parent and Ticket: worktree of a kanban ticket, opened as its own project (hidden
	// from the project list, its kanban and conversations are those of the parent).
	Parent    string    `json:"parent,omitempty"`
	Ticket    int64     `json:"ticket,omitempty"`
	CreatedAt time.Time `json:"createdAt"`
	OpenedAt  time.Time `json:"openedAt"`
}

// Name is the title, or a name derived from the path or the host.
func (p *Project) Name() string {
	if strings.TrimSpace(p.Title) != "" {
		return p.Title
	}
	base := path.Base(strings.TrimRight(p.Path, "/"))
	if p.Type == "ssh" && p.SSH != nil {
		if base == "/" || base == "." || base == "" {
			return p.SSH.Host
		}
		return base + " @ " + p.SSH.Host
	}
	return base
}

type Registry struct {
	mu    sync.Mutex
	st    *store.Store
	items []*Project
}

const file = "projects.json"

func Load(st *store.Store) (*Registry, error) {
	r := &Registry{st: st}
	if err := st.ReadJSON(file, &r.items); err != nil && !store.IsNotExist(err) {
		return nil, err
	}
	return r, nil
}

func (r *Registry) save() error { return r.st.WriteJSON(file, r.items) }

type View struct {
	*Project
	DisplayName string `json:"name"`
}

func view(p *Project) View { return View{Project: p, DisplayName: p.Name()} }

func (r *Registry) List() []View {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]View, 0, len(r.items))
	for _, p := range r.items {
		if p.Parent != "" {
			continue
		}
		cp := *p
		out = append(out, view(&cp))
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].OpenedAt.After(out[j].OpenedAt) })
	return out
}

func (r *Registry) Get(id string) (*Project, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, p := range r.items {
		if p.ID == id {
			cp := *p
			return &cp, true
		}
	}
	return nil, false
}

func validate(p *Project) error {
	p.Path = strings.TrimSpace(p.Path)
	switch p.Type {
	case "local":
		p.SSH = nil
		if !path.IsAbs(p.Path) {
			return errors.New("le chemin du projet doit être absolu")
		}
	case "ssh":
		if p.SSH == nil || p.SSH.Host == "" {
			return errors.New("hôte SSH manquant")
		}
		if p.SSH.Port == 0 {
			p.SSH.Port = 22
		}
		if p.SSH.Auth == "" {
			p.SSH.Auth = "agent"
		}
		if p.Path == "" {
			p.Path = "."
		}
	default:
		return errors.New("type de projet inconnu")
	}
	p.Path = path.Clean(p.Path)
	return nil
}

func (r *Registry) Create(p Project) (View, error) {
	if err := validate(&p); err != nil {
		return View{}, err
	}
	buf := make([]byte, 6)
	_, _ = rand.Read(buf)
	p.ID = hex.EncodeToString(buf)
	p.CreatedAt = time.Now()
	p.OpenedAt = p.CreatedAt
	r.mu.Lock()
	defer r.mu.Unlock()
	r.items = append(r.items, &p)
	return view(&p), r.save()
}

func (r *Registry) Update(p Project) (View, error) {
	if err := validate(&p); err != nil {
		return View{}, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for i, cur := range r.items {
		if cur.ID == p.ID {
			p.CreatedAt, p.OpenedAt = cur.CreatedAt, cur.OpenedAt
			r.items[i] = &p
			return view(&p), r.save()
		}
	}
	return View{}, errors.New("projet introuvable")
}

// SetPath records the resolved absolute path (an SSH project given relative to the remote home).
func (r *Registry) SetPath(id, p string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, cur := range r.items {
		if cur.ID == id && cur.Path != p {
			cur.Path = p
			_ = r.save()
		}
	}
}

func (r *Registry) Touch(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, cur := range r.items {
		if cur.ID == id {
			cur.OpenedAt = time.Now()
			_ = r.save()
		}
	}
}

func (r *Registry) Delete(id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i, cur := range r.items {
		if cur.ID == id {
			r.items = append(r.items[:i], r.items[i+1:]...)
			return r.save()
		}
	}
	return errors.New("projet introuvable")
}

// ChildID is the id of the project opened on the worktree of a ticket.
func ChildID(parent string, ticket int64) string {
	return parent + "-t" + strconv.FormatInt(ticket, 10)
}

// PutChild registers (or updates) the project of the worktree of a ticket.
func (r *Registry) PutChild(parent *Project, ticket int64, title, dir string) (View, error) {
	p := Project{ID: ChildID(parent.ID, ticket), Title: title, Type: parent.Type, Path: dir, Parent: parent.ID, Ticket: ticket}
	if parent.SSH != nil {
		ssh := *parent.SSH
		p.SSH = &ssh
	}
	if err := validate(&p); err != nil {
		return View{}, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for i, cur := range r.items {
		if cur.ID == p.ID {
			p.CreatedAt, p.OpenedAt = cur.CreatedAt, cur.OpenedAt
			r.items[i] = &p
			return view(&p), r.save()
		}
	}
	p.CreatedAt = time.Now()
	p.OpenedAt = p.CreatedAt
	r.items = append(r.items, &p)
	return view(&p), r.save()
}

// Children lists the worktree projects of a project.
func (r *Registry) Children(parent string) []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var ids []string
	for _, p := range r.items {
		if p.Parent == parent {
			ids = append(ids, p.ID)
		}
	}
	return ids
}
