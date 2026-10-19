// The Symbols tab of Search Everywhere: the symbols of the project (workspace/symbol) found by the
// server of the active file, else of the language the pod detected.
import type { SearchHit, SearchProvider } from '../api'
import { api } from './plugin'
import { jump, symbolKinds, workspaceSymbols } from './client'

/** A path of a language with a running server: the active file, else the detected language. */
async function symbolPath() {
  const path = api.activeFile() ?? ''
  if (api.editor.serverLanguage(path)) return path
  const st = await api.rpc<any[]>('lsp.status').catch(() => [])
  const lang = st.find((s) => s.detected && s.command)?.lang
  const ext: Record<string, string> = { go: '/x.go', php: '/x.php', python: '/x.py', typescript: '/x.ts' }
  return lang ? ext[lang] : ''
}

export const symbolsProvider: SearchProvider = {
  id: 'symbols',
  label: 'Symbols',
  placeholder: 'Go to symbol (class, function, method…)',
  order: 20,
  inAll: 8,
  delay: 150,
  minChars: 2,
  async find(q, signal, max) {
    if (q.length < 2) return []
    const path = await symbolPath()
    if (!path) return []
    const list = await workspaceSymbols(path, q, signal).catch(() => [])
    return list
      .filter((s) => s.loc)
      .slice(0, max)
      .map(
        (s): SearchHit => ({
          label: s.name,
          code: true,
          path: true,
          icon: () => <span class="se-glyph">{symbolKinds[s.kind]?.[1] ?? '·'}</span>,
          detail: `${s.container ? s.container + ' · ' : ''}${api.project.relPath(s.loc!.path)}`,
          run: () => jump(s.loc!),
        }),
      )
  },
}
