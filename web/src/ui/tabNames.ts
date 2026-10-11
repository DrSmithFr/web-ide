// File tabs that share a name show the end of their folder path before it, the shortest one
// that tells them apart: src/index.ts and lib/index.ts; a/x/index.ts and b/x/index.ts.

/** The folders to show before each relative path whose name another path shares ("src/"). */
export function distinctDirs(paths: string[]): Map<string, string> {
  const byName = new Map<string, string[]>()
  for (const p of new Set(paths)) {
    const name = p.slice(p.lastIndexOf('/') + 1)
    byName.set(name, [...(byName.get(name) ?? []), p])
  }
  const out = new Map<string, string>()
  for (const group of byName.values()) {
    if (group.length < 2) continue
    const dirs = group.map((p) => p.split('/').slice(0, -1))
    const depth = Math.max(...dirs.map((d) => d.length))
    let k = 1
    const end = (d: string[]) => d.slice(Math.max(0, d.length - k)).join('/')
    while (k < depth && new Set(dirs.map(end)).size < group.length) k++
    group.forEach((p, i) => {
      const e = end(dirs[i])
      if (e) out.set(p, e + '/')
    })
  }
  return out
}
