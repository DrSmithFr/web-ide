package server

import (
	"bufio"
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/DrSmithFr/web-ide/pod/internal/execx"
	"github.com/DrSmithFr/web-ide/pod/internal/i18n"
	"github.com/DrSmithFr/web-ide/pod/internal/runtime"
)

func (s *Server) registerDocker() {
	type profilesArg struct {
		Profiles []string `json:"profiles"`
	}
	s.handle("docker.status", withRuntime(func(ctx context.Context, c *Client, rt *runtime.Runtime, p json.RawMessage) (any, error) {
		st := rt.Docker.Status(ctx)
		if st.Err != nil {
			st.Error = i18n.Translate(c.language(), st.Err)
		}
		return st, nil
	}))
	s.handle("docker.stack", withRuntime(func(ctx context.Context, c *Client, rt *runtime.Runtime, p json.RawMessage) (any, error) {
		a, err := bind[profilesArg](p)
		if err != nil {
			return nil, err
		}
		return rt.Docker.Stack(ctx, a.Profiles)
	}))
	s.handle("docker.containers", withRuntime(func(ctx context.Context, c *Client, rt *runtime.Runtime, p json.RawMessage) (any, error) {
		return rt.Docker.Containers(ctx)
	}))
	s.handle("docker.inspect", withRuntime(func(ctx context.Context, c *Client, rt *runtime.Runtime, p json.RawMessage) (any, error) {
		a, err := bind[struct {
			ID string `json:"id"`
		}](p)
		if err != nil {
			return nil, err
		}
		return rt.Docker.Inspect(ctx, a.ID)
	}))
	s.handle("docker.stats", withRuntime(func(ctx context.Context, c *Client, rt *runtime.Runtime, p json.RawMessage) (any, error) {
		a, err := bind[struct {
			IDs []string `json:"ids"`
		}](p)
		if err != nil {
			return nil, err
		}
		return rt.Docker.Stats(ctx, a.IDs)
	}))
	s.handle("docker.compose", withRuntime(func(ctx context.Context, c *Client, rt *runtime.Runtime, p json.RawMessage) (any, error) {
		a, err := bind[struct {
			Profiles []string `json:"profiles"`
			Action   string   `json:"action"`
			Service  string   `json:"service"`
		}](p)
		if err != nil {
			return nil, err
		}
		return nil, rt.Docker.Compose(ctx, a.Profiles, a.Action, a.Service)
	}))
	s.handle("docker.container", withRuntime(func(ctx context.Context, c *Client, rt *runtime.Runtime, p json.RawMessage) (any, error) {
		a, err := bind[struct {
			Action string `json:"action"`
			ID     string `json:"id"`
		}](p)
		if err != nil {
			return nil, err
		}
		return nil, rt.Docker.Container(ctx, a.Action, a.ID)
	}))
}

var streamSeq atomic.Int64

// Lines of a log stream go to the window in batches (every 100 ms or 500 lines).
const (
	logBatch = 500
	logLine  = 16 << 10
)

func (s *Server) registerDockerLogs() {
	// docker.logs follows the logs of a container (id) or of the stack (no id) and pushes
	// docker.log events {stream, lines} to this window, then docker.logEnd when docker stops.
	s.handle("docker.logs", withRuntime(func(ctx context.Context, c *Client, rt *runtime.Runtime, p json.RawMessage) (any, error) {
		a, err := bind[struct {
			ID       string   `json:"id"`
			Profiles []string `json:"profiles"`
			Tail     int      `json:"tail"`
		}](p)
		if err != nil {
			return nil, err
		}
		if a.Tail <= 0 {
			a.Tail = 500
		}
		proc, err := rt.Docker.Logs(a.ID, a.Profiles, a.Tail)
		if err != nil {
			return nil, err
		}
		id := "l" + strconv.FormatInt(streamSeq.Add(1), 10)
		var once sync.Once
		stop := func() { once.Do(func() { _ = proc.Kill() }) }
		c.streams.Store(id, stop)
		go s.pumpLogs(c, id, proc, stop)
		return map[string]string{"stream": id}, nil
	}))
	s.handle("docker.logsStop", func(ctx context.Context, c *Client, p json.RawMessage) (any, error) {
		a, err := bind[struct {
			Stream string `json:"stream"`
		}](p)
		if err != nil {
			return nil, err
		}
		if stop, ok := c.streams.LoadAndDelete(a.Stream); ok {
			stop.(func())()
		}
		return nil, nil
	})
}

func (s *Server) pumpLogs(c *Client, id string, proc execx.Process, stop func()) {
	defer func() {
		stop()
		_ = proc.Wait()
		c.streams.Delete(id)
		c.push("docker.logEnd", map[string]string{"stream": id})
	}()
	lines := make(chan string, logBatch)
	done := make(chan struct{})
	defer close(done)
	go func() {
		defer close(lines)
		r := bufio.NewReaderSize(proc.Stdout(), 64<<10)
		for {
			line, err := r.ReadString('\n')
			if line != "" {
				line = strings.TrimRight(line, "\r\n")
				if len(line) > logLine {
					line = line[:logLine] + "…"
				}
				select {
				case lines <- strings.ToValidUTF8(line, "�"):
				case <-done:
					return
				}
			}
			if err != nil {
				return
			}
		}
	}()
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	var batch []string
	flush := func() {
		if len(batch) > 0 {
			c.push("docker.log", map[string]any{"stream": id, "lines": batch})
			batch = nil
		}
	}
	for {
		select {
		case l, ok := <-lines:
			if !ok {
				flush()
				return
			}
			batch = append(batch, l)
			if len(batch) >= logBatch {
				flush()
			}
		case <-tick.C:
			flush()
		case <-c.ctx.Done():
			return
		}
	}
}

func (s *Server) registerDockerDisk() {
	s.handle("docker.disk", withRuntime(func(ctx context.Context, c *Client, rt *runtime.Runtime, p json.RawMessage) (any, error) {
		return rt.Docker.Disk(ctx)
	}))
	s.handle("docker.prune", withRuntime(func(ctx context.Context, c *Client, rt *runtime.Runtime, p json.RawMessage) (any, error) {
		a, err := bind[struct {
			What string `json:"what"`
		}](p)
		if err != nil {
			return nil, err
		}
		freed, err := rt.Docker.Prune(ctx, a.What)
		return map[string]string{"reclaimed": freed}, err
	}))
	s.handle("docker.remove", withRuntime(func(ctx context.Context, c *Client, rt *runtime.Runtime, p json.RawMessage) (any, error) {
		a, err := bind[struct {
			Kind string `json:"kind"`
			ID   string `json:"id"`
		}](p)
		if err != nil {
			return nil, err
		}
		return nil, rt.Docker.RemoveObject(ctx, a.Kind, a.ID)
	}))
}
