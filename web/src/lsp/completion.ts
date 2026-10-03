// Code completion: LSP textDocument/completion, client-side fuzzy filtering while typing,
// snippets turned into plain text, additional edits (automatic imports) applied with the item.
import { request } from '../pod/rpc'
import type { Doc } from '../editor/doc'
import { lspLanguage } from '../editor/languages'
import { fileUri, flushLsp } from '../state/project'
import { fuzzy } from '../ui/overlay'
import { capabilities, toOffsets, type TextEdit } from './edits'

export interface CompletionItem {
  label: string
  kind?: number
  detail?: string
  documentation?: string | { kind: string; value: string }
  sortText?: string
  filterText?: string
  insertText?: string
  insertTextFormat?: number
  textEdit?: TextEdit | { newText: string; insert: TextEdit['range']; replace: TextEdit['range'] }
  additionalTextEdits?: TextEdit[]
  labelDetails?: { detail?: string; description?: string }
  data?: unknown
  /** Words of the buffer, when no language server answers. */
  local?: boolean
}

export const completionKinds: Record<number, [string, string]> = {
  1: ['Text', 'abc'], 2: ['Method', 'm'], 3: ['Function', 'ƒ'], 4: ['Constructor', 'm'], 5: ['Field', 'f'], 6: ['Variable', 'v'],
  7: ['Class', 'C'], 8: ['Interface', 'I'], 9: ['Module', '{}'], 10: ['Property', 'p'], 11: ['Unit', 'u'], 12: ['Value', '='],
  13: ['Enum', 'E'], 14: ['Keyword', 'k'], 15: ['Snippet', '⌘'], 16: ['Color', '#'], 17: ['File', '📄'], 18: ['Reference', '&'],
  19: ['Folder', '📁'], 20: ['Enum member', 'e'], 21: ['Constant', 'c'], 22: ['Struct', 'S'], 23: ['Event', '⚡'],
  24: ['Operator', '±'], 25: ['Type parameter', 'T'],
}

/** Characters of an identifier, by language ($ belongs to PHP and JS names). */
export function isWordChar(ch: string, lang: string) {
  return /[\p{L}\p{N}_]/u.test(ch) || (ch === '$' && ['php', 'javascript', 'typescript', 'shell'].includes(lang))
}

export function wordStart(doc: Doc, offset: number) {
  let i = offset
  while (i > 0 && isWordChar(doc.text[i - 1], doc.lang)) i--
  return i
}

export interface CompletionList {
  items: CompletionItem[]
  incomplete: boolean
  resolve: boolean
}

/** Asks the language server (or falls back to the words of the buffer). */
export async function complete(doc: Doc, offset: number, trigger: string | null, signal: AbortSignal): Promise<CompletionList> {
  const lang = lspLanguage(doc.path)
  const caps = lang ? await capabilities(doc.path) : null
  if (!caps?.completionProvider) return { items: bufferWords(doc, offset), incomplete: false, resolve: false }
  flushLsp(doc.path)
  const { line, col } = doc.pos(offset)
  const res = await request(
    'lsp.request',
    {
      lang,
      method: 'textDocument/completion',
      params: {
        textDocument: { uri: fileUri(doc.path) },
        position: { line, character: col },
        context: trigger ? { triggerKind: 2, triggerCharacter: trigger } : { triggerKind: 1 },
      },
    },
    signal,
  )
  const items: CompletionItem[] = Array.isArray(res) ? res : res?.items ?? []
  return { items, incomplete: !Array.isArray(res) && !!res?.isIncomplete, resolve: !!caps.completionProvider.resolveProvider }
}

export async function triggerCharacters(doc: Doc): Promise<string[]> {
  const caps = await capabilities(doc.path)
  return caps?.completionProvider?.triggerCharacters ?? []
}

export async function resolveItem(doc: Doc, item: CompletionItem): Promise<CompletionItem> {
  const lang = lspLanguage(doc.path)
  try {
    return { ...item, ...(await request('lsp.request', { lang, method: 'completionItem/resolve', params: item })) }
  } catch {
    return item
  }
}

/** Identifiers of the buffer, as a completion of last resort. */
function bufferWords(doc: Doc, offset: number): CompletionItem[] {
  const start = wordStart(doc, offset)
  const seen = new Set<string>()
  const re = doc.lang === 'php' ? /\$?[\p{L}_][\p{L}\p{N}_]{2,}/gu : /[\p{L}_$][\p{L}\p{N}_$]{2,}/gu
  const text = doc.text.length > 2_000_000 ? doc.text.slice(Math.max(0, offset - 1_000_000), offset + 1_000_000) : doc.text
  const own = doc.text.slice(start, offset)
  for (const m of text.matchAll(re)) {
    if (m[0] !== own) seen.add(m[0])
    if (seen.size > 5000) break
  }
  return [...seen].map((w) => ({ label: w, kind: 1, local: true }))
}

/** Items matching the typed prefix, best first. */
export function filterItems(items: CompletionItem[], prefix: string): CompletionItem[] {
  if (!prefix) return [...items].sort((a, b) => (a.sortText ?? a.label).localeCompare(b.sortText ?? b.label)).slice(0, 300)
  const scored: { it: CompletionItem; s: number }[] = []
  for (const it of items) {
    const key = it.filterText ?? it.label
    let s = fuzzy(prefix, key)
    if (s <= 0) continue
    if (key.startsWith(prefix)) s += 20
    else if (key.toLowerCase().startsWith(prefix.toLowerCase())) s += 10
    if (it.local) s -= 5
    scored.push({ it, s })
  }
  scored.sort((a, b) => b.s - a.s || (a.it.sortText ?? a.it.label).localeCompare(b.it.sortText ?? b.it.label))
  return scored.slice(0, 300).map((x) => x.it)
}

/** Snippet syntax to plain text; returns the text and the caret (first tab stop, or $0). */
export function expandSnippet(snippet: string): { text: string; caret: number | null; selEnd: number | null } {
  let out = ''
  let caret: number | null = null
  let selEnd: number | null = null
  let firstStop = Infinity
  let finalStop: number | null = null
  const re = /\\([$}\\])|\$(\d+)|\$\{(\d+)(?::((?:[^}\\]|\\.)*)|\|([^|]*)\|)?\}|\$\{?[A-Za-z_]+\}?/g
  let last = 0
  for (let m = re.exec(snippet); m; m = re.exec(snippet)) {
    out += snippet.slice(last, m.index)
    last = re.lastIndex
    if (m[1]) {
      out += m[1]
      continue
    }
    const n = Number(m[2] ?? m[3])
    if (Number.isNaN(n)) continue // variables ($TM_FILENAME...): dropped
    const value = m[4] !== undefined ? m[4].replace(/\\(.)/g, '$1').replace(/\$\{\d+:?([^}]*)\}|\$\d+/g, '$1') : m[5] !== undefined ? m[5].split(',')[0] : ''
    if (n === 0) finalStop = out.length
    else if (n < firstStop) {
      firstStop = n
      caret = out.length
      selEnd = out.length + value.length
    }
    out += value
  }
  out += snippet.slice(last)
  if (caret === null && finalStop !== null) caret = selEnd = finalStop
  return { text: out, caret, selEnd }
}

/**
 * Applies an item: the main edit replaces the word being typed (up to the caret), the
 * additional edits (imports) are applied in the same undo step. Returns the new selection.
 */
export function applyItem(doc: Doc, item: CompletionItem, wordFrom: number, caret: number, origin: unknown): { anchor: number; head: number } {
  let from = wordFrom
  let to = caret
  let text = item.insertText ?? item.label
  if (item.textEdit) {
    const range = 'range' in item.textEdit ? item.textEdit.range : item.textEdit.replace
    const [o] = toOffsets(doc.text, [{ range, newText: '' }])
    from = Math.min(o.from, caret)
    to = Math.max(o.to, caret)
    text = item.textEdit.newText
  }
  let pos: number | null = null
  let selEnd: number | null = null
  if (item.insertTextFormat === 2) {
    const s = expandSnippet(text)
    text = s.text
    pos = s.caret
    selEnd = s.selEnd
  }
  const extra = item.additionalTextEdits ? toOffsets(doc.text, item.additionalTextEdits).filter((e) => e.to <= from || e.from >= to) : []
  doc.applyEdits([{ from, to, text }, ...extra], origin)
  // Start of the inserted text, moved by the edits placed before it (imports at the top).
  const base = extra.reduce((d, e) => (e.to <= from ? d + e.text.length - (e.to - e.from) : d), 0) + from
  return { anchor: base + (pos ?? text.length), head: base + (selEnd ?? pos ?? text.length) }
}

export function docText(item: CompletionItem): string {
  const d = item.documentation
  if (!d) return ''
  return (typeof d === 'string' ? d : d.value).replace(/```\w*\n?/g, '').trim()
}
