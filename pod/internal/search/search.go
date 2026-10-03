// Package search implements the global search of a project (contents and file names).
package search

import (
	"bufio"
	"bytes"
	"context"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/DrSmithFr/web-ide/pod/internal/execx"
)

var skipDirs = map[string]bool{".git": true, "node_modules": true, ".ide": true, ".idea": true, "__pycache__": true, ".venv": true, ".cache": true}

const maxFileSize = 2 << 20

type Options struct {
	Query   string `json:"query"`
	Regex   bool   `json:"regex"`
	Case    bool   `json:"caseSensitive"`
	Word    bool   `json:"wholeWord"`
	Include string `json:"include"` // comma separated globs on the file name, e.g. "*.go,*.ts"
	Max     int    `json:"max"`
}

type Match struct {
	Path   string   `json:"path"`
	Line   int      `json:"line"`
	Col    int      `json:"col"` // UTF-16 column of the first match in the whole line
	Text   string   `json:"text"`
	Ranges [][2]int `json:"ranges"` // UTF-16 offsets in Text, as JavaScript indexes strings
}

type Result struct {
	Matches   []Match `json:"matches"`
	Truncated bool    `json:"truncated"`
	Files     int     `json:"files"`
}

func Compile(o Options) (*regexp.Regexp, error) {
	q := o.Query
	if !o.Regex {
		q = regexp.QuoteMeta(q)
	}
	if o.Word {
		q = `\b(?:` + q + `)\b`
	}
	if !o.Case {
		q = `(?i)` + q
	}
	return regexp.Compile(q)
}

func included(name, include string) bool {
	if strings.TrimSpace(include) == "" {
		return true
	}
	for _, g := range strings.Split(include, ",") {
		if ok, _ := path.Match(strings.TrimSpace(g), name); ok {
			return true
		}
	}
	return false
}

func utf16Len(s string) int {
	n := 0
	for _, r := range s {
		n += len(utf16.Encode([]rune{r}))
	}
	return n
}

func matchLine(re *regexp.Regexp, p string, line int, text string) (Match, bool) {
	if !utf8.ValidString(text) {
		return Match{}, false
	}
	locs := re.FindAllStringIndex(text, 50)
	if len(locs) == 0 {
		return Match{}, false
	}
	// Long lines are cut around the first match.
	cut := 0
	if len(text) > 400 && locs[0][0] > 100 {
		cut = locs[0][0] - 80
		for cut > 0 && !utf8.RuneStart(text[cut]) {
			cut--
		}
	}
	shown := text[cut:]
	if len(shown) > 400 {
		end := 400
		for end < len(shown) && !utf8.RuneStart(shown[end]) {
			end++
		}
		shown = shown[:end]
	}
	m := Match{Path: p, Line: line, Col: utf16Len(text[:locs[0][0]]), Text: shown}
	for _, l := range locs {
		s, e := l[0]-cut, l[1]-cut
		if s < 0 || e > len(shown) {
			continue
		}
		m.Ranges = append(m.Ranges, [2]int{utf16Len(shown[:s]), utf16Len(shown[:e])})
	}
	return m, true
}

func Local(ctx context.Context, root string, o Options) (*Result, error) {
	re, err := Compile(o)
	if err != nil {
		return nil, err
	}
	max := o.Max
	if max <= 0 {
		max = 2000
	}
	res := &Result{Matches: []Match{}}
	err = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if skipDirs[d.Name()] && p != root {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() || !included(d.Name(), o.Include) {
			return nil
		}
		if fi, err := d.Info(); err != nil || fi.Size() > maxFileSize {
			return nil
		}
		data, err := os.ReadFile(p)
		if err != nil || bytes.IndexByte(data[:min(len(data), 8000)], 0) >= 0 {
			return nil
		}
		res.Files++
		sc := bufio.NewScanner(bytes.NewReader(data))
		sc.Buffer(make([]byte, 64*1024), maxFileSize)
		n := 0
		for sc.Scan() {
			n++
			if m, ok := matchLine(re, p, n, sc.Text()); ok {
				res.Matches = append(res.Matches, m)
				if len(res.Matches) >= max {
					res.Truncated = true
					return filepath.SkipAll
				}
			}
		}
		return nil
	})
	return res, err
}

func excludes() []string {
	var out []string
	for d := range skipDirs {
		out = append(out, "--exclude-dir="+d)
	}
	return out
}

// Remote runs grep on the SSH host, then computes the match ranges locally.
func Remote(ctx context.Context, r execx.Runner, root string, o Options) (*Result, error) {
	re, err := Compile(o)
	if err != nil {
		return nil, err
	}
	max := o.Max
	if max <= 0 {
		max = 2000
	}
	args := []string{"grep", "-rnI"}
	if o.Regex {
		args = append(args, "-E")
	} else {
		args = append(args, "-F")
	}
	if !o.Case {
		args = append(args, "-i")
	}
	if o.Word {
		args = append(args, "-w")
	}
	args = append(args, excludes()...)
	for _, g := range strings.Split(o.Include, ",") {
		if g = strings.TrimSpace(g); g != "" {
			args = append(args, "--include="+g)
		}
	}
	args = append(args, "-e", o.Query, "--", ".")
	cmd := execx.Join(args) + " | head -n " + strconv.Itoa(max+1)
	out, err := r.Output(ctx, []string{"sh", "-c", cmd}, root)
	if err != nil && len(out) == 0 {
		return &Result{Matches: []Match{}}, nil // grep exits 1 when nothing matches
	}
	res := &Result{Matches: []Match{}}
	files := map[string]bool{}
	for _, line := range strings.Split(strings.TrimRight(string(out), "\n"), "\n") {
		p, rest, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		ln, text, ok := strings.Cut(rest, ":")
		if !ok {
			continue
		}
		n, _ := strconv.Atoi(ln)
		full := path.Join(root, p)
		files[full] = true
		if m, ok := matchLine(re, full, n, text); ok {
			if len(res.Matches) >= max {
				res.Truncated = true
				break
			}
			res.Matches = append(res.Matches, m)
		}
	}
	res.Files = len(files)
	return res, nil
}

const maxFiles = 50000

// FilesLocal lists the project files (for "go to file").
func FilesLocal(ctx context.Context, root string) ([]string, error) {
	var out []string
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if skipDirs[d.Name()] && p != root {
				return filepath.SkipDir
			}
			return nil
		}
		out = append(out, p)
		if len(out) >= maxFiles {
			return filepath.SkipAll
		}
		return nil
	})
	return out, err
}

func FilesRemote(ctx context.Context, r execx.Runner, root string) ([]string, error) {
	var prune []string
	for d := range skipDirs {
		if len(prune) > 0 {
			prune = append(prune, "-o")
		}
		prune = append(prune, "-name", d)
	}
	args := append([]string{"find", ".", "-type", "d", "("}, prune...)
	args = append(args, ")", "-prune", "-o", "-type", "f", "-print")
	out, err := r.Output(ctx, []string{"sh", "-c", execx.Join(args) + " | head -n " + strconv.Itoa(maxFiles)}, root)
	if err != nil && len(out) == 0 {
		return nil, err
	}
	var files []string
	for _, l := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if l != "" {
			files = append(files, path.Join(root, l))
		}
	}
	return files, nil
}
