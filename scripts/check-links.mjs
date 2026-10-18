// Checks the relative links of the Markdown files tracked by git: the target exists and its
// #anchor is a heading of it (GitHub slugs), and the same for the targets of the "?" links of the
// interface (web/src/help/topics.json). Run by `make test`; prints the broken links.
import { execFileSync } from 'node:child_process'
import { existsSync, readFileSync, statSync } from 'node:fs'
import path from 'node:path'

const root = execFileSync('git', ['rev-parse', '--show-toplevel'], { encoding: 'utf8' }).trim()
const files = execFileSync('git', ['ls-files', '-co', '--exclude-standard', '*.md'], { cwd: root, encoding: 'utf8' })
  .split('\n')
  .filter((f) => f && !f.includes('node_modules/') && !f.startsWith('.ide/') && existsSync(path.join(root, f)))

// The text outside code fences and inline code, where links count.
function prose(text) {
  return text.replace(/^(```|~~~)[^\n]*\n[\s\S]*?^\1[^\n]*$/gm, '').replace(/`[^`\n]*`/g, '')
}

const slugCache = new Map()
function slugs(file) {
  if (slugCache.has(file)) return slugCache.get(file)
  const seen = new Map()
  const out = new Set()
  for (const m of prose(readFileSync(file, 'utf8')).matchAll(/^#{1,6}\s+(.+?)\s*#*$/gm)) {
    const base = m[1]
      .replace(/\[([^\]]*)\]\([^)]*\)/g, '$1')
      .toLowerCase()
      .replace(/[^\p{L}\p{N}\s_-]/gu, '')
      .replace(/\s/g, '-')
    const n = seen.get(base) ?? 0
    seen.set(base, n + 1)
    out.add(n ? `${base}-${n}` : base)
  }
  slugCache.set(file, out)
  return out
}

const broken = []
for (const f of files) {
  const abs = path.join(root, f)
  for (const m of prose(readFileSync(abs, 'utf8')).matchAll(/!?\[[^\]]*\]\(([^)\s]+)(?:\s+"[^"]*")?\)/g)) {
    const link = m[1]
    if (/^[a-z][a-z0-9+.-]*:/i.test(link) || link.startsWith('/')) continue
    const [target, anchor] = link.split('#')
    const dest = target ? path.resolve(path.dirname(abs), decodeURI(target)) : abs
    if (!existsSync(dest)) {
      broken.push(`${f}: ${link} (no such file)`)
    } else if (anchor && statSync(dest).isFile() && dest.endsWith('.md') && !slugs(dest).has(decodeURIComponent(anchor).toLowerCase())) {
      broken.push(`${f}: ${link} (no such heading)`)
    }
  }
}
// The places of the interface that have a page: topic → "path#heading", paths from the repo root.
const topicsFile = 'web/src/help/topics.json'
const topics = JSON.parse(readFileSync(path.join(root, topicsFile), 'utf8'))
for (const [topic, target] of Object.entries(topics)) {
  const [file, anchor] = target.split('#')
  const dest = path.join(root, file)
  if (!file.endsWith('.md') || !existsSync(dest)) broken.push(`${topicsFile}: ${topic} → ${target} (no such page)`)
  else if (anchor && !slugs(dest).has(anchor)) broken.push(`${topicsFile}: ${topic} → ${target} (no such heading)`)
}
if (broken.length) {
  console.error(`broken links:\n  ${broken.join('\n  ')}`)
  process.exit(1)
}
console.log(`links: ${files.length} Markdown files and ${Object.keys(topics).length} help topics checked`)
