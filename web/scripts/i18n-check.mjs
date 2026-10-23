// Checks the translation catalogs of every language of src/i18n/languages.json (English
// has none): the core's (src/i18n/<lang>.json) and those of each built-in plugin
// (plugins/<id>/i18n/<lang>.json at the root of the repository, shared with its messages in the
// pod). Every literal passed to t() / tn() must have an entry in each language: in the core's
// catalog for the sources of the core, in the plugin's or else the core's for the page module of
// a plugin and the labels of its manifest. A catalog has the same keys in every language, a
// translation keeps the {parameters} of its key, and a key of a plugin is not in the core's too.
// Keys used through variables (labels of data tables) cannot be checked against the sources;
// catalog entries never seen as literals are listed with --unused.
import { readFileSync, readdirSync, statSync } from 'node:fs'
import { join, relative } from 'node:path'

const src = new URL('../src/', import.meta.url).pathname
const pluginsDir = new URL('../../plugins/', import.meta.url).pathname
const readJSON = (p) => JSON.parse(readFileSync(p, 'utf8'))
const langs = readJSON(join(src, 'i18n', 'languages.json')).map((l) => l.id).filter((l) => l !== 'en')
const catalogsOf = (dir) => Object.fromEntries(langs.map((l) => [l, readJSON(join(dir, `${l}.json`))]))

// The owners: the core, then each plugin with a catalog; their sources and the catalogs a
// key may be found in.
const core = { name: 'core', catalogs: catalogsOf(join(src, 'i18n')), files: [], used: new Map() }
const owners = [core]
const isDir = (p) => { try { return statSync(p).isDirectory() } catch { return false } }
for (const id of readdirSync(pluginsDir)) {
  const dir = join(pluginsDir, id)
  if (!isDir(join(dir, 'i18n'))) continue
  owners.push({ name: `plugins/${id}`, dir, catalogs: catalogsOf(join(dir, 'i18n')), files: [], used: new Map() })
}

const walk = (d, files) => {
  for (const name of readdirSync(d)) {
    const p = join(d, name)
    if (statSync(p).isDirectory()) walk(p, files)
    else if (/\.(ts|tsx)$/.test(name)) files.push(p)
  }
}
walk(src, core.files)
for (const o of owners.slice(1)) if (isDir(join(o.dir, 'web'))) walk(join(o.dir, 'web'), o.files)

const str = String.raw`'((?:\\.|[^'\\])*)'|"((?:\\.|[^"\\])*)"`
const call = new RegExp(String.raw`(?<![\w.$])t\(\s*(?:${str})`, 'g')
const plural = new RegExp(String.raw`(?<![\w.$])tn\([^,]+,\s*(?:${str})\s*,\s*(?:${str})`, 'g')
const unescape = (s) => s.replace(/\\(.)/g, (_, c) => ({ n: '\n', t: '\t' })[c] ?? c)

const root = new URL('../../', import.meta.url).pathname
for (const o of owners) {
  const add = (k, where) => k !== undefined && !o.used.has(unescape(k)) && o.used.set(unescape(k), where)
  for (const f of o.files) {
    const text = readFileSync(f, 'utf8')
    const at = (i) => `${relative(root, f)}:${text.slice(0, i).split('\n').length}`
    for (const m of text.matchAll(call)) add(m[1] ?? m[2], at(m.index))
    for (const m of text.matchAll(plural)) {
      add(m[1] ?? m[2], at(m.index))
      add(m[3] ?? m[4], at(m.index))
    }
  }
  // The labels of the manifest, shown by the core through t().
  if (o.dir) {
    const c = readJSON(join(o.dir, 'plugin.json')).contributes ?? {}
    for (const list of Object.values(c)) if (Array.isArray(list)) for (const x of list) if (x?.label) add(JSON.stringify(x.label).slice(1, -1), `${o.name}/plugin.json`)
  }
}

let missing = 0
const report = (s) => {
  console.log(s)
  missing++
}
const params = (s) => [...s.matchAll(/\{(\w+)\}/g)].map((m) => m[1]).sort().join()
for (const o of owners) {
  for (const lang of langs) {
    for (const [key, where] of o.used) {
      if (!(key in o.catalogs[lang]) && !(o !== core && key in core.catalogs[lang])) report(`missing ${lang}: ${JSON.stringify(key)} (${where})`)
    }
    for (const [key, text] of Object.entries(o.catalogs[lang])) {
      if (params(key) !== params(text)) report(`parameters differ in ${o.name} ${lang}: ${JSON.stringify(key)}`)
      for (const other of langs) if (other !== lang && !(key in o.catalogs[other])) report(`missing ${o.name} ${other}: ${JSON.stringify(key)} (present in ${lang})`)
      if (o !== core && key in core.catalogs[lang]) report(`${o.name} ${lang}: ${JSON.stringify(key)} is in the core catalog too`)
    }
  }
  if (process.argv.includes('--unused')) for (const key of Object.keys(o.catalogs[langs[0]])) if (!o.used.has(key)) console.log(`not used as a literal (${o.name}): ${JSON.stringify(key)}`)
}
if (missing) {
  console.log(`${missing} missing translation(s)`)
  process.exit(1)
}
console.log(`i18n: ${owners.reduce((n, o) => n + o.used.size, 0)} keys checked in ${owners.length} catalogs`)
