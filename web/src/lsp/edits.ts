// LSP text edits and workspace edits, applied to the open buffers or, for the files that are
// not open, written directly through the pod.
import { request } from '../pod/rpc'
import { lspLanguage } from '../editor/languages'
import { getDoc, pathFromUri } from '../state/project'
import type { Range } from './client'

export interface TextEdit {
  range: Range
  newText: string
}

const caps = new Map<string, Promise<any>>()

/** Server capabilities of a language (from initialize), cached. */
export function capabilities(path: string): Promise<any> {
  const lang = lspLanguage(path)
  if (!lang) return Promise.resolve(null)
  let p = caps.get(lang)
  if (!p) {
    p = request('lsp.capabilities', { lang }).catch(() => {
      caps.delete(lang)
      return null
    })
    caps.set(lang, p)
  }
  return p
}

/** Offset of an LSP position in a text (UTF-16 columns, like JavaScript strings). */
export function offsetIn(text: string, starts: number[], line: number, character: number) {
  if (line >= starts.length) return text.length
  const end = line + 1 < starts.length ? starts[line + 1] - 1 : text.length
  return Math.min(starts[line] + character, end)
}

export function lineStarts(text: string) {
  const s = [0]
  for (let i = text.indexOf('\n'); i >= 0; i = text.indexOf('\n', i + 1)) s.push(i + 1)
  return s
}

export function toOffsets(text: string, edits: TextEdit[]) {
  const starts = lineStarts(text)
  return edits.map((e) => ({
    from: offsetIn(text, starts, e.range.start.line, e.range.start.character),
    to: offsetIn(text, starts, e.range.end.line, e.range.end.character),
    text: e.newText,
  }))
}

/** Applies edits to a string (descending order, so offsets stay valid). */
export function applyToString(text: string, edits: TextEdit[]) {
  const list = toOffsets(text, edits).sort((a, b) => b.from - a.from || b.to - a.to)
  let out = text
  for (const e of list) out = out.slice(0, e.from) + e.text + out.slice(e.to)
  return out
}

export interface WorkspaceResult {
  files: number
  edits: number
  written: string[]
  skipped: string[]
}

/** Applies a WorkspaceEdit: open buffers are modified (to be saved), other files are written. */
export async function applyWorkspaceEdit(we: any, origin: unknown): Promise<WorkspaceResult> {
  const byPath = new Map<string, TextEdit[]>()
  const skipped: string[] = []
  const add = (uri: string, edits: TextEdit[]) => {
    const p = pathFromUri(uri)
    byPath.set(p, [...(byPath.get(p) ?? []), ...edits])
  }
  for (const [uri, edits] of Object.entries<TextEdit[]>(we?.changes ?? {})) add(uri, edits)
  for (const dc of we?.documentChanges ?? []) {
    if (dc.textDocument) add(dc.textDocument.uri, dc.edits)
    else skipped.push(`${dc.kind} ${dc.uri ?? dc.oldUri ?? ''}`)
  }
  const res: WorkspaceResult = { files: 0, edits: 0, written: [], skipped }
  for (const [path, edits] of byPath) {
    if (!edits.length) continue
    res.files++
    res.edits += edits.length
    const doc = getDoc(path)
    if (doc && !doc.readOnly) {
      doc.applyEdits(toOffsets(doc.text, edits), origin)
      continue
    }
    const f = await request('fs.read', { path })
    if (f.binary || f.readOnly) {
      skipped.push(path)
      continue
    }
    await request('fs.write', { path, content: applyToString(f.content, edits) })
    res.written.push(path)
  }
  return res
}
