// Checks the translation catalogs: every literal passed to t() / tn() in the sources must
// have an entry in each catalog. Keys used through variables (labels of data tables) cannot
// be checked here; catalog entries never seen as literals are listed with --unused.
import { readFileSync, readdirSync, statSync } from 'node:fs'
import { join, relative } from 'node:path'

const src = new URL('../src/', import.meta.url).pathname
const catalogs = { fr: JSON.parse(readFileSync(join(src, 'i18n/fr.json'), 'utf8')) }

const files = []
const walk = (d) => {
  for (const name of readdirSync(d)) {
    const p = join(d, name)
    if (statSync(p).isDirectory()) walk(p)
    else if (/\.(ts|tsx)$/.test(name)) files.push(p)
  }
}
walk(src)

const str = String.raw`'((?:\\.|[^'\\])*)'|"((?:\\.|[^"\\])*)"`
const call = new RegExp(String.raw`(?<![\w.$])t\(\s*(?:${str})`, 'g')
const plural = new RegExp(String.raw`(?<![\w.$])tn\([^,]+,\s*(?:${str})\s*,\s*(?:${str})`, 'g')
const unescape = (s) => s.replace(/\\(.)/g, (_, c) => ({ n: '\n', t: '\t' })[c] ?? c)

const used = new Map()
for (const f of files) {
  const text = readFileSync(f, 'utf8')
  const add = (k, i) => k !== undefined && !used.has(unescape(k)) && used.set(unescape(k), `${relative(src, f)}:${text.slice(0, i).split('\n').length}`)
  for (const m of text.matchAll(call)) add(m[1] ?? m[2], m.index)
  for (const m of text.matchAll(plural)) {
    add(m[1] ?? m[2], m.index)
    add(m[3] ?? m[4], m.index)
  }
}

let missing = 0
for (const [lang, cat] of Object.entries(catalogs)) {
  for (const [key, where] of used) {
    if (!(key in cat)) {
      console.log(`missing ${lang}: ${JSON.stringify(key)} (${where})`)
      missing++
    }
  }
  if (process.argv.includes('--unused')) for (const key of Object.keys(cat)) if (!used.has(key)) console.log(`not used as a literal (${lang}): ${JSON.stringify(key)}`)
}
if (missing) {
  console.log(`${missing} missing translation(s)`)
  process.exit(1)
}
console.log(`i18n: ${used.size} keys checked`)
