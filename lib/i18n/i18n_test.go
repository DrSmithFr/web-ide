package i18n

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

func TestTranslate(t *testing.T) {
	// The test adds entries to the French catalog: put it back for the other tests.
	saved := maps.Clone(catalogs["fr"])
	t.Cleanup(func() { catalogs["fr"] = saved })
	catalogs["fr"] = maps.Clone(saved)
	catalogs["fr"]["file %s not found"] = "fichier %s introuvable"
	catalogs["fr"]["reading %s: %w"] = "lecture de %s : %w"
	catalogs["fr"]["empty"] = "vide"
	catalogs["fr"]["status %s"] = "état %s"
	catalogs["fr"]["New"] = "Nouveau"

	err := Errorf("file %s not found", "a.go")
	if err.Error() != "file a.go not found" || Translate("fr", err) != "fichier a.go introuvable" || Translate("en", err) != "file a.go not found" {
		t.Fatalf("Errorf: %q %q", err, Translate("fr", err))
	}
	if Translate("fr", New("empty")) != "vide" || Translate("fr", New("other")) != "other" {
		t.Fatal("New")
	}
	// The wrapped error is translated too, and errors.Is still sees it.
	inner := New("empty")
	outer := Errorf("reading %s: %w", "x", inner)
	if Translate("fr", outer) != "lecture de x : vide" || !errors.Is(outer, inner) {
		t.Fatalf("wrap: %q", Translate("fr", outer))
	}
	if Translate("fr", Errorf("status %s", Text("New"))) != "état Nouveau" {
		t.Fatal("Text argument")
	}
	// An error wrapping a translatable one keeps its own text.
	if Translate("fr", fmt.Errorf("git: %w", inner)) != "git: empty" {
		t.Fatal("foreign wrapper")
	}
}

// pluginCatalogs are the catalogs of the built-in plugins (plugins/<id>/i18n), read from the
// repository: by plugin id, then language.
func pluginCatalogs(t *testing.T) map[string]map[string]map[string]string {
	t.Helper()
	out := map[string]map[string]map[string]string{}
	dirs, _ := filepath.Glob("../../plugins/*/i18n")
	for _, dir := range dirs {
		id := filepath.Base(filepath.Dir(dir))
		out[id] = map[string]map[string]string{}
		for _, lang := range Languages[1:] {
			data, err := os.ReadFile(filepath.Join(dir, lang+".json"))
			if err != nil {
				t.Fatal(err)
			}
			c := map[string]string{}
			if err := json.Unmarshal(data, &c); err != nil {
				t.Fatalf("%s/%s.json: %v", dir, lang, err)
			}
			out[id][lang] = c
		}
	}
	return out
}

// pluginOf is the built-in plugin of a file of the repository ("" for the core).
func pluginOf(path string) string {
	rel, _ := filepath.Rel("../..", path)
	parts := strings.Split(filepath.ToSlash(rel), "/")
	if len(parts) > 2 && parts[0] == "plugins" {
		return parts[1]
	}
	return ""
}

// Every message written with New / Errorf / T in the Go code (pod/, relay/, lib/, plugins/)
// has a translation in every language: in the catalog of its plugin (plugins/<id>/i18n) or the
// core's.
func TestCatalogComplete(t *testing.T) {
	plugins := pluginCatalogs(t)
	call := regexp.MustCompile(`i18n\.(?:New|Errorf|T\([^,]+,)\s*\(?\s*("(?:[^"\\]|\\.)*")`)
	missing := 0
	err := filepath.Walk("../..", func(path string, info os.FileInfo, err error) error {
		if err == nil && info.IsDir() && path != "../.." && (info.Name() == "node_modules" || strings.HasPrefix(info.Name(), ".")) {
			return filepath.SkipDir
		}
		if err != nil || info.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, m := range call.FindAllStringSubmatch(string(data), -1) {
			key, err := strconv.Unquote(m[1])
			if err != nil {
				t.Errorf("%s: %v", path, err)
				continue
			}
			for _, lang := range Languages[1:] {
				_, core := catalogs[lang][key]
				_, own := plugins[pluginOf(path)][lang][key]
				if !core && !own {
					t.Errorf("%s: missing %s translation of %q", path, lang, key)
					missing++
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if missing > 0 {
		t.Logf("%d missing", missing)
	}
}

var verb = regexp.MustCompile(`%[-+#0-9.]*[a-zA-Z]`) // no space flag: "{n} % du" is text

// The catalogs of the languages have the same keys (no extra, none missing) and a translation
// keeps the format verbs of its key, in the same number: the core's and each plugin's. A key
// of a plugin is not in the core's (the pod would show the core's text, the page the plugin's).
func TestCatalogsAgree(t *testing.T) {
	if Languages[0] != "en" || len(Languages) < 2 {
		t.Fatalf("Languages: %v", Languages)
	}
	agree(t, "lib/i18n", catalogs)
	for id, c := range pluginCatalogs(t) {
		agree(t, "plugins/"+id+"/i18n", c)
		for key := range c[Languages[1]] {
			if _, ok := catalogs[Languages[1]][key]; ok {
				t.Errorf("plugins/%s/i18n: %q is in the core catalog too", id, key)
			}
		}
	}
}

func agree(t *testing.T, where string, cats map[string]map[string]string) {
	for _, lang := range Languages[1:] {
		for key, text := range cats[lang] {
			if _, ok := cats[Languages[1]][key]; !ok {
				t.Errorf("%s: %s: %q is not in the %s catalog", where, lang, key, Languages[1])
			}
			want := strings.Join(verb.FindAllString(strings.ReplaceAll(key, "%%", ""), -1), " ")
			got := strings.Join(verb.FindAllString(strings.ReplaceAll(text, "%%", ""), -1), " ")
			if want != got && sorted(want) != sorted(got) {
				t.Errorf("%s: %s: verbs of %q: %q", where, lang, key, text)
			}
		}
		for key := range cats[Languages[1]] {
			if _, ok := cats[lang][key]; !ok {
				t.Errorf("%s: %s: missing %q", where, lang, key)
			}
		}
	}
}

func sorted(s string) string {
	f := strings.Fields(s)
	sort.Strings(f)
	return strings.Join(f, " ")
}

func TestDetect(t *testing.T) {
	for header, want := range map[string]string{
		"":                        "en",
		"fr-FR,fr;q=0.9,en;q=0.8": "fr",
		"es-ES,es;q=0.9":          "es",
		"de,es;q=0.5":             "es",
		"de-DE":                   "en",
		"EN-us":                   "en",
	} {
		if got := Detect(header); got != want {
			t.Errorf("Detect(%q) = %q, want %q", header, got, want)
		}
	}
}
