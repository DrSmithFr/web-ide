package llm

import (
	"context"
	"sync"
	"time"

	"github.com/DrSmithFr/web-ide/pod/internal/i18n"
)

// A job is a completion that runs in the pod independently of the page that asked for it:
// when the page reloads, the generation goes on and the new page attaches to it, getting
// what was already written then the rest.

type job struct {
	mu      sync.Mutex
	cancel  context.CancelFunc
	started time.Time
	// Everything received so far (counters: last values).
	acc    Delta
	subs   map[int]func(Delta)
	nextID int
	done   chan struct{}
	result *ChatResult
	err    error
	ended  time.Time
}

// Snapshot is what an attaching page receives first.
type Snapshot struct {
	Delta
	StartedAt int64 `json:"startedAt"`
}

const jobKeep = 10 * time.Minute

func (j *job) publish(d Delta) {
	j.mu.Lock()
	j.acc.Content += d.Content
	j.acc.Reasoning += d.Reasoning
	if d.Tool != "" {
		j.acc.Tool = d.Tool
	}
	addCounters(&j.acc, d)
	subs := make([]func(Delta), 0, len(j.subs))
	for _, f := range j.subs {
		subs = append(subs, f)
	}
	j.mu.Unlock()
	for _, f := range subs {
		f(d)
	}
}

// subscribe returns what was received so far and registers f for the rest, atomically.
func (j *job) subscribe(f func(Delta)) (Snapshot, func()) {
	j.mu.Lock()
	defer j.mu.Unlock()
	id := j.nextID
	j.nextID++
	if f == nil {
		f = func(Delta) {}
	}
	j.subs[id] = f
	snap := Snapshot{Delta: j.acc, StartedAt: j.started.UnixMilli()}
	return snap, func() {
		j.mu.Lock()
		delete(j.subs, id)
		j.mu.Unlock()
	}
}

// StartChat starts a completion as a job identified by stream (chosen by the page).
func (m *Manager) StartChat(stream string, req ChatRequest) error {
	return m.startJob(stream, req, false, time.Now())
}

// ResumeChat takes back a completion the relay still runs for stream (the pod restarted): it
// reads it again from its start, then follows it. started is when it was first asked.
func (m *Manager) ResumeChat(stream string, req ChatRequest, started time.Time) error {
	if m.Relay == nil {
		return i18n.New("completion not found (the pod may have restarted)")
	}
	return m.startJob(stream, req, true, started)
}

func (m *Manager) startJob(stream string, req ChatRequest, resume bool, started time.Time) error {
	if stream == "" {
		return i18n.New("stream id is missing")
	}
	m.mu.Lock()
	m.gcJobs()
	if _, ok := m.jobs[stream]; ok {
		m.mu.Unlock()
		return i18n.New("stream already in use")
	}
	ctx, cancel := context.WithCancel(context.Background())
	j := &job{cancel: cancel, started: started, subs: map[int]func(Delta){}, done: make(chan struct{})}
	m.jobs[stream] = j
	m.mu.Unlock()
	go func() {
		defer cancel()
		res, err := m.Chat(withStream(ctx, stream, resume), req, j.publish)
		if ctx.Err() != nil && m.Relay != nil {
			_ = m.Relay.Cancel(stream) // Stop: the relay stops asking the model too
		}
		j.mu.Lock()
		j.result, j.err, j.ended = res, err, time.Now()
		j.mu.Unlock()
		close(j.done)
	}()
	return nil
}

// WaitChat follows a job: onSnapshot receives what was already written, onDelta the rest;
// it returns the final answer. When ctx ends, the job goes on (see CancelChat).
func (m *Manager) WaitChat(ctx context.Context, stream string, onSnapshot func(Snapshot), onDelta func(Delta)) (*ChatResult, error) {
	m.mu.Lock()
	j := m.jobs[stream]
	m.mu.Unlock()
	if j == nil {
		return nil, i18n.New("completion not found (the pod may have restarted)")
	}
	snap, unsubscribe := j.subscribe(onDelta)
	defer unsubscribe()
	if onSnapshot != nil {
		onSnapshot(snap)
	}
	select {
	case <-j.done:
		return j.result, j.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// CancelChat stops a job (button Stop).
func (m *Manager) CancelChat(stream string) {
	m.mu.Lock()
	j := m.jobs[stream]
	m.mu.Unlock()
	if j != nil {
		j.cancel()
	}
}

// gcJobs forgets the jobs ended for a while. Called with m.mu held.
func (m *Manager) gcJobs() {
	for id, j := range m.jobs {
		j.mu.Lock()
		old := !j.ended.IsZero() && time.Since(j.ended) > jobKeep
		j.mu.Unlock()
		if old {
			delete(m.jobs, id)
		}
	}
}
