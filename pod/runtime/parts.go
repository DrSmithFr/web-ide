package runtime

import (
	"github.com/DrSmithFr/web-ide/pod/sshx"
	"github.com/DrSmithFr/web-ide/pod/store"
)

// The parts of a runtime are the state a plugin keeps per project (its manager, its
// connections): the runtime holds them without knowing them, so the business code of a plugin
// stays in its folder (docs/design/plugins.md).

// Stopper is a part that runs something: Stop(false) when the project closes, Stop(true) when
// the pod stops (what the keeper holds survives, the next pod takes it back).
type Stopper interface{ Stop(release bool) }

// Idler is a part told when the last window of the project left a while ago (language servers,
// some of several hundred MB).
type Idler interface{ Idle() }

// Part is the part of a plugin under key, made on first use.
func (r *Runtime) Part(key string, newPart func() any) any {
	r.partsMu.Lock()
	defer r.partsMu.Unlock()
	if p, ok := r.parts[key]; ok {
		return p
	}
	p := newPart()
	r.parts[key] = p
	r.order = append(r.order, key)
	return p
}

// stopParts stops the parts, the last made first.
func (r *Runtime) stopParts(release bool) {
	r.partsMu.Lock()
	list := make([]any, 0, len(r.order))
	for i := len(r.order) - 1; i >= 0; i-- {
		list = append(list, r.parts[r.order[i]])
	}
	r.partsMu.Unlock()
	for _, p := range list {
		if s, ok := p.(Stopper); ok {
			s.Stop(release)
		}
	}
}

func (r *Runtime) idleParts() {
	r.partsMu.Lock()
	list := make([]any, 0, len(r.parts))
	for _, k := range r.order {
		list = append(list, r.parts[k])
	}
	r.partsMu.Unlock()
	for _, p := range list {
		if s, ok := p.(Idler); ok {
			s.Idle()
		}
	}
}

// What the makers of the parts need besides the files and the runner.

// Emit sends an event to every window of the project.
func (r *Runtime) Emit(event string, data any) { r.emit(event, data, "") }

// Pool is the SSH pool of the pod (an SSH project reaches its host again with it).
func (r *Runtime) Pool() *sshx.Pool { return r.pool }

// Store is the data folder of the pod (nil in some tests).
func (r *Runtime) Store() *store.Store { return r.store }

// SettingsID is the project whose settings and secrets the runtime uses: the main project of a
// worktree, else the project itself.
func (r *Runtime) SettingsID() string {
	if r.P.Parent != "" {
		return r.P.Parent
	}
	return r.P.ID
}
