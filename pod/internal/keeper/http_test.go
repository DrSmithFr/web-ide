package keeper

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// stream answers the body it got, then n lines, one every 20 ms.
func stream(t *testing.T, n int) *httptest.Server {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		in, _ := io.ReadAll(r.Body)
		w.Header().Set("X-Test", r.Header.Get("Authorization"))
		w.WriteHeader(201)
		fmt.Fprintf(w, "got %s\n", in)
		for i := 0; n < 0 || i < n; i++ {
			if _, err := fmt.Fprintf(w, "line %d\n", i); err != nil {
				return
			}
			w.(http.Flusher).Flush()
			time.Sleep(20 * time.Millisecond)
		}
	}))
	t.Cleanup(ts.Close)
	return ts
}

func want(n int) string {
	var b strings.Builder
	b.WriteString("got hello\n")
	for i := 0; i < n; i++ {
		fmt.Fprintf(&b, "line %d\n", i)
	}
	return b.String()
}

func TestRelay(t *testing.T) {
	s, path := start(t)
	c := dial(t, path)
	ts := stream(t, 15)
	err := c.StartHTTP(HTTPStart{ID: "r1", Owner: "p", Method: "POST", URL: ts.URL, Headers: http.Header{"Authorization": {"Bearer secret"}}, Meta: []byte(`{"chat":"c"}`)}, []byte("hello"))
	if err != nil {
		t.Fatal(err)
	}
	if err := c.StartHTTP(HTTPStart{ID: "r1", Method: "GET", URL: ts.URL}, nil); err == nil {
		t.Fatal("the same id twice")
	}
	// Read while it streams; the connection drops in the middle (the pod restarts).
	resp, err := c.OpenHTTP(context.Background(), "r1", 0)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 201 || resp.Header.Get("X-Test") != "Bearer secret" {
		t.Fatalf("head: %d %v", resp.StatusCode, resp.Header)
	}
	buf := make([]byte, 20)
	n, _ := io.ReadFull(resp.Body, buf)
	s.mu.Lock()
	for sc := range s.conns {
		sc.c.Close()
	}
	s.mu.Unlock()
	rest, err := io.ReadAll(resp.Body)
	if err != nil || string(buf[:n])+string(rest) != want(15) {
		t.Fatalf("body: %v %q", err, string(buf[:n])+string(rest))
	}
	// From an offset, once ended.
	resp, err = c.OpenHTTP(context.Background(), "r1", 10)
	if err != nil {
		t.Fatal("from 10:", err)
	}
	if b, _ := io.ReadAll(resp.Body); string(b) != want(15)[10:] {
		t.Fatalf("from 10: %q", b)
	}
	list, _ := c.ListHTTP("p")
	data, _ := json.Marshal(list)
	if len(list) != 1 || !list[0].Done || list[0].Status != 201 || list[0].Size != int64(len(want(15))) || strings.Contains(string(data), "secret") {
		t.Fatalf("list: %s", data)
	}
	if err := c.ForgetHTTP("r1"); err != nil {
		t.Fatal(err)
	}
	if list, _ := c.ListHTTP(""); len(list) != 0 {
		t.Fatalf("forgotten: %+v", list)
	}
	if _, err := c.OpenHTTP(context.Background(), "r1", 0); err == nil {
		t.Fatal("a forgotten request opened")
	}
}

func TestRelayCancelAndCap(t *testing.T) {
	_, path := start(t)
	c := dial(t, path)
	ts := stream(t, -1)
	_ = c.StartHTTP(HTTPStart{ID: "inf", Method: "POST", URL: ts.URL}, []byte("hello"))
	resp, err := c.OpenHTTP(context.Background(), "inf", 0)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(60 * time.Millisecond)
	if err := c.CancelHTTP("inf"); err != nil {
		t.Fatal(err)
	}
	if _, err := io.ReadAll(resp.Body); err == nil || !strings.Contains(err.Error(), "canceled") {
		t.Fatalf("cancelled: %v", err)
	}
	// Stopping to read leaves the request running.
	_ = c.StartHTTP(HTTPStart{ID: "bg", Method: "POST", URL: stream(t, 5).URL}, []byte("hello"))
	ctx, cancel := context.WithCancel(context.Background())
	resp, _ = c.OpenHTTP(ctx, "bg", 0)
	cancel()
	if _, err := io.ReadAll(resp.Body); err == nil {
		t.Fatal("a stopped reading goes on")
	}
	time.Sleep(300 * time.Millisecond)
	if l, _ := c.ListHTTP(""); len(l) != 2 || !l[0].Done && !l[1].Done {
		t.Fatalf("list: %+v", l)
	}
	resp, _ = c.OpenHTTP(context.Background(), "bg", 0)
	if b, _ := io.ReadAll(resp.Body); string(b) != want(5) {
		t.Fatalf("read again: %q", b)
	}
	// Beyond the cap.
	old := HTTPMax
	HTTPMax = 100
	defer func() { HTTPMax = old }()
	_ = c.StartHTTP(HTTPStart{ID: "big", Method: "POST", URL: ts.URL}, []byte("hello"))
	resp, err = c.OpenHTTP(context.Background(), "big", 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.ReadAll(resp.Body); err == nil || !strings.Contains(err.Error(), "beyond") {
		t.Fatalf("cap: %v", err)
	}
	// A server that does not answer.
	_ = c.StartHTTP(HTTPStart{ID: "down", Method: "GET", URL: "http://127.0.0.1:1"}, nil)
	if _, err := c.OpenHTTP(context.Background(), "down", 0); err == nil {
		t.Fatal("no error without answer")
	}
}
