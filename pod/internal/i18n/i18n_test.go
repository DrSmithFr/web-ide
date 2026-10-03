package i18n

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

func TestTranslate(t *testing.T) {
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

// Every message written with New / Errorf / T in the pod has a French translation.
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
			if _, ok := catalogs["fr"][key]; !ok {
				t.Errorf("%s: missing French translation of %q", path, key)
				missing++
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
