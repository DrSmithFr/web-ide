// The Text tab of Search Everywhere: the lines of the project matching the query.
import type { SearchHit, SearchProvider } from '../api'
import { api } from './plugin'

interface Match {
  path: string
  line: number
  col: number
  text: string
  ranges: [number, number][]
}

export const textProvider: SearchProvider = {
  id: 'text',
  label: 'Text',
  placeholder: 'Search the text of the project',
  order: 40,
  delay: 250,
  minChars: 2,
  async find(q, signal) {
    if (q.length < 2) return []
    const r = await api.rpc<{ matches: Match[] }>('grep', { query: q, max: 200 }, signal)
    return r.matches.map((m): SearchHit => {
      const indent = m.text.length - m.text.trimStart().length
      return {
        label: m.text.trim(),
        code: true,
        path: true,
        ranges: m.ranges.map(([a, b]) => [a - indent, b - indent]),
        detail: `${api.project.relPath(m.path)}:${m.line}`,
        run: () => void api.openFile({ path: m.path, line: m.line - 1, col: m.col }),
      }
    })
  },
}
