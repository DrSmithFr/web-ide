package capture

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestURL(t *testing.T) {
	if _, err := URL(context.Background(), "file:///etc/passwd", 0, 0); err == nil || !strings.Contains(err.Error(), "only http") {
		t.Fatalf("file URL: %v", err)
	}
	if Chromium() == "" {
		t.Skip("no Chromium on this machine")
	}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `<body style="background:#c00"><h1>Hello</h1></body>`)
	}))
	defer ts.Close()
	png, err := URL(context.Background(), ts.URL, 400, 300)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(png, []byte("\x89PNG")) {
		t.Fatalf("not a PNG: %.20q", png)
	}
}
