package agent

import "testing"

func TestRunsFreely(t *testing.T) {
	const root = "/p"
	for _, c := range []struct {
		cmd  string
		cwd  string
		free bool
	}{
		{"ls -la", "", true},
		{"cat a.go | grep x | wc -l", "", true},
		{"git log --oneline -5 && git diff HEAD~1", "", true},
		{"git -C /elsewhere status", "", true},
		{"git push", "", false},
		{"git commit -m 'x'", "", false},
		{"go test ./...", "", true},
		{"go test ./...", "/tmp", false},
		{"cd /tmp && go test ./...", "", false},
		{"cd sub && make", "", true},
		{"make install", "", false},
		{"npm run check", "", true},
		{"npm install", "", false},
		{"npx tsc --noEmit", "", true},
		{"npx prettier --write .", "", false},
		{"rm -rf build", "", false},
		{"echo x > file.txt", "", false},
		{"go vet ./... 2>&1 > /dev/null", "", true},
		{"go test ./... > /tmp/out.log 2>&1", "", true},
		{"sed -i s/a/b/ f", "", false},
		{"sed -n 1,5p f", "", true},
		{"find . -name '*.go' -delete", "", false},
		{"find . -name '*.go'", "", true},
		{"echo $(git rev-parse HEAD)", "", true},
		{"echo $(rm -rf x)", "", false},
		{"grep 'a|b;c' file", "", true},
		{"LANG=C timeout 60 grep -r x .", "", true},
		{"node scripts/check.mjs", "", true},
		{"node -e 'require(\"fs\").rmSync(\"x\")'", "", false},
		{"./run.sh", "", true},
		{"/usr/bin/evil", "", false},
		{"curl http://x", "", false},
		{"docker compose ps", "", true},
		{"docker compose up -d", "", false},
		{"awk '{ system(\"rm x\") }' f", "", false},
	} {
		if got := RunsFreely(c.cmd, root, c.cwd); got != c.free {
			t.Errorf("RunsFreely(%q, cwd %q) = %v, want %v", c.cmd, c.cwd, got, c.free)
		}
	}
}
