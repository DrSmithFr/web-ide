package search

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/DrSmithFr/web-ide/pod/execx"
)

func TestLocalSearch(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "a.go"), []byte("package a\n// café Foo foo\nfunc FooBar() {}\n"), 0o644)
	os.MkdirAll(filepath.Join(dir, "node_modules"), 0o755)
	os.WriteFile(filepath.Join(dir, "node_modules", "x.js"), []byte("Foo"), 0o644)
	os.WriteFile(filepath.Join(dir, "bin"), []byte{0, 1, 'F', 'o', 'o'}, 0o644)

	r, err := Local(context.Background(), dir, Options{Query: "foo"})
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Matches) != 2 {
		t.Fatalf("matches = %+v", r.Matches)
	}
	// "é" is one UTF-16 unit: "// café " is 8 units before "Foo".
	if m := r.Matches[0]; m.Line != 2 || m.Col != 8 || m.Ranges[0] != [2]int{8, 11} || m.Ranges[1] != [2]int{12, 15} {
		t.Fatalf("first match = %+v", m)
	}
	r, _ = Local(context.Background(), dir, Options{Query: "foo", Case: true, Word: true})
	if len(r.Matches) != 1 || r.Matches[0].Line != 2 {
		t.Fatalf("case+word = %+v", r.Matches)
	}
	if _, err := Compile(Options{Query: "(", Regex: true}); err == nil {
		t.Fatal("invalid regex accepted")
	}
}

// Remote runs grep through a runner: the local one here, the SSH one on a host.
func TestRemoteSearch(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("hello over sftp\n"), 0o644)
	os.MkdirAll(filepath.Join(dir, "node_modules"), 0o755)
	os.WriteFile(filepath.Join(dir, "node_modules", "b.txt"), []byte("sftp"), 0o644)
	r, err := Remote(context.Background(), execx.Local{}, dir, Options{Query: "sftp"})
	if err != nil || len(r.Matches) != 1 || r.Matches[0].Line != 1 {
		t.Fatalf("remote search = %+v %v", r, err)
	}
}
