package keeper

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"sync"
)

// StartHTTP runs a request in the relay of the keeper under the id a.ID.
func (c *Client) StartHTTP(a HTTPStart, body []byte) error {
	return c.call(OpHTTPStart, a, body, nil)
}

// ListHTTP gives the requests of the relay for owner ("" for all), without their headers.
func (c *Client) ListHTTP(owner string) ([]HTTPInfo, error) {
	var r []HTTPInfo
	err := c.call(OpHTTPList, procArgs{Owner: owner}, nil, &r)
	return r, err
}

func (c *Client) CancelHTTP(id string) error { return c.call(OpHTTPCancel, procArgs{ID: id}, nil, nil) }

// ForgetHTTP drops a request of the relay (cancelled if it still runs).
func (c *Client) ForgetHTTP(id string) error { return c.call(OpHTTPForget, procArgs{ID: id}, nil, nil) }

// OpenHTTP reads the response of the request id from the offset from of its body: the body
// follows what the keeper receives, across the reconnections, until the end of the response.
// Cancelling ctx (or closing the body) stops reading, not the request.
func (c *Client) OpenHTTP(ctx context.Context, id string, from int64) (*http.Response, error) {
	body := &relayBody{}
	body.cond = sync.NewCond(&body.mu)
	heads := make(chan *http.Response, 1)
	ended := make(chan string, 1)
	var once sync.Once
	a := &Attachment{c: c, id: id, op: OpHTTPAttach, next: from}
	a.Head = func(raw json.RawMessage) {
		var h httpHead
		if json.Unmarshal(raw, &h) == nil {
			once.Do(func() { heads <- &http.Response{StatusCode: h.Status, Header: h.Headers, Body: body} })
		}
	}
	a.Output = func(_ int64, data []byte, _ bool) { body.write(data) }
	a.Exit = func(int) {
		a.mu.Lock()
		msg := a.err
		a.mu.Unlock()
		var err error = io.EOF
		if msg != "" {
			err = errors.New(msg)
		}
		body.end(err)
		ended <- msg
	}
	body.stop = a.Stop
	if err := c.attach(a); err != nil && !errors.Is(err, ErrUnreachable) {
		return nil, err
	}
	stop := context.AfterFunc(ctx, func() { a.Stop(); body.end(ctx.Err()) })
	select {
	case resp := <-heads:
		return resp, nil
	case msg := <-ended:
		select {
		case resp := <-heads: // an ended request: its head came first
			return resp, nil
		default:
		}
		stop()
		if msg == "" {
			msg = "the request ended without an answer"
		}
		return nil, errors.New(msg)
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// relayBody is the body of a relayed response: written by the reader of the client, never
// blocking it, read by the pod at its pace.
type relayBody struct {
	mu   sync.Mutex
	cond *sync.Cond
	data []byte
	err  error
	stop func()
}

func (b *relayBody) write(p []byte) {
	b.mu.Lock()
	if b.err == nil {
		b.data = append(b.data, p...)
	}
	b.cond.Broadcast()
	b.mu.Unlock()
}

func (b *relayBody) end(err error) {
	b.mu.Lock()
	if b.err == nil {
		b.err = err
	}
	b.cond.Broadcast()
	b.mu.Unlock()
}

func (b *relayBody) Read(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for len(b.data) == 0 && b.err == nil {
		b.cond.Wait()
	}
	if len(b.data) > 0 {
		n := copy(p, b.data)
		b.data = b.data[n:]
		return n, nil
	}
	return 0, b.err
}

func (b *relayBody) Close() error {
	b.stop()
	b.end(errors.New("body closed"))
	return nil
}
