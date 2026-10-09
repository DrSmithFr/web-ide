package keeper

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync"
	"time"
)

// The HTTP relay runs a request for the pod and keeps its response: a pod that restarts reads
// the body again from an offset (a streamed answer of a model server goes on). It knows
// nothing about what the request is.

// HTTPMax is the body kept for a request: beyond it the request is cancelled with an error.
var HTTPMax = 64 << 20

// HTTPKeep: an ended request is dropped after this long, unless forgotten before.
const HTTPKeep = 15 * time.Minute

const (
	OpHTTPStart  = "http.start"
	OpHTTPAttach = "http.attach"
	OpHTTPList   = "http.list"
	OpHTTPCancel = "http.cancel"
	OpHTTPForget = "http.forget"

	// KindHead carries the status and the headers of the response, before its body.
	KindHead = "head"
)

// HTTPStart: the request (its body is the payload of the frame). ID is chosen by the pod.
type HTTPStart struct {
	ID      string          `json:"id"`
	Owner   string          `json:"owner,omitempty"`
	Method  string          `json:"method"`
	URL     string          `json:"url"`
	Headers http.Header     `json:"headers,omitempty"`
	Meta    json.RawMessage `json:"meta,omitempty"`
}

// HTTPInfo describes a request of the relay (never its headers: they may hold a key).
type HTTPInfo struct {
	ID      string          `json:"id"`
	Owner   string          `json:"owner,omitempty"`
	Status  int             `json:"status,omitempty"`
	Size    int64           `json:"size"`
	Done    bool            `json:"done,omitempty"`
	Error   string          `json:"error,omitempty"`
	Started int64           `json:"started"`
	Meta    json.RawMessage `json:"meta,omitempty"`
}

type httpHead struct {
	Status  int         `json:"status"`
	Headers http.Header `json:"headers,omitempty"`
}

type relay struct {
	HTTPInfo
	head     *httpHead
	buf      []byte
	ended    time.Time
	cancel   context.CancelFunc
	attached int
	cond     *sync.Cond
}

var relayClient = &http.Client{Transport: &http.Transport{
	Proxy:                 http.ProxyFromEnvironment,
	DialContext:           (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
	TLSHandshakeTimeout:   10 * time.Second,
	ResponseHeaderTimeout: 10 * time.Minute, // a model loading, a long prompt read
	IdleConnTimeout:       90 * time.Second,
}}

func (s *Server) httpStart(a HTTPStart, body []byte) error {
	if a.ID == "" || a.URL == "" {
		return fmt.Errorf("id and url are needed")
	}
	ctx, cancel := context.WithCancel(context.Background())
	req, err := http.NewRequestWithContext(ctx, a.Method, a.URL, bytes.NewReader(body))
	if err != nil {
		cancel()
		return err
	}
	req.Header = a.Headers
	r := &relay{HTTPInfo: HTTPInfo{ID: a.ID, Owner: a.Owner, Started: time.Now().UnixMilli(), Meta: a.Meta}, cancel: cancel}
	r.cond = sync.NewCond(&s.mu)
	s.mu.Lock()
	if _, dup := s.relays[a.ID]; dup {
		s.mu.Unlock()
		cancel()
		return fmt.Errorf("request %s exists", a.ID)
	}
	s.relays[a.ID] = r
	s.mu.Unlock()
	go s.httpRun(r, req)
	return nil
}

func (s *Server) httpRun(r *relay, req *http.Request) {
	end := func(err string) {
		s.mu.Lock()
		r.Done, r.Error, r.ended = true, err, time.Now()
		r.cond.Broadcast()
		s.mu.Unlock()
		r.cancel()
	}
	resp, err := relayClient.Do(req)
	if err != nil {
		end(err.Error())
		return
	}
	defer resp.Body.Close()
	s.mu.Lock()
	r.Status, r.head = resp.StatusCode, &httpHead{Status: resp.StatusCode, Headers: resp.Header}
	r.cond.Broadcast()
	s.mu.Unlock()
	b := make([]byte, 32*1024)
	for {
		n, err := resp.Body.Read(b)
		if n > 0 {
			s.mu.Lock()
			if len(r.buf)+n > HTTPMax {
				s.mu.Unlock()
				end(fmt.Sprintf("response beyond %d MiB", HTTPMax>>20))
				return
			}
			r.buf = append(r.buf, b[:n]...)
			r.Size = int64(len(r.buf))
			r.cond.Broadcast()
			s.mu.Unlock()
		}
		if err == io.EOF {
			end("")
			return
		}
		if err != nil {
			end(err.Error())
			return
		}
	}
}

func (s *Server) httpGet(id string) (*relay, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.relays[id]
	if !ok {
		return nil, fmt.Errorf("no request %s", id)
	}
	return r, nil
}

func (s *Server) httpList(owner string) []HTTPInfo {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []HTTPInfo{}
	for _, r := range s.relays {
		if owner == "" || r.Owner == owner {
			out = append(out, r.HTTPInfo)
		}
	}
	return out
}

func (s *Server) httpForget(id string) {
	s.mu.Lock()
	r, ok := s.relays[id]
	if ok {
		delete(s.relays, id)
		r.cond.Broadcast()
	}
	s.mu.Unlock()
	if ok {
		r.cancel()
	}
}

// httpGC drops the requests ended for HTTPKeep that nobody follows.
func (s *Server) httpGC(now time.Time) {
	s.mu.Lock()
	var old []string
	for id, r := range s.relays {
		if r.Done && r.attached == 0 && now.Sub(r.ended) > HTTPKeep {
			old = append(old, id)
		}
	}
	s.mu.Unlock()
	for _, id := range old {
		s.httpForget(id)
	}
}

// httpAttach sends the head of the response, its body from the offset from, then its end.
func (sc *serverConn) httpAttach(id int64, r *relay, from int64) {
	s := sc.s
	s.mu.Lock()
	r.attached++
	defer func() {
		r.attached--
		s.mu.Unlock()
	}()
	gone := func() bool { return sc.closed || s.relays[r.ID] != r }
	headSent := false
	if from < 0 {
		from = 0
	}
	for {
		for !gone() && !r.Done && (r.head == nil || headSent) && from >= r.Size {
			r.cond.Wait()
		}
		if gone() {
			return
		}
		if r.head != nil && !headSent {
			data, _ := json.Marshal(r.head)
			s.mu.Unlock()
			err := sc.send(header{ID: id, Kind: KindHead, Result: data}, nil)
			s.mu.Lock()
			if err != nil {
				return
			}
			headSent = true
			continue
		}
		if from < r.Size {
			chunk := append([]byte(nil), r.buf[from:]...)
			if len(chunk) > 64*1024 {
				chunk = chunk[:64*1024]
			}
			h := header{ID: id, Kind: KindOutput, Offset: from}
			from += int64(len(chunk))
			s.mu.Unlock()
			err := sc.send(h, chunk)
			s.mu.Lock()
			if err != nil {
				return
			}
			continue
		}
		msg := r.Error
		s.mu.Unlock()
		_ = sc.send(header{ID: id, Kind: KindExit, Error: msg}, nil)
		s.mu.Lock()
		return
	}
}
