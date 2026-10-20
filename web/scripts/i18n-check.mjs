// Checks the translation catalogs of every language of src/i18n/languages.json (English
// has none): every literal passed to t() / tn() in the sources must have an entry in each
// catalog, the catalogs have the same keys, and a translation keeps the {parameters} of its
// key. Keys used through variables (labels of data tables) cannot be checked against the
// sources; catalog entries never seen as literals are listed with --unused.
import { readFileSync, readdirSync, statSync } from 'node:fs'
import { join, relative } from 'node:path'

const src = new URL('../src/', import.meta.url).pathname
const read = (f) => JSON.parse(readFileSync(join(src, 'i18n', f), 'utf8'))
const catalogs = Object.fromEntries(read('languages.json').filter((l) => l.id !== 'en').map((l) => [l.id, read(`${l.id}.json`)]))

const files = []
const walk = (d) => {
  for (const name of readdirSync(d)) {
    const p = join(d, name)
    if (statSync(p).isDirectory()) walk(p)
    else if (/\.(ts|tsx)$/.test(name)) files.push(p)
  }
}
walk(src)
// The page modules of the built-in plugins (plugins/<id>/web at the root of the repository).
const plugins = new URL('../../plugins/', import.meta.url).pathname
for (const id of readdirSync(plugins)) if (statSync(join(plugins, id)).isDirectory() && statSync(join(plugins, id, 'web'), { throwIfNoEntry: false })?.isDirectory()) walk(join(plugins, id, 'web'))

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
  const params = (s) => [...s.matchAll(/\{(\w+)\}/g)].map((m) => m[1]).sort().join()
  for (const [key, text] of Object.entries(cat)) {
    if (params(key) !== params(text)) {
      console.log(`parameters differ in ${lang}: ${JSON.stringify(key)}`)
      missing++
    }
    for (const [other, o] of Object.entries(catalogs)) {
      if (other !== lang && !(key in o)) {
        console.log(`missing ${other}: ${JSON.stringify(key)} (present in ${lang})`)
        missing++
      }
    }
  }
  if (process.argv.includes('--unused')) for (const key of Object.keys(cat)) if (!used.has(key)) console.log(`not used as a literal (${lang}): ${JSON.stringify(key)}`)
}
if (missing) {
  console.log(`${missing} missing translation(s)`)
  process.exit(1)
}
console.log(`i18n: ${used.size} keys checked`)
