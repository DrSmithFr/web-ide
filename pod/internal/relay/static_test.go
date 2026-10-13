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

// TestBootstrapPage: the page, its script and its styles are served, and none of them loads a
// resource from another origin or carries a script inline (the page must fit a strict CSP).
func TestBootstrapPage(t *testing.T) {
	st, _ := OpenStore(filepath.Join(t.TempDir(), "relay.db"))
	t.Cleanup(func() { st.Close() })
	ts := httptest.NewServer(New(st, nil).Handler())
	t.Cleanup(ts.Close)
	for path, ct := range map[string]string{
		"/":                     "text/html",
		"/_relay/bootstrap.css": "text/css",
		"/_relay/bootstrap.js":  "text/javascript",
	} {
		r, err := http.Get(ts.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(r.Body)
		r.Body.Close()
		body := string(b)
		if r.StatusCode != 200 || !strings.HasPrefix(r.Header.Get("Content-Type"), ct) {
			t.Fatalf("%s: %d %q", path, r.StatusCode, r.Header.Get("Content-Type"))
		}
		for _, bad := range []string{"@import", "url(http", `src="http`, `href="http`} {
			if strings.Contains(body, bad) {
				t.Fatalf("%s loads something from another origin: %q", path, bad)
			}
		}
		if path == "/" && (strings.Contains(body, "<script>") || strings.Contains(body, "<style")) {
			t.Fatal("the page carries a script or styles inline")
		}
		if path == "/_relay/bootstrap.js" && strings.Contains(body, "innerHTML") {
			t.Fatal("the page builds HTML from strings")
		}
	}
}
