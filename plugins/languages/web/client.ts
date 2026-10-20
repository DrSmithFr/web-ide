// Code navigation through the language servers run by the pod.
import type { Doc, PickItem } from '@ide/api'
import { api, pick, t, toast } from './plugin'

export interface Position {
  line: number
  character: number
}
export interface Range {
  start: Position
  end: Position
}
export interface Location {
  path: string
  range: Range
}

export interface DocumentSymbol {
  name: string
  detail?: string
  kind: number
  range: Range
  selectionRange: Range
  children?: DocumentSymbol[]
}

export const symbolKinds: Record<number, [string, string]> = {
  1: ['File', '📄'], 2: ['Module', '📦'], 3: ['Namespace', '{}'], 4: ['Package', '📦'], 5: ['Class', 'C'], 6: ['Method', 'm'],
  7: ['Property', 'p'], 8: ['Field', 'f'], 9: ['Constructor', 'm'], 10: ['Enum', 'E'], 11: ['Interface', 'I'], 12: ['Function', 'ƒ'],
  13: ['Variable', 'v'], 14: ['Constant', 'c'], 15: ['String', 's'], 16: ['Number', '#'], 17: ['Boolean', 'b'], 18: ['Array', '[]'],
  19: ['Object', 'o'], 20: ['Key', 'k'], 21: ['Null', '∅'], 22: ['Enum member', 'e'], 23: ['Struct', 'S'], 24: ['Event', '⚡'],
  25: ['Operator', '±'], 26: ['Type parameter', 'T'],
}

export class NoServer extends Error {}

export async function lsp<T = any>(path: string, method: string, params: any): Promise<T> {
  const lang = api.editor.serverLanguage(path)
  if (!lang) throw new NoServer(t('No language server for this file type'))
  return api.rpc('lsp.request', { lang, method, params })
}

function at(doc: Doc, offset: number) {
  const { line, col } = doc.pos(offset)
  return { textDocument: { uri: api.project.fileUri(doc.path) }, position: { line, character: col } }
}

export function toLocations(res: any): Location[] {
  if (!res) return []
  const list = Array.isArray(res) ? res : [res]
  return list
    .map((l: any) => {
      if (l.targetUri) return { path: api.project.pathFromUri(l.targetUri), range: l.targetSelectionRange ?? l.targetRange }
      if (l.uri) return { path: api.project.pathFromUri(l.uri), range: l.range }
      return null
    })
    .filter((l): l is Location => !!l)
}

export function jump(loc: Location) {
  return api.openFile({ path: loc.path, line: loc.range.start.line, col: loc.range.start.character })
}

/** One location: jump. Several: a list with the line of each one. */
export async function showLocations(locs: Location[], title: string) {
  if (!locs.length) {
    toast(t('{title}: no result', { title }), 'info')
    return
  }
  if (locs.length === 1) {
    await jump(locs[0])
    return
  }
  const lines = new Map<string, string[]>()
  for (const l of locs.slice(0, 200)) {
    if (!lines.has(l.path)) {
      try {
        const f = await api.files.read(l.path)
        lines.set(l.path, (f.content as string).split('\n'))
      } catch {
        lines.set(l.path, [])
      }
    }
  }
  const items: PickItem<Location>[] = locs.map((l) => ({
    label: (lines.get(l.path)?.[l.range.start.line] ?? '').trim() || api.project.basename(l.path),
    detail: `${api.project.relPath(l.path)}:${l.range.start.line + 1}`,
    value: l,
  }))
  const chosen = await pick({ placeholder: `${title} (${locs.length})`, items, codeLabel: true, pathDetail: true })
  if (chosen) await jump(chosen)
}

function contains(r: Range, line: number, ch: number) {
  if (line < r.start.line || line > r.end.line) return false
  if (line === r.start.line && ch < r.start.character) return false
  if (line === r.end.line && ch > r.end.character) return false
  return true
}

function guard(e: unknown) {
  if (e instanceof NoServer) toast(e.message, 'info')
  else toast(t('Language server: {message}', { message: (e as Error).message }), 'error')
}

/** Ctrl+B: declaration, or the usages when the caret already is on the declaration. */
export async function gotoDeclaration(doc: Doc, offset: number) {
  try {
    const locs = toLocations(await lsp(doc.path, 'textDocument/definition', at(doc, offset)))
    const { line, col } = doc.pos(offset)
    const onItself = locs.length > 0 && locs.every((l) => l.path === doc.path && contains(l.range, line, col))
    if (onItself) return findReferences(doc, offset)
    await showLocations(locs, t('Declaration'))
  } catch (e) {
    guard(e)
  }
}

export async function findReferences(doc: Doc, offset: number) {
  try {
    const locs = toLocations(await lsp(doc.path, 'textDocument/references', { ...at(doc, offset), context: { includeDeclaration: false } }))
    await showLocations(locs, t('Usages'))
  } catch (e) {
    guard(e)
  }
}

export async function gotoImplementation(doc: Doc, offset: number) {
  try {
    await showLocations(toLocations(await lsp(doc.path, 'textDocument/implementation', at(doc, offset))), t('Implementations'))
  } catch (e) {
    guard(e)
  }
}

export async function gotoTypeDefinition(doc: Doc, offset: number) {
  try {
    await showLocations(toLocations(await lsp(doc.path, 'textDocument/typeDefinition', at(doc, offset))), t('Type declaration'))
  } catch (e) {
    guard(e)
  }
}

export async function documentSymbols(path: string): Promise<DocumentSymbol[]> {
  const res = await lsp<any[]>(path, 'textDocument/documentSymbol', { textDocument: { uri: api.project.fileUri(path) } })
  if (!res) return []
  // SymbolInformation[] (flat) is converted to the hierarchical form.
  return res.map((s: any) => (s.selectionRange ? s : { name: s.name, kind: s.kind, detail: s.containerName, range: s.location.range, selectionRange: s.location.range }))
}

export function flatten(symbols: DocumentSymbol[], depth = 0, out: { s: DocumentSymbol; depth: number }[] = []) {
  for (const s of symbols) {
    out.push({ s, depth })
    if (s.children) flatten(s.children, depth + 1, out)
  }
  return out
}

/** Chain of symbols containing a position, outermost first. */
function enclosing(symbols: DocumentSymbol[], line: number, ch: number): DocumentSymbol[] {
  for (const s of symbols) {
    if (contains(s.range, line, ch)) return [s, ...enclosing(s.children ?? [], line, ch)]
  }
  return []
}

const methodKinds = new Set([6, 9, 12])
const typeKinds = new Set([5, 10, 11, 23])

/** Super method: type hierarchy of the enclosing class, then the method of the same name. */
export async function gotoSuperMethod(doc: Doc, offset: number) {
  try {
    const { line, col } = doc.pos(offset)
    const chain = enclosing(await documentSymbols(doc.path), line, col)
    const method = [...chain].reverse().find((s) => methodKinds.has(s.kind))
    const cls = [...chain].reverse().find((s) => typeKinds.has(s.kind))
    if (!method || !cls) {
      toast(t('The caret must be in a class method'), 'info')
      return
    }
    const items = await lsp<any[]>(doc.path, 'textDocument/prepareTypeHierarchy', {
      textDocument: { uri: api.project.fileUri(doc.path) },
      position: cls.selectionRange.start,
    }).catch(() => null)
    if (!items?.length) {
      toast(t('This language server does not provide the type hierarchy'), 'info')
      return
    }
    const supers = await lsp<any[]>(doc.path, 'typeHierarchy/supertypes', { item: items[0] })
    const found: Location[] = []
    for (const sup of supers ?? []) {
      const path = api.project.pathFromUri(sup.uri)
      const syms = await symbolsOf(path)
      const target = flatten(syms).find(({ s }) => s.name === method.name && methodKinds.has(s.kind) && contains(sup.range, s.range.start.line, s.range.start.character))
      if (target) found.push({ path, range: target.s.selectionRange })
    }
    if (!found.length) toast(t('No super method for {name}', { name: method.name }), 'info')
    else await showLocations(found, t('Super method'))
  } catch (e) {
    guard(e)
  }
}

/** Symbols of a file that may not be open: opened in the server for the request. */
async function symbolsOf(path: string): Promise<DocumentSymbol[]> {
  try {
    const s = await documentSymbols(path)
    if (s.length) return s
  } catch {
    /* fall through */
  }
  const lang = api.editor.serverLanguage(path)
  const f = await api.files.read(path)
  const uri = api.project.fileUri(path)
  await api.rpc('lsp.notify', { lang, method: 'textDocument/didOpen', params: { textDocument: { uri, languageId: api.editor.serverLanguageId(path), version: 1, text: f.content } } })
  try {
    return await documentSymbols(path)
  } finally {
    await api.rpc('lsp.notify', { lang, method: 'textDocument/didClose', params: { textDocument: { uri } } })
  }
}

export async function hoverText(doc: Doc, offset: number): Promise<string> {
  const res = await lsp<any>(doc.path, 'textDocument/hover', at(doc, offset))
  if (!res?.contents) return ''
  const part = (c: any): string => (typeof c === 'string' ? c : c.value ?? '')
  const text = Array.isArray(res.contents) ? res.contents.map(part).join('\n\n') : part(res.contents)
  return text.replace(/```\w*\n?/g, '').trim()
}

export async function workspaceSymbols(path: string, query: string, signal?: AbortSignal) {
  const lang = api.editor.serverLanguage(path)
  if (!lang) return []
  const res = await api.rpc<any[]>('lsp.request', { lang, method: 'workspace/symbol', params: { query } }, signal)
  return (res ?? []).map((s: any) => ({
    name: s.name as string,
    kind: s.kind as number,
    container: (s.containerName as string) ?? '',
    loc: s.location?.range ? { path: api.project.pathFromUri(s.location.uri), range: s.location.range } : null,
  }))
}
