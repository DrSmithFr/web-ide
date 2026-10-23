package install

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/DrSmithFr/web-ide/pod/store"
)

// fixture is the repository of a plugin "greet" with its tags, and the server of its archives.
type fixture struct {
	repo   string
	srv    *httptest.Server
	mu     sync.Mutex
	assets map[string][]byte // path -> archive
}

type release struct {
	version, ide string
	perms        []string
	plugins      map[string]string
}

func manifestOf(r release, assets string) []byte {
	m := map[string]any{"id": "greet", "name": "Greet", "version": r.version, "apiVersion": 1, "assets": assets,
		"contributes": map[string]any{"tools": []any{map[string]any{"id": "greet", "label": "Greet", "icon": "sparkle", "zone": "right"}}}}
	if r.ide != "" {
		m["ide"] = r.ide
	}
	if r.perms != nil {
		m["permissions"] = r.perms
	}
	if r.plugins != nil {
		m["plugins"] = r.plugins
	}
	data, _ := json.MarshalIndent(m, "", "  ")
	return data
}

func gitRun(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@x", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@x", "GIT_CONFIG_GLOBAL="+os.DevNull)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v %s", args, err, out)
	}
}

// entry of an archive: a file, or a link (link set).
type entry struct {
	name, body, link string
	typ              byte
}

func tarGz(t *testing.T, entries []entry) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, e := range entries {
		h := &tar.Header{Name: e.name, Mode: 0o644, Size: int64(len(e.body)), Typeflag: tar.TypeReg}
		if e.typ != 0 {
			h.Typeflag, h.Size = e.typ, 0
		}
		if e.link != "" {
			h.Typeflag, h.Linkname, h.Size = tar.TypeSymlink, e.link, 0
		}
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		if h.Size > 0 {
			tw.Write([]byte(e.body))
		}
	}
	tw.Close()
	gz.Close()
	return buf.Bytes()
}

func newFixture(t *testing.T, releases ...release) *fixture {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is needed")
	}
	f := &fixture{repo: t.TempDir(), assets: map[string][]byte{}}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		data, ok := f.assets[r.URL.Path]
		f.mu.Unlock()
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Write(data)
	}))
	t.Cleanup(f.srv.Close)
	assets := f.srv.URL + "/{version}/greet-{arch}.tar.gz"
	gitRun(t, f.repo, "init", "-q", "-b", "main")
	for _, r := range releases {
		m := manifestOf(r, assets)
		os.WriteFile(filepath.Join(f.repo, "plugin.json"), m, 0o644)
		gitRun(t, f.repo, "add", "-A")
		gitRun(t, f.repo, "commit", "-q", "-m", r.version)
		gitRun(t, f.repo, "tag", "v"+r.version)
		f.setAsset(r.version, tarGz(t, []entry{{name: "plugin.json", body: string(m)}, {name: "web/index.js", body: "export {}"}}))
	}
	return f
}

func (f *fixture) setAsset(version string, data []byte) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.assets["/"+version+"/greet-linux-amd64.tar.gz"] = data
}

func (f *fixture) installer(t *testing.T) *Installer {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return &Installer{Store: st, Arch: "linux-amd64", Resolver: Resolver{IDE: "v1.24.0-3-gabc", Installed: map[string]string{"git": "1.0.0"},
		Builtin: func(id string) bool { return id == "git" }, AllowHTTP: true}}
}

func TestResolveAndInstall(t *testing.T) {
	f := newFixture(t,
		release{version: "1.0.0", ide: "^1.20", perms: []string{"files:read"}},
		release{version: "1.1.0", ide: "^1.20", perms: []string{"files:read", "net"}, plugins: map[string]string{"git": "^1"}},
		release{version: "2.0.0", ide: "^99"},
	)
	in := f.installer(t)
	ctx := context.Background()

	// The highest version fitting the IDE: 2.0.0 asks ide ^99.
	c, err := in.Resolve(ctx, f.repo, "")
	if err != nil || c.Version.String() != "1.1.0" {
		t.Fatalf("resolve: %+v %v", c.Version, err)
	}
	if _, err := in.Resolve(ctx, f.repo, "^2"); err == nil || !strings.Contains(err.Error(), "needs the IDE ^99") {
		t.Fatalf("incompatible IDE: %v", err)
	}
	if _, err := in.Resolve(ctx, f.repo, "^3"); err == nil || !strings.Contains(err.Error(), "no version") {
		t.Fatalf("no match: %v", err)
	}
	in.Installed = map[string]string{"git": "0.9.0"}
	if _, err := in.Resolve(ctx, f.repo, "=1.1.0"); err == nil || !strings.Contains(err.Error(), "needs git ^1") {
		t.Fatalf("plugin requirement: %v", err)
	}
	in.Installed = map[string]string{}
	if _, err := in.Resolve(ctx, f.repo, "=1.1.0"); err == nil || !strings.Contains(err.Error(), "needs the git plugin") {
		t.Fatalf("missing plugin: %v", err)
	}
	in.Installed = map[string]string{"git": "1.0.0"}

	// Install 1.0.0, with a grant it does not ask: refused; then with its own.
	c, _ = in.Resolve(ctx, f.repo, "~1.0")
	if _, err := in.Install(ctx, c, "~1.0", []string{"exec"}); err == nil {
		t.Fatal("a grant not asked is refused")
	}
	l, err := in.Install(ctx, c, "~1.0", []string{"files:read"})
	if err != nil || l.Version != "1.0.0" || len(l.SHA256) != 64 {
		t.Fatalf("install: %+v %v", l, err)
	}
	if _, err := os.Stat(filepath.Join(in.Dir("greet"), "1.0.0", "web", "index.js")); err != nil {
		t.Fatal("extracted")
	}
	lock, _ := ReadLock(in.Store)
	if got := lock.Plugins["greet"]; got.URL != f.repo || got.Grants[0] != "files:read" || got.Constraint != "~1.0" {
		t.Fatalf("lock: %+v", got)
	}

	// The archive of a locked version changes: refused.
	f.setAsset("1.0.0", tarGz(t, []entry{{name: "plugin.json", body: string(manifestOf(release{version: "1.0.0", ide: "^1.20", perms: []string{"files:read"}}, f.srv.URL+"/{version}/greet-{arch}.tar.gz"))}, {name: "evil.js", body: "x"}}))
	if _, err := in.Install(ctx, c, "~1.0", []string{"files:read"}); err == nil || !strings.Contains(err.Error(), "changed since it was installed") {
		t.Fatalf("changed hash: %v", err)
	}

	// Update to 1.1.0: the new permission is one to accept; the old version goes.
	c, _ = in.Resolve(ctx, f.repo, "")
	if n := NewGrants(c.Manifest, lock.Plugins["greet"].Grants); len(n) != 1 || n[0] != "net" {
		t.Fatalf("new grants: %v", n)
	}
	if _, err := in.Install(ctx, c, "", []string{"files:read", "net"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(in.Dir("greet"), "1.0.0")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("the old version is removed")
	}
	if m, err := in.Manifest("greet", "1.1.0"); err != nil || m.Version != "1.1.0" {
		t.Fatalf("manifest: %v", err)
	}

	// Remove: the folder and the entry.
	if err := in.Remove("greet"); err != nil {
		t.Fatal(err)
	}
	lock, _ = ReadLock(in.Store)
	if _, ok := lock.Plugins["greet"]; ok {
		t.Fatal("removed from the lock")
	}
	if _, err := os.Stat(in.Dir("greet")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("folder removed")
	}
}

func TestUnsafeArchives(t *testing.T) {
	f := newFixture(t, release{version: "1.0.0"})
	in := f.installer(t)
	ctx := context.Background()
	c, err := in.Resolve(ctx, f.repo, "")
	if err != nil {
		t.Fatal(err)
	}
	m := string(manifestOf(release{version: "1.0.0"}, f.srv.URL+"/{version}/greet-{arch}.tar.gz"))
	for name, entries := range map[string][]entry{
		"dot dot":       {{name: "plugin.json", body: m}, {name: "../escape", body: "x"}},
		"absolute":      {{name: "plugin.json", body: m}, {name: "/etc/x", body: "x"}},
		"link out":      {{name: "plugin.json", body: m}, {name: "web/l", link: "../../.."}},
		"absolute link": {{name: "plugin.json", body: m}, {name: "l", link: "/etc/passwd"}},
		"device":        {{name: "plugin.json", body: m}, {name: "dev", typ: tar.TypeChar}},
		"other json":    {{name: "plugin.json", body: strings.Replace(m, `"Greet"`, `"Other"`, 1)}},
	} {
		f.setAsset("1.0.0", tarGz(t, entries))
		if _, err := in.Install(ctx, c, "", nil); err == nil {
			t.Errorf("%s: an unsafe archive is refused", name)
		}
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(in.Dir("greet")), "escape")); err == nil {
		t.Fatal("nothing written out of the folder")
	}
}

func TestUnsafeManifests(t *testing.T) {
	builtin := func(id string) bool { return id == "git" }
	ok := func(mut func(m map[string]any)) error {
		m := map[string]any{"id": "greet", "name": "Greet", "version": "1.0.0", "apiVersion": 1, "assets": "https://x/{arch}.tar.gz", "contributes": map[string]any{}}
		mut(m)
		data, _ := json.Marshal(m)
		_, err := Resolver{Builtin: builtin}.fits(data, Version{Major: 1})
		return err
	}
	if err := ok(func(map[string]any) {}); err != nil {
		t.Fatal(err)
	}
	if err := ok(func(m map[string]any) {
		m["backend"] = "bin/greet"
		m["contributes"] = map[string]any{"assistantTools": []string{"greet_hello"}, "toolSchemas": map[string]any{"greet_hello": map[string]any{"description": "Says hello", "properties": map[string]any{}}}}
	}); err != nil {
		t.Fatal("a backend and its tool:", err)
	}
	for name, mut := range map[string]func(m map[string]any){
		"unknown permission": func(m map[string]any) { m["permissions"] = []string{"root"} },
		"built-in id":        func(m map[string]any) { m["id"] = "git" },
		"no assets":          func(m map[string]any) { delete(m, "assets") },
		"locked":             func(m map[string]any) { m["required"] = true },
		"namespaces":         func(m map[string]any) { m["namespaces"] = []string{"fs"} },
		"other version":      func(m map[string]any) { m["version"] = "1.0.1" },
		"bad ide":            func(m map[string]any) { m["ide"] = "^x" },
		"backend out":        func(m map[string]any) { m["backend"] = "../../bin/sh" },
		"backend deep":       func(m map[string]any) { m["backend"] = "bin/a/b" },
		"tool no schema": func(m map[string]any) {
			m["backend"] = "bin/greet"
			m["contributes"] = map[string]any{"assistantTools": []string{"greet_hello"}}
		},
		"tool no backend": func(m map[string]any) {
			m["contributes"] = map[string]any{"assistantTools": []string{"greet_hello"}, "toolSchemas": map[string]any{"greet_hello": map[string]any{"description": "d"}}}
		},
		"mcp tools": func(m map[string]any) { m["contributes"] = map[string]any{"mcpTools": []string{"greet_x"}} },
	} {
		if ok(mut) == nil {
			t.Errorf("%s: refused", name)
		}
	}
	if !ValidGrant("core:fs.read") || ValidGrant("core:fs") || ValidGrant("files") {
		t.Fatal("grants")
	}
}

func TestCheckURL(t *testing.T) {
	r := Resolver{}
	for _, u := range []string{"https://github.com/a/b", "git@github.com:a/b.git", "ssh://git@host/a", t.TempDir()} {
		if err := r.CheckURL(u); err != nil {
			t.Errorf("%s: %v", u, err)
		}
	}
	for _, u := range []string{"", "http://example.com/a", "file:///etc", "ftp://x/y", "relative/path", "http://127.0.0.1/x"} {
		if r.CheckURL(u) == nil {
			t.Errorf("%s refused", u)
		}
	}
	if (Resolver{AllowHTTP: true}).CheckURL("http://127.0.0.1:9/x") != nil {
		t.Error("http to 127.0.0.1 in the tests")
	}
}
