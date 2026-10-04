// Package runtime holds everything the pod keeps alive for an open project: file system,
// watches, open files and their revisions, buffers shared between windows, consoles,
// language servers and database connections.
package runtime

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"path"
	"sync"
	"time"

	"github.com/DrSmithFr/web-ide/pod/internal/console"
	"github.com/DrSmithFr/web-ide/pod/internal/db"
	"github.com/DrSmithFr/web-ide/pod/internal/docker"
	"github.com/DrSmithFr/web-ide/pod/internal/execx"
	"github.com/DrSmithFr/web-ide/pod/internal/fsx"
	"github.com/DrSmithFr/web-ide/pod/internal/git"
	"github.com/DrSmithFr/web-ide/pod/internal/i18n"
	"github.com/DrSmithFr/web-ide/pod/internal/lsp"
	"github.com/DrSmithFr/web-ide/pod/internal/projects"
	"github.com/DrSmithFr/web-ide/pod/internal/sshx"
	"github.com/DrSmithFr/web-ide/pod/internal/store"
)

const maxFile = 10 << 20

// Emit sends an event to the clients of the project, except the client named by except.
type Emit func(event string, data any, except string)

type Deps struct {
	Pool  *sshx.Pool
	Store *store.Store
}

type fileState struct {
	hash   [32]byte
	rev    int
	exists bool
}

type ProjectConfig struct {
	LSP   map[string][]string `json:"lsp,omitempty"`
	Tests map[string]string   `json:"tests,omitempty"` // custom patterns, see related.go
}

type Runtime struct {
	P      projects.Project
	Root   string
	FS     fsx.FS
	Runner execx.Runner
	Local  bool
	Config ProjectConfig

	Consoles *console.Manager
	LSP      *lsp.Manager
	DB       *db.Manager
	Git      *git.Repo
	Docker   *docker.Docker

	emit    Emit
	watcher fsx.Watcher
	// pool and target reach the SSH host again (tunnels).
	pool   *sshx.Pool
	target sshx.Target

	mu      sync.Mutex
	watched map[string]bool
	files   map[string]*fileState
	buffers map[string]string
	timers  map[string]*time.Timer
	clients int
	idle    *time.Timer
}

func Open(p projects.Project, creds sshx.Creds, d Deps, emit Emit) (*Runtime, error) {
	r := &Runtime{P: p, emit: emit, watched: map[string]bool{}, files: map[string]*fileState{},
		buffers: map[string]string{}, timers: map[string]*time.Timer{}, pool: d.Pool}
	if p.Type == "ssh" {
		t := sshx.Target{Host: p.SSH.Host, Port: p.SSH.Port, User: p.SSH.User, Auth: p.SSH.Auth, KeyPath: p.SSH.KeyPath}
		r.target = t
		client, err := d.Pool.Get(t, creds)
		if err != nil {
			return nil, err
		}
		sfs, err := fsx.NewSFTP(client)
		if err != nil {
			return nil, err
		}
		r.FS, r.Runner = sfs, execx.SSH{Client: client}
	} else {
		r.FS, r.Runner, r.Local = fsx.Local{}, execx.Local{}, true
	}
	root, err := r.FS.Abs(p.Path)
	if err != nil {
		return nil, err
	}
	st, err := r.FS.Stat(root)
	if err != nil {
		return nil, i18n.Errorf("project folder not reachable: %w", err)
	}
	if !st.Dir {
		return nil, i18n.New("the project target is not a folder")
	}
	r.Root = root
	if data, err := r.FS.Read(path.Join(root, ".ide", "project.json")); err == nil {
		_ = json.Unmarshal(data, &r.Config)
	}
	if r.watcher, err = r.FS.NewWatcher(); err != nil {
		return nil, err
	}
	go r.watchLoop()

	r.Consoles = console.NewManager(r.Runner, root, console.Events{
		Output: func(id string, data []byte) {
			r.emit("console.output", map[string]string{"id": id, "data": base64.StdEncoding.EncodeToString(data)}, "")
		},
		Exit: func(id string, code int) {
			r.emit("console.exit", map[string]any{"id": id, "code": code}, "")
		},
	})
	exists := func(p string) bool { _, err := r.FS.Stat(p); return err == nil }
	r.LSP = lsp.NewManager(r.Runner, root, r.Local, r.Config.LSP, exists, func(ev string, data any) { r.emit(ev, data, "") })
	r.Git = git.New(r.Runner, root)
	r.Docker = docker.New(r.Runner, root, exists)
	r.DB = db.NewManager(db.Deps{FS: r.FS, Root: root, ProjectID: p.ID, Local: r.Local, Runner: r.Runner, Pool: d.Pool, Store: d.Store})
	return r, nil
}

// Attach and Detach count the connected windows. Language servers stop a while after
// the last window leaves (some use several hundred MB); consoles keep running.
func (r *Runtime) Attach() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.clients++
	if r.idle != nil {
		r.idle.Stop()
		r.idle = nil
	}
}

func (r *Runtime) Detach() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.clients--
	if r.clients <= 0 {
		r.clients = 0
		r.idle = time.AfterFunc(2*time.Minute, r.LSP.StopAll)
	}
}

func (r *Runtime) Close() {
	r.Consoles.CloseAll()
	r.LSP.StopAll()
	r.DB.CloseAll()
	_ = r.watcher.Close()
	_ = r.FS.Close()
}

// ---------- files ----------

func (r *Runtime) watchDir(dir string) {
	r.mu.Lock()
	if r.watched[dir] {
		r.mu.Unlock()
		return
	}
	r.watched[dir] = true
	r.mu.Unlock()
	r.watcher.Add(dir)
}

func (r *Runtime) Abs(p string) (string, error) {
	if p == "" {
		return r.Root, nil
	}
	if !path.IsAbs(p) {
		return path.Join(r.Root, p), nil
	}
	return path.Clean(p), nil
}

func (r *Runtime) List(dir string) ([]fsx.Entry, error) {
	es, err := r.FS.List(dir)
	if err != nil {
		return nil, err
	}
	r.watchDir(dir)
	return es, nil
}

type FileContent struct {
	Path     string  `json:"path"`
	Content  string  `json:"content"`
	Rev      int     `json:"rev"`
	Buffer   *string `json:"buffer,omitempty"` // unsaved content of another window
	ReadOnly bool    `json:"readOnly"`
	Binary   bool    `json:"binary"`
	Size     int64   `json:"size"`
	Format
}

// Read reads any path the pod can reach. Files outside the project are read-only.
func (r *Runtime) Read(p string) (*FileContent, error) {
	st, err := r.FS.Stat(p)
	if err != nil {
		return nil, err
	}
	if st.Dir {
		return nil, i18n.New("it is a folder")
	}
	fc := &FileContent{Path: p, Size: st.Size, ReadOnly: !fsx.Within(r.Root, p)}
	if st.Size > maxFile {
		fc.Binary = true
		return fc, nil
	}
	data, err := r.FS.Read(p)
	if err != nil {
		return nil, err
	}
	text, f, ok := DecodeText(data)
	if !ok {
		fc.Binary = true
		return fc, nil
	}
	fc.Content, fc.Format = text, f
	h := sha256.Sum256(data)
	r.mu.Lock()
	fs, ok := r.files[p]
	if !ok {
		fs = &fileState{rev: 1}
		r.files[p] = fs
	} else if fs.hash != h {
		fs.rev++
	}
	fs.hash, fs.exists = h, true
	fc.Rev = fs.rev
	if b, ok := r.buffers[p]; ok {
		fc.Buffer = &b
	}
	r.mu.Unlock()
	r.watchDir(path.Dir(p))
	return fc, nil
}

// Write saves a file. The other windows receive the new version as a remote change
// (a clean merge since their buffers are already in sync).
func (r *Runtime) Write(p, content string, f Format, client string) (int, error) {
	if !fsx.Within(r.Root, p) {
		return 0, i18n.New("file outside the project: read-only")
	}
	data, err := EncodeText(content, f)
	if err != nil {
		return 0, err
	}
	if err := r.FS.Write(p, data); err != nil {
		return 0, err
	}
	h := sha256.Sum256(data)
	r.mu.Lock()
	fs, ok := r.files[p]
	if !ok {
		fs = &fileState{}
		r.files[p] = fs
	}
	fs.rev++
	fs.hash, fs.exists = h, true
	rev := fs.rev
	delete(r.buffers, p)
	r.mu.Unlock()
	r.watchDir(path.Dir(p))
	r.emit("fs.changed", map[string]any{"path": p, "content": content, "rev": rev, "saved": true, "encoding": f.Encoding, "eol": f.EOL}, client)
	return rev, nil
}

// SyncBuffer shares the unsaved content of a file with the other windows (nil: buffer clean).
func (r *Runtime) SyncBuffer(p string, content *string, client string) {
	r.mu.Lock()
	if content == nil {
		delete(r.buffers, p)
	} else {
		r.buffers[p] = *content
	}
	r.mu.Unlock()
	r.emit("buffer.synced", map[string]any{"path": p, "content": content}, client)
}

func (r *Runtime) Create(p string, dir bool) error {
	if _, err := r.FS.Stat(p); err == nil {
		return i18n.New("already exists")
	}
	if dir {
		return r.FS.Mkdir(p)
	}
	return r.FS.Write(p, nil)
}

func (r *Runtime) Delete(p string) error {
	if p == r.Root || !fsx.Within(r.Root, p) {
		return i18n.New("deletion refused outside the project")
	}
	return r.FS.Remove(p)
}

func (r *Runtime) Rename(from, to string) error {
	if !fsx.Within(r.Root, from) || !fsx.Within(r.Root, to) {
		return i18n.New("renaming refused outside the project")
	}
	if _, err := r.FS.Stat(to); err == nil {
		return i18n.New("the destination already exists")
	}
	return r.FS.Rename(from, to)
}

func (r *Runtime) watchLoop() {
	for p := range r.watcher.Events() {
		r.mu.Lock()
		if t, ok := r.timers[p]; ok {
			t.Stop()
		}
		// AI tools often write a file in several steps: wait for it to settle.
		r.timers[p] = time.AfterFunc(150*time.Millisecond, func() { r.changed(p) })
		r.mu.Unlock()
	}
}

func (r *Runtime) changed(p string) {
	r.mu.Lock()
	delete(r.timers, p)
	fs, open := r.files[p]
	r.mu.Unlock()
	r.emit("fs.dir", map[string]string{"path": path.Dir(p)}, "")
	if !open {
		return
	}
	data, err := r.FS.Read(p)
	if err != nil {
		r.mu.Lock()
		wasThere := fs.exists
		fs.exists = false
		r.mu.Unlock()
		if wasThere {
			r.emit("fs.deleted", map[string]string{"path": p}, "")
		}
		return
	}
	if len(data) > maxFile {
		return
	}
	text, f, ok := DecodeText(data)
	if !ok {
		return
	}
	h := sha256.Sum256(data)
	r.mu.Lock()
	if fs.hash == h && fs.exists {
		r.mu.Unlock()
		return
	}
	fs.hash, fs.exists = h, true
	fs.rev++
	rev := fs.rev
	r.mu.Unlock()
	r.emit("fs.changed", map[string]any{"path": p, "content": text, "rev": rev, "encoding": f.Encoding, "eol": f.EOL}, "")
}
