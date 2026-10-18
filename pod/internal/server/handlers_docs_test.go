package server

import (
	"strings"
	"testing"
	"testing/fstest"
)

func TestDocs(t *testing.T) {
	s, ts := newServer(t)
	s.Docs = fstest.MapFS{
		"docs/guide.md":     {Data: []byte("# Guide")},
		"docs/images/a.png": {Data: []byte("png")},
		"docs/images/b.svg": {Data: []byte("<svg/>")},
		"pod/internal/x.go": {Data: []byte("package x")},
	}
	w, err := dial(t, ts, s.Token)
	if err != nil {
		t.Fatal(err)
	}
	read := func(p string) map[string]any { return w.callRaw("docs.read", map[string]string{"path": p}) }
	if r := read("docs/guide.md"); r["result"].(map[string]any)["text"] != "# Guide" {
		t.Fatalf("read: %v", r)
	}
	if r := read("/docs/../docs/guide.md"); r["error"] != nil {
		t.Fatalf("a clean path inside the tree reads: %v", r)
	}
	for _, p := range []string{"../go.mod", "docs/../../etc/passwd.md", "/etc/passwd", "docs/missing.md", "pod/internal/x.go", "docs", ""} {
		if r := read(p); r["error"] == nil {
			t.Errorf("%q must be refused: %v", p, r)
		}
	}
	img := w.callRaw("docs.image", map[string]string{"path": "docs/images/a.png"})
	if u, _ := img["result"].(map[string]any)["url"].(string); !strings.HasPrefix(u, "data:image/png;base64,") {
		t.Fatalf("image: %v", img)
	}
	if r := w.callRaw("docs.image", map[string]string{"path": "docs/guide.md"}); r["error"] == nil {
		t.Fatal("a page is not an image")
	}
	s.Docs = nil
	if r := read("docs/guide.md"); r["error"] == nil {
		t.Fatal("no docs: an error, not a panic")
	}
}
