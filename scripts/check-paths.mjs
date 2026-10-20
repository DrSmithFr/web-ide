// Checks the paths of the repository written as inline code in the Markdown files tracked by
// git (`pod/server/server.go`, `relay/web/`): each one exists. The links are checked by
// check-links.mjs; this catches a path left behind when code moves. A path with a placeholder
// (`<id>`, `*`, `{a,b}`) is skipped; `pod/multipod.RemoteGateway` (a Go symbol) and
// `web/src/main.tsx:12` (a line) check the file. Run by `make test`; prints the missing paths.
import { execFileSync } from 'node:child_process'
import { existsSync, readFileSync } from 'node:fs'
import path from 'node:path'

const root = execFileSync('git', ['rev-parse', '--show-toplevel'], { encoding: 'utf8' }).trim()
const files = execFileSync('git', ['ls-files', '-co', '--exclude-standard', '*.md'], { cwd: root, encoding: 'utf8' })
  .split('\n')
  .filter((f) => f && !f.includes('node_modules/') && !f.startsWith('.ide/') && existsSync(path.join(root, f)))

// The folders of the repository a path written in the docs starts with.
const tops = ['pod', 'relay', 'lib', 'web', 'e2e', 'scripts', 'docs', 'claude-mod']
const shape = new RegExp(`^(?:${tops.join('|')})/[\\w./-]*$`)

// Generated or git-ignored paths the docs name on purpose, and the German catalogs: the example
// of add-translation.md, files the reader creates.
const ignored = [/^pod\/webdist\/dist/, /^pod\/docsdist\/content/, /^web\/dist/, /^e2e\/node_modules/, /\/i18n\/de\.json$/]

// A path is also read from the folder of its doc (`scripts/i18n-check.mjs` in web/README.md).
function exists(p, doc) {
  const candidates = [p, p.replace(/:\d+(?:-\d+)?$/, ''), p.replace(/\.[A-Z]\w*(?:\.\w+)*$/, '')]
  return candidates.some((c) => existsSync(path.join(root, c)) || existsSync(path.join(root, path.dirname(doc), c)))
}

const missing = []
for (const f of files) {
  const text = readFileSync(path.join(root, f), 'utf8').replace(/^(```|~~~)[^\n]*\n[\s\S]*?^\1[^\n]*$/gm, '')
  for (const m of text.matchAll(/`([^`\n]+)`/g)) {
    const p = m[1].trim().replace(/[.,;:)]+$/, '')
    if (!shape.test(p) || ignored.some((r) => r.test(p)) || exists(p, f)) continue
    missing.push(`${f}: ${p}`)
  }
}
if (missing.length) {
  console.error(`missing paths (${missing.length}):\n` + missing.join('\n'))
  process.exit(1)
}
console.log(`paths: ${files.length} Markdown files checked`)
