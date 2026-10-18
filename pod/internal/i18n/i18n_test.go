package i18n

import (
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

// Every message written with New / Errorf / T in the pod has a translation in every language.
func TestCatalogComplete(t *testing.T) {
	call := regexp.MustCompile(`i18n\.(?:New|Errorf|T\([^,]+,)\s*\(?\s*("(?:[^"\\]|\\.)*")`)
	missing := 0
	err := filepath.Walk("../..", func(path string, info os.FileInfo, err error) error {
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
				if _, ok := catalogs[lang][key]; !ok {
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

var verb = regexp.MustCompile(`%[-+# 0-9.]*[a-zA-Z]`)

// The catalogs of the languages have the same keys (no extra, none missing) and a translation
// keeps the format verbs of its key, in the same number.
func TestCatalogsAgree(t *testing.T) {
	if Languages[0] != "en" || len(Languages) < 2 {
		t.Fatalf("Languages: %v", Languages)
	}
	for _, lang := range Languages[1:] {
		for key, text := range catalogs[lang] {
			if _, ok := catalogs[Languages[1]][key]; !ok {
				t.Errorf("%s: %q is not in the %s catalog", lang, key, Languages[1])
			}
			want := strings.Join(verb.FindAllString(strings.ReplaceAll(key, "%%", ""), -1), " ")
			got := strings.Join(verb.FindAllString(strings.ReplaceAll(text, "%%", ""), -1), " ")
			if want != got && sorted(want) != sorted(got) {
				t.Errorf("%s: verbs of %q: %q", lang, key, text)
			}
		}
		for key := range catalogs[Languages[1]] {
			if _, ok := catalogs[lang][key]; !ok {
				t.Errorf("%s: missing %q", lang, key)
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
