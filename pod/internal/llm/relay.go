package llm

import (
	"context"
	"io"
	"net/http"

	"github.com/DrSmithFr/web-ide/pod/internal/keeper"
)

// Relay runs the streamed completions outside the pod (the keeper): a pod that restarts reads
// them again from the start and goes on (ResumeChat).
type Relay interface {
	Start(id string, req *http.Request, body []byte) error
	// Open reads the response of id from the offset from of its body.
	Open(ctx context.Context, id string, from int64) (*http.Response, error)
	Cancel(id string) error
	Forget(id string) error
}

// KeeperRelay is the relay of the keeper.
type KeeperRelay struct{ C *keeper.Client }

func (r KeeperRelay) Start(id string, req *http.Request, body []byte) error {
	return r.C.StartHTTP(keeper.HTTPStart{ID: id, Method: req.Method, URL: req.URL.String(), Headers: req.Header}, body)
}

func (r KeeperRelay) Open(ctx context.Context, id string, from int64) (*http.Response, error) {
	return r.C.OpenHTTP(ctx, id, from)
}

func (r KeeperRelay) Cancel(id string) error { return r.C.CancelHTTP(id) }
func (r KeeperRelay) Forget(id string) error { return r.C.ForgetHTTP(id) }

type ctxKey int

const (
	streamKey ctxKey = iota
	resumeKey
)

// The completions of a job go through the relay under the id of their stream.
func withStream(ctx context.Context, stream string, resume bool) context.Context {
	return context.WithValue(context.WithValue(ctx, streamKey, stream), resumeKey, resume)
}

func streamOf(ctx context.Context) (string, bool) {
	id, _ := ctx.Value(streamKey).(string)
	resume, _ := ctx.Value(resumeKey).(bool)
	return id, resume
}

// relayed sends a streamed completion through the relay (or, resuming, reads the one already
// sent from its start), with the errors of do.
func (m *Manager) relayed(ctx context.Context, s Server, req *http.Request, data []byte) (*http.Response, error) {
	id, resume := streamOf(ctx)
	if !resume {
		_ = m.Relay.Forget(id) // a retry after a rate limit
		if err := m.Relay.Start(id, req, data); err != nil {
			return nil, err
		}
	}
	resp, err := m.Relay.Open(ctx, id, 0)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, i18nReach(s, err)
	}
	if resp.StatusCode >= 300 {
		defer resp.Body.Close()
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, httpError(resp, data)
	}
	return resp, nil
}
