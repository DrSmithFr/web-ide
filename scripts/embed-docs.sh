#!/bin/sh
# Copies the documentation into pod/docsdist/content for go:embed (go:embed cannot reach ../docs):
# the Markdown files and pictures of docs/, GLOSSARY.md and the READMEs of the code, at their repo
# paths so the relative links between them keep working. Run by make on every build.
set -e
root=$(cd "$(dirname "$0")/.." && pwd)
out=$root/pod/docsdist/content
rm -rf "$out"
mkdir -p "$out"
cd "$root"
for f in README.md AGENTS.md GLOSSARY.md pod/README.md pod/packages.md relay/README.md lib/README.md plugins/README.md plugins/*/README.md web/README.md web/src/README.md e2e/README.md; do
  [ -f "$f" ] || continue
  mkdir -p "$out/$(dirname "$f")"
  cp "$f" "$out/$f"
done
(cd "$root" && find docs -type f \( -name '*.md' -o -name '*.png' -o -name '*.gif' -o -name '*.svg' \)) | while read -r f; do
  mkdir -p "$out/$(dirname "$f")"
  cp "$f" "$out/$f"
done
