// Package backend runs the backend of an external plugin (docs/design/plugins.md, *Confined
// backend*): a process started through `web-ide-pod plugin-exec` (pod/plugins/confine), which
// speaks JSON-RPC 2.0 over its stdin and stdout, one message per line.
//
// The pod asks it `initialize {apiVersion, plugin, grants, project}`, `call {method, params,
// lang}` (an <id>.* method of a window), `tool {name, args, lang}` (one of its assistant tools)
// and `shutdown`; it asks the pod `core {method, params}` (a method of the core, which the pod
// refuses without its grant) and tells it `emit {event, data}` (an event to the windows).
package backend

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"time"

	"antennae.space/ide/lib/i18n"
)

// MaxMessage caps one message of either side.
const MaxMessage = 8 << 20

// CallTimeout bounds a call without a deadline of its own.
const CallTimeout = 2 * time.Minute

// Config starts a backend.
type Config struct {
	// Argv is plugin-exec and its flags, then the backend (the pod builds it: Argv of Spec).
	Argv []string
	Env  []string
	Dir  string
	// Log receives its standard error (capped by the writer).
	Log io.Writer
	// Init are the params of initialize.
	Init any
	// Core answers a call of the backend to the core; Emit takes its events.
	Core func(ctx context.Context, method string, params json.RawMessage) (any, error)
	Emit func(event string, data json.RawMessage)
}

// Process is a running backend.
type Process struct {
	cfg  Config
	cmd  *exec.Cmd
	in   io.WriteCloser
	wmu  sync.Mutex
	mu   sync.Mutex
	next int64
	wait map[int64]chan message
	done chan struct{}
	err  error
}

type message struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// Start runs a backend and initializes it.
func Start(ctx context.Context, cfg Config) (*Process, error) {
	cmd := exec.Command(cfg.Argv[0], cfg.Argv[1:]...)
	cmd.Env, cmd.Dir = cfg.Env, cfg.Dir
	cmd.Stderr = cfg.Log
	in, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	p := &Process{cfg: cfg, cmd: cmd, in: in, wait: map[int64]chan message{}, done: make(chan struct{})}
	go p.read(out)
	go func() {
		err := cmd.Wait()
		p.end(err)
	}()
	if _, err := p.Call(ctx, "initialize", cfg.Init); err != nil {
		p.Stop()
		return nil, err
	}
	return p, nil
}

// Done is closed when the process ended; Err tells why.
func (p *Process) Done() <-chan struct{} { return p.done }
func (p *Process) Err() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.err
}

func (p *Process) end(err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	select {
	case <-p.done:
		return
	default:
	}
	if err == nil {
		err = errors.New("the backend ended")
	}
	p.err = err
	close(p.done)
	for id, ch := range p.wait {
		ch <- message{Error: &rpcError{Message: i18n.Errorf("the backend of the plugin ended: %v", err).Error()}}
		delete(p.wait, id)
	}
}

func (p *Process) send(m message) error {
	m.JSONRPC = "2.0"
	data, err := json.Marshal(m)
	if err != nil {
		return err
	}
	if len(data) > MaxMessage {
		return i18n.Errorf("a message to the backend is larger than %d MB", MaxMessage>>20)
	}
	p.wmu.Lock()
	defer p.wmu.Unlock()
	_, err = p.in.Write(append(data, '\n'))
	return err
}

// Call asks the backend and waits for its answer (CallTimeout without a deadline).
func (p *Process) Call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, CallTimeout)
		defer cancel()
	}
	raw, err := json.Marshal(params)
	if err != nil {
		return nil, err
	}
	ch := make(chan message, 1)
	p.mu.Lock()
	select {
	case <-p.done:
		p.mu.Unlock()
		return nil, i18n.Errorf("the backend of the plugin ended: %v", p.err)
	default:
	}
	p.next++
	id := p.next
	p.wait[id] = ch
	p.mu.Unlock()
	idRaw, _ := json.Marshal(id)
	if err := p.send(message{ID: idRaw, Method: method, Params: raw}); err != nil {
		p.forget(id)
		return nil, err
	}
	select {
	case m := <-ch:
		if m.Error != nil {
			return nil, errors.New(m.Error.Message)
		}
		return m.Result, nil
	case <-ctx.Done():
		p.forget(id)
		return nil, i18n.Errorf("the backend of the plugin did not answer %s in time", method)
	}
}

func (p *Process) forget(id int64) {
	p.mu.Lock()
	delete(p.wait, id)
	p.mu.Unlock()
}

// read takes the lines of the backend: the answers to the pod, its calls and its events.
func (p *Process) read(out io.Reader) {
	sc := bufio.NewScanner(out)
	sc.Buffer(make([]byte, 64<<10), MaxMessage)
	for sc.Scan() {
		var m message
		if err := json.Unmarshal(sc.Bytes(), &m); err != nil {
			continue
		}
		switch {
		case m.Method == "" && m.ID != nil:
			var id int64
			if json.Unmarshal(m.ID, &id) != nil {
				continue
			}
			p.mu.Lock()
			ch := p.wait[id]
			delete(p.wait, id)
			p.mu.Unlock()
			if ch != nil {
				ch <- m
			}
		case m.Method == "emit":
			var e struct {
				Event string          `json:"event"`
				Data  json.RawMessage `json:"data"`
			}
			if json.Unmarshal(m.Params, &e) == nil && p.cfg.Emit != nil {
				p.cfg.Emit(e.Event, e.Data)
			}
		case m.Method == "core" && m.ID != nil:
			go p.core(m)
		case m.ID != nil:
			_ = p.send(message{ID: m.ID, Error: &rpcError{Code: -32601, Message: "unknown method: " + m.Method}})
		}
	}
	// A line too long, or the pipe closed: the backend is stopped.
	err := sc.Err()
	if err == nil {
		err = io.EOF
	}
	_ = p.cmd.Process.Kill()
	p.end(fmt.Errorf("its output: %v", err))
}

// core answers a call of the backend to the core.
func (p *Process) core(m message) {
	var c struct {
		Method string          `json:"method"`
		Params json.RawMessage `json:"params"`
	}
	reply := message{ID: m.ID}
	if err := json.Unmarshal(m.Params, &c); err != nil || p.cfg.Core == nil {
		reply.Error = &rpcError{Code: -32602, Message: "invalid core call"}
	} else if res, err := p.cfg.Core(context.Background(), c.Method, c.Params); err != nil {
		reply.Error = &rpcError{Code: -32000, Message: err.Error()}
	} else if reply.Result, err = json.Marshal(res); err != nil {
		reply.Error = &rpcError{Code: -32000, Message: err.Error()}
	}
	_ = p.send(reply)
}

// Stop asks the backend to shut down, then kills it after a second.
func (p *Process) Stop() {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	go func() { _, _ = p.Call(ctx, "shutdown", nil) }()
	select {
	case <-p.done:
	case <-ctx.Done():
		_ = p.cmd.Process.Signal(os.Kill)
		<-p.done
	}
}
