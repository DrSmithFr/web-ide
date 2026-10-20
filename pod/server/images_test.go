package server

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/DrSmithFr/web-ide/pod/fsx"
)

func TestReadImage(t *testing.T) {
	dir := t.TempDir()
	png := filepath.Join(dir, "a.PNG")
	_ = os.WriteFile(png, []byte("png"), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "a.txt"), []byte("text"), 0o644)
	r, err := readImage(fsx.Local{}, png)
	if err != nil || r["url"] != "data:image/png;base64,cG5n" {
		t.Fatalf("image: %v %v", r, err)
	}
	if _, err := readImage(fsx.Local{}, filepath.Join(dir, "a.txt")); err == nil {
		t.Fatal("a text file is not an image")
	}
	if _, err := readImage(fsx.Local{}, filepath.Join(dir, "missing.png")); err == nil {
		t.Fatal("a missing image is an error")
	}
}
