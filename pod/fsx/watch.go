package fsx

import (
	"path/filepath"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

// Watcher reports changed paths inside the watched directories.
// Directories only are watched: editors that save through a rename would break a file watch.
type Watcher interface {
	Add(dir string)
	Remove(dir string)
	Events() <-chan string
	Close() error
}

type localWatcher struct {
	w      *fsnotify.Watcher
	events chan string
}

func newLocalWatcher() (Watcher, error) {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	lw := &localWatcher{w: w, events: make(chan string, 256)}
	go lw.loop()
	return lw, nil
}

func (l *localWatcher) loop() {
	defer close(l.events)
	for {
		select {
		case ev, ok := <-l.w.Events:
			if !ok {
				return
			}
			if ev.Op == fsnotify.Chmod {
				continue
			}
			l.events <- filepath.Clean(ev.Name)
		case _, ok := <-l.w.Errors:
			if !ok {
				return
			}
		}
	}
}

func (l *localWatcher) Add(dir string)        { _ = l.w.Add(dir) }
func (l *localWatcher) Remove(dir string)     { _ = l.w.Remove(dir) }
func (l *localWatcher) Events() <-chan string { return l.events }
func (l *localWatcher) Close() error          { return l.w.Close() }

// pollWatcher lists the watched directories at a fixed interval (SFTP has no notifications).
type pollWatcher struct {
	fs       FS
	mu       sync.Mutex
	dirs     map[string]map[string]Entry
	events   chan string
	stop     chan struct{}
	interval time.Duration
}

func newPollWatcher(f FS, interval time.Duration) *pollWatcher {
	p := &pollWatcher{fs: f, dirs: map[string]map[string]Entry{}, events: make(chan string, 256), stop: make(chan struct{}), interval: interval}
	go p.loop()
	return p
}

func (p *pollWatcher) snapshot(dir string) map[string]Entry {
	es, err := p.fs.List(dir)
	if err != nil {
		return nil
	}
	m := make(map[string]Entry, len(es))
	for _, e := range es {
		m[e.Name] = e
	}
	return m
}

func (p *pollWatcher) Add(dir string) {
	p.mu.Lock()
	_, ok := p.dirs[dir]
	p.mu.Unlock()
	if ok {
		return
	}
	snap := p.snapshot(dir)
	p.mu.Lock()
	p.dirs[dir] = snap
	p.mu.Unlock()
}

func (p *pollWatcher) Remove(dir string) {
	p.mu.Lock()
	delete(p.dirs, dir)
	p.mu.Unlock()
}

func (p *pollWatcher) Events() <-chan string { return p.events }

func (p *pollWatcher) Close() error {
	close(p.stop)
	return nil
}

func (p *pollWatcher) loop() {
	t := time.NewTicker(p.interval)
	defer t.Stop()
	for {
		select {
		case <-p.stop:
			close(p.events)
			return
		case <-t.C:
		}
		p.mu.Lock()
		dirs := make([]string, 0, len(p.dirs))
		for d := range p.dirs {
			dirs = append(dirs, d)
		}
		p.mu.Unlock()
		for _, d := range dirs {
			cur := p.snapshot(d)
			p.mu.Lock()
			prev, ok := p.dirs[d]
			if ok {
				p.dirs[d] = cur
			}
			p.mu.Unlock()
			if !ok {
				continue
			}
			for name, e := range cur {
				if old, ok := prev[name]; !ok || old.ModTime != e.ModTime || old.Size != e.Size {
					p.events <- e.Path
				}
			}
			for name, e := range prev {
				if _, ok := cur[name]; !ok {
					p.events <- e.Path
				}
			}
		}
	}
}
