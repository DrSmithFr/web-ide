package relayload

import (
	"context"
	"io"
	"net/http"
	"sync"
	"time"
)

// staticFiles are what a browser actually fetches from the relay: the bootstrap page, its
// Service Worker, the scripts it loads, and the wordlist the pairing reads. The assets of a pod
// are not here — the relay does not hold them (#71 caches them in the Service Worker).
var staticFiles = []string{"/", "/sw.js", "/_relay/bootstrap.js", "/_relay/bootstrap.css", "/_relay/routes.js", "/_relay/identity.js", "/_relay/bip39.txt"}

// runStatic is the cheapest tier: what the relay costs in TLS, HTTP and its own files. It is the
// baseline a signaling or tunnel run is compared with.
func runStatic(ctx context.Context, c *Config, rep *Report) error {
	client := c.client()
	deadline := time.Now().Add(c.Hold)
	var mu sync.Mutex
	var n int
	var bytes int64
	var times []time.Duration
	var work sync.WaitGroup
	for w := 0; w < c.Workers; w++ {
		work.Add(1)
		go func() {
			defer work.Done()
			for time.Now().Before(deadline) {
				select {
				case <-ctx.Done():
					return
				default:
				}
				path := staticFiles[n%len(staticFiles)]
				n++
				at := time.Now()
				size, err := fetch(ctx, client, c.RelayURL+path)
				if err != nil {
					mu.Lock()
					rep.note(err)
					mu.Unlock()
					continue
				}
				mu.Lock()
				times = append(times, time.Since(at))
				bytes += size
				mu.Unlock()
			}
		}()
	}
	work.Wait()
	rep.OK = len(times)
	rep.Bytes = bytes
	rep.Setup = times
	return nil
}

// fetch reads one file of the relay and returns its size.
func fetch(ctx context.Context, client *http.Client, url string) (int64, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, err
	}
	r, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer r.Body.Close()
	b, err := io.ReadAll(r.Body)
	if err != nil {
		return 0, err
	}
	if r.StatusCode != http.StatusOK {
		return 0, errBadStatus(r.StatusCode)
	}
	return int64(len(b)), nil
}

// errBadStatus names a page the relay refused.
type errBadStatus int

func (e errBadStatus) Error() string { return "the relay answered " + http.StatusText(int(e)) }
