package relay

import (
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
)

// TestAppFiles: the bootstrap page links the manifest of the installable app, and the relay
// serves it and its icons from the front end of the pod; other asset paths stay the pod's.
func TestAppFiles(t *testing.T) {
	st, _ := OpenStore(filepath.Join(t.TempDir(), "relay.db"))
	t.Cleanup(func() { st.Close() })
	srv := New(st, nil)
	srv.App = fstest.MapFS{
		"manifest.webmanifest": {Data: []byte(`{"name":"Web IDE"}`)},
		"icon-192.png":         {Data: []byte("PNG")},
	}
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	get := func(path string) (int, string, string) {
		r, err := http.Get(ts.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		defer r.Body.Close()
		b, _ := io.ReadAll(r.Body)
		return r.StatusCode, r.Header.Get("Content-Type"), string(b)
	}
	if code, ct, body := get("/manifest.webmanifest"); code != 200 || ct != "application/manifest+json" || !strings.Contains(body, "Web IDE") {
		t.Fatalf("manifest: %d %q %q", code, ct, body)
	}
	if code, ct, _ := get("/icon-192.png"); code != 200 || ct != "image/png" {
		t.Fatalf("icon: %d %q", code, ct)
	}
	if code, _, _ := get("/icon-512.png"); code != 404 {
		t.Fatalf("a missing icon: %d", code)
	}
	if code, _, _ := get("/assets/app.js"); code != 404 {
		t.Fatalf("an asset of the app is not the relay's: %d", code)
	}
	if _, _, body := get("/"); !strings.Contains(body, `rel="manifest"`) {
		t.Fatal("the bootstrap page links the manifest")
	}
}
