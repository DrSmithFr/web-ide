package docker

import (
	"bufio"
	"strings"
	"time"

	"github.com/DrSmithFr/web-ide/pod/internal/execx"
	"github.com/DrSmithFr/web-ide/pod/internal/plugins"
)

// Lines of a log stream go to the window in batches (every 100 ms or 500 lines).
const (
	logBatch = 500
	logLine  = 16 << 10
)

func pumpLogs(w plugins.Window, id string, proc execx.Process, stop func()) {
	defer func() {
		stop()
		_ = proc.Wait()
		w.Release(id)
		w.Push("docker.logEnd", map[string]string{"stream": id})
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
			w.Push("docker.log", map[string]any{"stream": id, "lines": batch})
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
		case <-w.Done():
			return
		}
	}
}
