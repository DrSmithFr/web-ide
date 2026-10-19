// The Files tab of Search Everywhere: the files of the project by name or path (fuzzy), the
// recent files first without a query.
import type { SearchHit, SearchProvider } from '../api'
import { api, FileIcon } from './plugin'

let fileCache: { at: number; files: string[] } | null = null
async function projectFiles() {
  if (!fileCache || Date.now() - fileCache.at > 15000) fileCache = { at: Date.now(), files: await api.rpc<string[]>('search.files') }
  return fileCache.files
}

const fileHit = (f: string): SearchHit => {
  const name = api.project.basename(f)
  return { label: name, detail: api.project.relPath(f), path: true, icon: () => <FileIcon name={name} />, run: () => void api.openFile(f) }
}

export const filesProvider: SearchProvider = {
  id: 'files',
  label: 'Files',
  placeholder: 'Go to file (name or path, fuzzy search)',
  order: 10,
  inAll: 8,
  delay: 0,
  max: 100,
  async find(q, _signal, max) {
    const list = await projectFiles()
    if (!q) return [...new Set([...api.recentFiles(), ...list])].slice(0, max).map(fileHit)
    const { fuzzy } = api.ui
    return list
      .map((f) => ({ f, s: fuzzy(q, api.project.basename(f)) * 2 + fuzzy(q, api.project.relPath(f)) }))
      .filter((x) => x.s > 0)
      .sort((a, b) => b.s - a.s)
      .slice(0, max)
      .map(({ f }) => fileHit(f))
  },
}
