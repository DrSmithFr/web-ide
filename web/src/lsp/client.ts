// Code navigation through the language servers run by the pod.
import { request } from '../pod/rpc'
import type { Doc } from '../editor/doc'
import { lspLanguage, lspLanguageId } from '../editor/languages'
import { fileUri, openFile, pathFromUri, relPath, basename } from '../state/project'
import { pick, type PickItem } from '../ui/overlay'
import { toast } from '../ui/toast'

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
  1: ['Fichier', '📄'], 2: ['Module', '📦'], 3: ['Namespace', '{}'], 4: ['Package', '📦'], 5: ['Classe', 'C'], 6: ['Méthode', 'm'],
  7: ['Propriété', 'p'], 8: ['Champ', 'f'], 9: ['Constructeur', 'm'], 10: ['Enum', 'E'], 11: ['Interface', 'I'], 12: ['Fonction', 'ƒ'],
  13: ['Variable', 'v'], 14: ['Constante', 'c'], 15: ['Chaîne', 's'], 16: ['Nombre', '#'], 17: ['Booléen', 'b'], 18: ['Tableau', '[]'],
  19: ['Objet', 'o'], 20: ['Clé', 'k'], 21: ['Null', '∅'], 22: ['Membre d’enum', 'e'], 23: ['Struct', 'S'], 24: ['Événement', '⚡'],
  25: ['Opérateur', '±'], 26: ['Paramètre de type', 'T'],
}

export class NoServer extends Error {}

export async function lsp<T = any>(path: string, method: string, params: any): Promise<T> {
  const lang = lspLanguage(path)
  if (!lang) throw new NoServer('Pas de serveur de langage pour ce type de fichier')
  return request('lsp.request', { lang, method, params })
}

function at(doc: Doc, offset: number) {
  const { line, col } = doc.pos(offset)
  return { textDocument: { uri: fileUri(doc.path) }, position: { line, character: col } }
}

export function toLocations(res: any): Location[] {
  if (!res) return []
  const list = Array.isArray(res) ? res : [res]
  return list
    .map((l: any) => {
      if (l.targetUri) return { path: pathFromUri(l.targetUri), range: l.targetSelectionRange ?? l.targetRange }
      if (l.uri) return { path: pathFromUri(l.uri), range: l.range }
      return null
    })
    .filter((l): l is Location => !!l)
}

export function jump(loc: Location) {
  return openFile({ path: loc.path, line: loc.range.start.line, col: loc.range.start.character })
}

/** One location: jump. Several: a list with the line of each one. */
export async function showLocations(locs: Location[], title: string) {
  if (!locs.length) {
    toast(`${title} : aucun résultat`, 'info')
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
        const f = await request('fs.read', { path: l.path })
        lines.set(l.path, (f.content as string).split('\n'))
      } catch {
        lines.set(l.path, [])
      }
    }
  }
  const items: PickItem<Location>[] = locs.map((l) => ({
    label: (lines.get(l.path)?.[l.range.start.line] ?? '').trim() || basename(l.path),
    detail: `${relPath(l.path)}:${l.range.start.line + 1}`,
    value: l,
  }))
  const chosen = await pick({ placeholder: `${title} (${locs.length})`, items })
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
  else toast(`Serveur de langage : ${(e as Error).message}`, 'error')
}

/** Ctrl+B: declaration, or the usages when the caret already is on the declaration. */
export async function gotoDeclaration(doc: Doc, offset: number) {
  try {
    const locs = toLocations(await lsp(doc.path, 'textDocument/definition', at(doc, offset)))
    const { line, col } = doc.pos(offset)
    const onItself = locs.length > 0 && locs.every((l) => l.path === doc.path && contains(l.range, line, col))
    if (onItself) return findReferences(doc, offset)
    await showLocations(locs, 'Déclaration')
  } catch (e) {
    guard(e)
  }
}

export async function findReferences(doc: Doc, offset: number) {
  try {
    const locs = toLocations(await lsp(doc.path, 'textDocument/references', { ...at(doc, offset), context: { includeDeclaration: false } }))
    await showLocations(locs, 'Usages')
  } catch (e) {
    guard(e)
  }
}

export async function gotoImplementation(doc: Doc, offset: number) {
  try {
    await showLocations(toLocations(await lsp(doc.path, 'textDocument/implementation', at(doc, offset))), 'Implémentations')
  } catch (e) {
    guard(e)
  }
}

export async function gotoTypeDefinition(doc: Doc, offset: number) {
  try {
    await showLocations(toLocations(await lsp(doc.path, 'textDocument/typeDefinition', at(doc, offset))), 'Déclaration de type')
  } catch (e) {
    guard(e)
  }
}

export async function documentSymbols(path: string): Promise<DocumentSymbol[]> {
  const res = await lsp<any[]>(path, 'textDocument/documentSymbol', { textDocument: { uri: fileUri(path) } })
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
      toast('Le curseur doit être dans une méthode de classe', 'info')
      return
    }
    const items = await lsp<any[]>(doc.path, 'textDocument/prepareTypeHierarchy', {
      textDocument: { uri: fileUri(doc.path) },
      position: cls.selectionRange.start,
    }).catch(() => null)
    if (!items?.length) {
      toast("Ce serveur de langage ne fournit pas la hiérarchie de types", 'info')
      return
    }
    const supers = await lsp<any[]>(doc.path, 'typeHierarchy/supertypes', { item: items[0] })
    const found: Location[] = []
    for (const sup of supers ?? []) {
      const path = pathFromUri(sup.uri)
      const syms = await symbolsOf(path)
      const target = flatten(syms).find(({ s }) => s.name === method.name && methodKinds.has(s.kind) && contains(sup.range, s.range.start.line, s.range.start.character))
      if (target) found.push({ path, range: target.s.selectionRange })
    }
    if (!found.length) toast(`Pas de super méthode pour ${method.name}`, 'info')
    else await showLocations(found, 'Super méthode')
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
  const lang = lspLanguage(path)
  const f = await request('fs.read', { path })
  const uri = fileUri(path)
  await request('lsp.notify', { lang, method: 'textDocument/didOpen', params: { textDocument: { uri, languageId: lspLanguageId(path), version: 1, text: f.content } } })
  try {
    return await documentSymbols(path)
  } finally {
    await request('lsp.notify', { lang, method: 'textDocument/didClose', params: { textDocument: { uri } } })
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
  const lang = lspLanguage(path)
  if (!lang) return []
  const res = await request<any[]>('lsp.request', { lang, method: 'workspace/symbol', params: { query } }, signal)
  return (res ?? []).map((s: any) => ({
    name: s.name as string,
    kind: s.kind as number,
    container: (s.containerName as string) ?? '',
    loc: s.location?.range ? { path: pathFromUri(s.location.uri), range: s.location.range } : null,
  }))
}
