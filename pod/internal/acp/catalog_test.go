package acp

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCleanStaleNpm(t *testing.T) {
	dir := t.TempDir()
	for _, d := range []string{".body-parser-j9aPlldF", "body-parser", ".bin", "@scope/.zod-AbCd1234", "@scope/zod"} {
		os.MkdirAll(filepath.Join(dir, d), 0o755)
	}
	os.WriteFile(filepath.Join(dir, ".package-lock.json"), nil, 0o644)
	cleanStaleNpm(dir)
	for d, want := range map[string]bool{".body-parser-j9aPlldF": false, "body-parser": true, ".bin": true, ".package-lock.json": true, "@scope/.zod-AbCd1234": false, "@scope/zod": true} {
		if _, err := os.Stat(filepath.Join(dir, d)); (err == nil) != want {
			t.Errorf("%s kept = %v, want %v", d, err == nil, want)
		}
	}
}

func TestNpmErrors(t *testing.T) {
	out := "npm warn EBADENGINE Unsupported engine {\nnpm warn EBADENGINE }\nnpm error code ENOTEMPTY\nnpm error syscall rename\n"
	if got := npmErrors(out); got != "npm error code ENOTEMPTY\nnpm error syscall rename" {
		t.Errorf("npmErrors = %q", got)
	}
	if got := npmErrors("boom"); got != "boom" {
		t.Errorf("npmErrors without error lines = %q", got)
	}
}
