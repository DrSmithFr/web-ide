package hfcache

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
)

func TestCache(t *testing.T) {
	var hits atomic.Int32
	hub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		switch r.URL.Path {
		case "/org/model/resolve/main/onnx/enc.onnx":
			io.WriteString(w, "weights")
		case "/org/model/resolve/main/config.json":
			io.WriteString(w, `{"a":1}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer hub.Close()
	c := New(t.TempDir())
	c.Remote = hub.URL
	srv := httptest.NewServer(c)
	defer srv.Close()
	get := func(p string) (int, string) {
		resp, err := http.Get(srv.URL + p)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}
	if code, body := get("/models/hf/org/model/resolve/main/onnx/enc.onnx"); code != 200 || body != "weights" {
		t.Fatalf("first: %d %q", code, body)
	}
	if code, body := get("/models/hf/org/model/resolve/main/onnx/enc.onnx"); code != 200 || body != "weights" || hits.Load() != 1 {
		t.Fatalf("cached: %d %q hits=%d", code, body, hits.Load())
	}
	if code, _ := get("/models/hf/org/model/resolve/main/missing.json"); code != 404 {
		t.Fatalf("missing: %d", code)
	}
	if code, _ := get("/models/hf/org/../resolve/main/x"); code != 400 {
		t.Fatalf("traversal: %d", code)
	}
	if _, err := os.Stat(filepath.Join(c.Dir, "org/model/main/onnx/enc.onnx")); err != nil {
		t.Fatal(err)
	}
	get("/models/hf/org/model/resolve/main/config.json")
	list, _ := c.List()
	if len(list) != 1 || list[0].Repo != "org/model" || list[0].Size != int64(len("weights")+len(`{"a":1}`)) {
		t.Fatalf("list: %+v", list)
	}
	if err := c.Delete("org/model"); err != nil {
		t.Fatal(err)
	}
	if list, _ := c.List(); len(list) != 0 {
		t.Fatalf("after delete: %+v", list)
	}
	if c.Delete("../x") == nil {
		t.Fatal("bad repo accepted")
	}
}
