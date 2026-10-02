package runtime

import (
	"context"
	"path"
	"sort"
	"strings"
	"sync"
	"time"

	"webide/pod/internal/search"
)

// Files lists the project files, cached for a few seconds.
func (r *Runtime) Files(ctx context.Context) ([]string, error) {
	filesCache.Lock()
	c, ok := filesCache.m[r]
	filesCache.Unlock()
	if ok && time.Since(c.at) < 15*time.Second {
		return c.files, nil
	}
	var files []string
	var err error
	if r.Local {
		files, err = search.FilesLocal(ctx, r.Root)
	} else {
		files, err = search.FilesRemote(ctx, r.Runner, r.Root)
	}
	if err != nil {
		return nil, err
	}
	filesCache.Lock()
	filesCache.m[r] = cachedFiles{files: files, at: time.Now()}
	filesCache.Unlock()
	return files, nil
}

type cachedFiles struct {
	files []string
	at    time.Time
}

var filesCache = struct {
	sync.Mutex
	m map[*Runtime]cachedFiles
}{m: map[*Runtime]cachedFiles{}}

func splitName(p string) (stem, ext string) {
	base := path.Base(p)
	ext = path.Ext(base)
	stem = strings.TrimSuffix(base, ext)
	// x.test.ts, x.spec.ts
	for _, s := range []string{".test", ".spec"} {
		if strings.HasSuffix(stem, s) {
			return strings.TrimSuffix(stem, s), ext
		}
	}
	return stem, ext
}

// isTest tells whether a path follows a test naming convention, and the source stem it tests.
func isTest(p string) (bool, string) {
	base := path.Base(p)
	ext := path.Ext(base)
	stem := strings.TrimSuffix(base, ext)
	switch {
	case strings.HasSuffix(stem, ".test"), strings.HasSuffix(stem, ".spec"):
		s, _ := splitName(p)
		return true, s
	case ext == ".go" && strings.HasSuffix(stem, "_test"):
		return true, strings.TrimSuffix(stem, "_test")
	case ext == ".py" && strings.HasPrefix(stem, "test_"):
		return true, strings.TrimPrefix(stem, "test_")
	case ext == ".py" && strings.HasSuffix(stem, "_test"):
		return true, strings.TrimSuffix(stem, "_test")
	case strings.HasSuffix(stem, "Test") && len(stem) > 4:
		return true, strings.TrimSuffix(stem, "Test")
	}
	return false, ""
}

// testNames lists the file names a test of p may have.
func (r *Runtime) testNames(p string) []string {
	stem, ext := splitName(p)
	var names []string
	switch ext {
	case ".go":
		names = []string{stem + "_test.go"}
	case ".py":
		names = []string{"test_" + stem + ".py", stem + "_test.py"}
	case ".php":
		names = []string{stem + "Test.php"}
	case ".ts", ".tsx", ".js", ".jsx", ".mjs", ".cjs", ".mts", ".cts":
		for _, e := range []string{ext, ".ts", ".tsx", ".js", ".jsx"} {
			names = append(names, stem+".test"+e, stem+".spec"+e)
		}
	default:
		names = []string{stem + "Test" + ext, stem + "_test" + ext, stem + ".test" + ext}
	}
	if pat, ok := r.Config.Tests[ext]; ok {
		names = append(names, strings.ReplaceAll(pat, "{name}", stem))
	}
	return names
}

type Related struct {
	IsTest  bool     `json:"isTest"`
	Tests   []string `json:"tests"`
	Sources []string `json:"sources"`
	Related []string `json:"related"`
}

// commonPrefix ranks candidates: the closest directory first.
func rank(p string, list []string) {
	score := func(c string) int {
		a, b := strings.Split(path.Dir(p), "/"), strings.Split(path.Dir(c), "/")
		n := 0
		for n < len(a) && n < len(b) && a[n] == b[n] {
			n++
		}
		return n*1000 - len(c)
	}
	sort.SliceStable(list, func(i, j int) bool { return score(list[i]) > score(list[j]) })
}

// FindRelated finds the tests of a source file, the sources of a test file, and the files
// sharing its name (navigation "symboles liés" and "tests").
func (r *Runtime) FindRelated(ctx context.Context, p string) (*Related, error) {
	files, err := r.Files(ctx)
	if err != nil {
		return nil, err
	}
	out := &Related{Tests: []string{}, Sources: []string{}, Related: []string{}}
	test, srcStem := isTest(p)
	out.IsTest = test
	_, ext := splitName(p)
	wanted := map[string]bool{}
	if test {
		for _, e := range []string{ext, ".ts", ".tsx", ".js", ".jsx"} {
			wanted[srcStem+e] = true
		}
	} else {
		for _, n := range r.testNames(p) {
			wanted[n] = true
		}
	}
	stem, _ := splitName(p)
	if test {
		stem = srcStem
	}
	for _, f := range files {
		if f == p {
			continue
		}
		base := path.Base(f)
		switch {
		case wanted[base] && test:
			out.Sources = append(out.Sources, f)
		case wanted[base]:
			out.Tests = append(out.Tests, f)
		default:
			s, _ := splitName(f)
			if t, ts := isTest(f); t {
				s = ts
			}
			if strings.EqualFold(s, stem) {
				out.Related = append(out.Related, f)
			}
		}
	}
	rank(p, out.Tests)
	rank(p, out.Sources)
	rank(p, out.Related)
	if len(out.Related) > 50 {
		out.Related = out.Related[:50]
	}
	return out, nil
}
